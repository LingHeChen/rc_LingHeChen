defmodule RcNotification.E2ETest do
  @moduledoc """
  End-to-end tests covering the full delivery pipeline:
    HTTP API → Ecto/Oban enqueue → Worker perform → real HTTP delivery to target server

  Uses Bandit to spin up a real in-process HTTP server as the target, mirroring
  what the Go version does with httptest.NewServer.

  Requires DATABASE_URL or a running local PostgreSQL.
  Run with: mix test --include integration test/e2e/
  """
  use RcNotification.DataCase, async: false
  use Oban.Testing, repo: RcNotification.Repo

  @moduletag :integration

  import Plug.Test
  import Plug.Conn

  alias RcNotification.{Router, Vendors}

  @opts Router.init([])

  # Switch the HTTP client to the real Req implementation for e2e tests.
  # Unit/integration tests use the Mox mock; e2e tests need actual TCP calls
  # so the local Bandit test server can receive them.
  setup_all do
    prev = Application.get_env(:rc_notification, :http_client)
    Application.put_env(:rc_notification, :http_client, RcNotification.HTTPClient.Req)
    on_exit(fn -> Application.put_env(:rc_notification, :http_client, prev) end)
    :ok
  end

  # ── Helpers ──────────────────────────────────────────────────────────────────

  defp enqueue(vendor_name, body_map, idempotency_key) do
    payload = Jason.encode!(%{body: body_map})

    conn(:post, "/notifications/#{vendor_name}", payload)
    |> put_req_header("content-type", "application/json")
    |> put_req_header("idempotency-key", idempotency_key)
    |> Router.call(@opts)
  end

  defp open_server(handler) do
    state =
      start_supervised!(
        Supervisor.child_spec({Agent, fn -> %{calls: 0, handler: handler} end},
          id: make_ref()
        )
      )

    server =
      start_supervised!(
        Supervisor.child_spec(
          {Bandit,
           plug: {RcNotification.HTTPTestServer, state},
           ip: {127, 0, 0, 1},
           port: 0,
           startup_log: false},
          id: make_ref()
        )
      )

    {:ok, {_address, port}} = ThousandIsland.listener_info(server)
    %{port: port, state: state}
  end

  # ── Tests ─────────────────────────────────────────────────────────────────────

  test "full pipeline: enqueue via API, worker delivers body to target" do
    agent = start_supervised!({Agent, fn -> [] end})

    server =
      open_server(fn conn ->
        assert conn.method == "POST"
        assert conn.request_path == "/hook"
        {:ok, raw, conn} = Plug.Conn.read_body(conn)
        Agent.update(agent, &[raw | &1])
        Plug.Conn.send_resp(conn, 200, "ok")
      end)

    {:ok, _} =
      Vendors.create(%{
        "name" => "e2e-basic",
        "target_url" => "http://localhost:#{server.port}/hook",
        "method" => "POST",
        "headers" => %{"Content-Type" => "application/json"}
      })

    conn = enqueue("e2e-basic", %{event: "user.registered", user_id: "u42"}, "e2e-001")
    assert conn.status == 202

    assert %{success: 1} = Oban.drain_queue(queue: :delivery)

    [body] = Agent.get(agent, & &1)
    parsed = Jason.decode!(body)
    assert parsed["event"] == "user.registered"
    assert parsed["user_id"] == "u42"
  end

  test "body template is rendered before delivery" do
    agent = start_supervised!({Agent, fn -> [] end})

    server =
      open_server(fn conn ->
        assert conn.method == "POST"
        assert conn.request_path == "/crm"
        {:ok, raw, conn} = Plug.Conn.read_body(conn)
        Agent.update(agent, &[raw | &1])
        Plug.Conn.send_resp(conn, 200, "ok")
      end)

    {:ok, _} =
      Vendors.create(%{
        "name" => "e2e-crm",
        "target_url" => "http://localhost:#{server.port}/crm",
        "method" => "POST",
        "headers" => %{"Content-Type" => "application/json"},
        "body_tpl" => ~s({"crm_id":"<%= @user_id %>","status":"paid"})
      })

    conn = enqueue("e2e-crm", %{user_id: "u99"}, "e2e-tpl-001")
    assert conn.status == 202

    assert %{success: 1} = Oban.drain_queue(queue: :delivery)

    [body] = Agent.get(agent, & &1)
    parsed = Jason.decode!(body)
    assert parsed["crm_id"] == "u99"
    assert parsed["status"] == "paid"
  end

  test "vendor headers are forwarded and override extra_headers" do
    agent = start_supervised!({Agent, fn -> [] end})

    server =
      open_server(fn conn ->
        assert conn.method == "POST"
        assert conn.request_path == "/secure"
        Agent.update(agent, &[conn.req_headers | &1])
        Plug.Conn.send_resp(conn, 200, "ok")
      end)

    {:ok, _} =
      Vendors.create(%{
        "name" => "e2e-secure",
        "target_url" => "http://localhost:#{server.port}/secure",
        "method" => "POST",
        "headers" => %{
          "Content-Type" => "application/json",
          "Authorization" => "Bearer secret-token"
        }
      })

    # Caller tries to override Authorization — vendor header must win.
    payload =
      Jason.encode!(%{
        body: %{},
        extra_headers: %{"Authorization" => "Bearer attacker-token"}
      })

    conn =
      conn(:post, "/notifications/e2e-secure", payload)
      |> put_req_header("content-type", "application/json")
      |> Router.call(@opts)

    assert conn.status == 202
    assert %{success: 1} = Oban.drain_queue(queue: :delivery)

    [headers] = Agent.get(agent, & &1)
    auth = Enum.find_value(headers, fn {k, v} -> k == "authorization" && v end)
    assert auth == "Bearer secret-token"
  end

  test "5xx response is treated as retriable: job stays in retryable state" do
    server =
      open_server(fn conn ->
        assert conn.method == "POST"
        assert conn.request_path == "/unstable"
        Plug.Conn.send_resp(conn, 503, "Service Unavailable")
      end)

    {:ok, _} =
      Vendors.create(%{
        "name" => "e2e-unstable",
        "target_url" => "http://localhost:#{server.port}/unstable",
        "method" => "POST",
        "headers" => %{}
      })

    conn = enqueue("e2e-unstable", %{}, "e2e-5xx-001")
    job_id = conn.resp_body |> Jason.decode!() |> Map.fetch!("id")

    assert %{failure: 1} = Oban.drain_queue(queue: :delivery)

    # After a 5xx failure the job must be scheduled for retry (state=retryable),
    # not discarded. assert_enqueued/1 only matches available/scheduled jobs, so
    # we assert the retryable state directly against the jobs table.
    job = RcNotification.Repo.get!(Oban.Job, job_id)
    assert job.state == "retryable"
    assert job.attempt == 1
    assert job.max_attempts == 10
    assert RcNotification.HTTPTestServer.calls(server.state) == 1
  end

  test "transient failure recovers: job succeeds on a later attempt (retry works)" do
    agent = start_supervised!({Agent, fn -> 0 end})

    # First delivery attempt fails with 503, second attempt succeeds with 200.
    server =
      open_server(fn conn ->
        assert conn.method == "POST"
        assert conn.request_path == "/flaky"
        n = Agent.get_and_update(agent, fn c -> {c + 1, c + 1} end)

        if n == 1 do
          Plug.Conn.send_resp(conn, 503, "Service Unavailable")
        else
          Plug.Conn.send_resp(conn, 200, "ok")
        end
      end)

    {:ok, _} =
      Vendors.create(%{
        "name" => "e2e-flaky",
        "target_url" => "http://localhost:#{server.port}/flaky",
        "method" => "POST",
        "headers" => %{}
      })

    conn = enqueue("e2e-flaky", %{}, "e2e-retry-001")
    job_id = conn.resp_body |> Jason.decode!() |> Map.fetch!("id")

    # Attempt 1 fails → job becomes retryable.
    assert %{failure: 1} = Oban.drain_queue(queue: :delivery)

    # Draining with :with_scheduled promotes the retryable job and runs it again;
    # attempt 2 hits the 200 branch and succeeds.
    assert %{success: 1} = Oban.drain_queue(queue: :delivery, with_scheduled: true)

    job = RcNotification.Repo.get!(Oban.Job, job_id)
    assert job.state == "completed"
    assert job.attempt == 2
    assert Agent.get(agent, & &1) == 2
  end

  test "permanent 4xx response discards job immediately" do
    server =
      open_server(fn conn ->
        assert conn.method == "POST"
        assert conn.request_path == "/gone"
        Plug.Conn.send_resp(conn, 404, "Not Found")
      end)

    {:ok, _} =
      Vendors.create(%{
        "name" => "e2e-gone",
        "target_url" => "http://localhost:#{server.port}/gone",
        "method" => "POST",
        "headers" => %{}
      })

    enqueue("e2e-gone", %{}, "e2e-404-001")

    assert %{discard: 1} = Oban.drain_queue(queue: :delivery)

    # Job is discarded, not retryable
    refute_enqueued(worker: RcNotification.Workers.DeliveryWorker)
    assert RcNotification.HTTPTestServer.calls(server.state) == 1
  end

  test "duplicate idempotency key does not enqueue a second job" do
    # Target should only be called once even if we submit twice
    server =
      open_server(fn conn ->
        assert conn.method == "POST"
        assert conn.request_path == "/idem"
        Plug.Conn.send_resp(conn, 200, "ok")
      end)

    {:ok, _} =
      Vendors.create(%{
        "name" => "e2e-idem",
        "target_url" => "http://localhost:#{server.port}/idem",
        "method" => "POST",
        "headers" => %{}
      })

    conn1 = enqueue("e2e-idem", %{}, "idem-shared-key")
    conn2 = enqueue("e2e-idem", %{}, "idem-shared-key")

    assert conn1.status == 202
    assert conn2.status == 202

    r1 = Jason.decode!(conn1.resp_body)
    r2 = Jason.decode!(conn2.resp_body)
    assert r1["id"] == r2["id"]

    # Only one job exists; draining delivers exactly once
    assert %{success: 1} = Oban.drain_queue(queue: :delivery)
    assert RcNotification.HTTPTestServer.calls(server.state) == 1
  end
end
