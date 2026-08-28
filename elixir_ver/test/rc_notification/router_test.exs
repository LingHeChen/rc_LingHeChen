defmodule RcNotification.RouterTest do
  use RcNotification.DataCase
  use Oban.Testing, repo: RcNotification.Repo

  import Plug.Test
  import Plug.Conn

  alias RcNotification.{Router, Vendors}

  @opts Router.init([])

  test "GET /healthz returns 200" do
    conn = conn(:get, "/healthz") |> Router.call(@opts)
    assert conn.status == 200
  end

  test "GET /nonexistent returns 404" do
    conn = conn(:get, "/nonexistent") |> Router.call(@opts)
    assert conn.status == 404
  end

  describe "vendor endpoints" do
    test "POST /vendors creates a vendor" do
      body = Jason.encode!(%{name: "crm", target_url: "https://crm.example.com"})

      conn =
        conn(:post, "/vendors", body)
        |> put_req_header("content-type", "application/json")
        |> Router.call(@opts)

      assert conn.status == 201
      response = Jason.decode!(conn.resp_body)
      assert response["name"] == "crm"
    end

    test "POST /vendors returns 400 for invalid body" do
      body = Jason.encode!(%{name: "x"})

      conn =
        conn(:post, "/vendors", body)
        |> put_req_header("content-type", "application/json")
        |> Router.call(@opts)

      assert conn.status == 400
    end

    test "GET /vendors returns list" do
      {:ok, _} = Vendors.create(%{"name" => "v1", "target_url" => "https://v1.com"})

      conn = conn(:get, "/vendors") |> Router.call(@opts)
      assert conn.status == 200
      response = Jason.decode!(conn.resp_body)
      assert is_list(response)
      assert Enum.any?(response, &(&1["name"] == "v1"))
    end

    test "GET /vendors/:name returns vendor" do
      {:ok, _} = Vendors.create(%{"name" => "ads", "target_url" => "https://ads.com"})

      conn = conn(:get, "/vendors/ads") |> Router.call(@opts)
      assert conn.status == 200
      assert Jason.decode!(conn.resp_body)["name"] == "ads"
    end

    test "GET /vendors/:name returns 404 for unknown vendor" do
      conn = conn(:get, "/vendors/ghost") |> Router.call(@opts)
      assert conn.status == 404
    end

    test "PUT /vendors/:name updates vendor" do
      {:ok, _} = Vendors.create(%{"name" => "crm", "target_url" => "https://old.com"})

      body = Jason.encode!(%{target_url: "https://new.com"})

      conn =
        conn(:put, "/vendors/crm", body)
        |> put_req_header("content-type", "application/json")
        |> Router.call(@opts)

      assert conn.status == 204
    end

    test "DELETE /vendors/:name removes vendor" do
      {:ok, _} = Vendors.create(%{"name" => "temp", "target_url" => "https://temp.com"})

      conn = conn(:delete, "/vendors/temp") |> Router.call(@opts)
      assert conn.status == 204
    end
  end

  describe "notification endpoints" do
    setup do
      {:ok, _} =
        Vendors.create(%{
          "name" => "test-vendor",
          "target_url" => "https://example.com/hook",
          "method" => "POST",
          "headers" => %{"Content-Type" => "application/json"}
        })

      :ok
    end

    test "POST /notifications/:vendor_name returns 202 and enqueues a job" do
      body = Jason.encode!(%{body: %{user_id: "u42", event: "user.registered"}})

      conn =
        conn(:post, "/notifications/test-vendor", body)
        |> put_req_header("content-type", "application/json")
        |> put_req_header("idempotency-key", "idem-001")
        |> Router.call(@opts)

      assert conn.status == 202
      response = Jason.decode!(conn.resp_body)
      assert response["idempotency_key"] == "idem-001"
      assert is_integer(response["id"])

      assert_enqueued(worker: RcNotification.Workers.DeliveryWorker)
    end

    test "POST /notifications/:vendor_name generates idempotency key when header absent" do
      body = Jason.encode!(%{body: %{}})

      conn =
        conn(:post, "/notifications/test-vendor", body)
        |> put_req_header("content-type", "application/json")
        |> Router.call(@opts)

      assert conn.status == 202
      response = Jason.decode!(conn.resp_body)
      assert String.length(response["idempotency_key"]) == 32
    end

    test "POST /notifications/:vendor_name returns 404 for unknown vendor" do
      body = Jason.encode!(%{body: %{}})

      conn =
        conn(:post, "/notifications/nonexistent", body)
        |> put_req_header("content-type", "application/json")
        |> Router.call(@opts)

      assert conn.status == 404
    end

    test "duplicate idempotency key returns the original job id" do
      body = Jason.encode!(%{body: %{user_id: "u1"}})

      make_request = fn ->
        conn(:post, "/notifications/test-vendor", body)
        |> put_req_header("content-type", "application/json")
        |> put_req_header("idempotency-key", "dedup-key")
        |> Router.call(@opts)
      end

      conn1 = make_request.()
      conn2 = make_request.()

      r1 = Jason.decode!(conn1.resp_body)
      r2 = Jason.decode!(conn2.resp_body)

      assert conn1.status == 202
      assert conn2.status == 202
      assert r1["id"] == r2["id"]
    end

    test "the same idempotency key is independent across vendors" do
      {:ok, _} =
        Vendors.create(%{
          "name" => "second-vendor",
          "target_url" => "https://example.com/second"
        })

      body = Jason.encode!(%{body: %{user_id: "u1"}})

      request = fn vendor ->
        conn(:post, "/notifications/#{vendor}", body)
        |> put_req_header("content-type", "application/json")
        |> put_req_header("idempotency-key", "shared-business-key")
        |> Router.call(@opts)
      end

      first = request.("test-vendor") |> Map.fetch!(:resp_body) |> Jason.decode!()
      second = request.("second-vendor") |> Map.fetch!(:resp_body) |> Jason.decode!()

      refute first["id"] == second["id"]
    end
  end
end
