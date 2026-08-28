defmodule RcNotification.Router do
  use Plug.Router

  alias RcNotification.{Template, Vendors, Workers.DeliveryWorker}

  plug(:match)

  plug(Plug.Parsers,
    parsers: [:json],
    json_decoder: Jason
  )

  plug(:dispatch)

  get "/healthz" do
    send_resp(conn, 200, "")
  end

  # --- Vendor config management ---

  post "/vendors" do
    case Vendors.create(conn.body_params) do
      {:ok, v} -> json(conn, 201, vendor_json(v))
      {:error, cs} -> json(conn, 400, %{error: changeset_errors(cs)})
    end
  end

  get "/vendors" do
    json(conn, 200, Enum.map(Vendors.list(), &vendor_json/1))
  end

  get "/vendors/:name" do
    case Vendors.get(conn.params["name"]) do
      {:ok, v} -> json(conn, 200, vendor_json(v))
      {:error, :not_found} -> json(conn, 404, %{error: "vendor not found"})
    end
  end

  put "/vendors/:name" do
    case Vendors.update(conn.params["name"], conn.body_params) do
      {:ok, _} -> send_resp(conn, 204, "")
      {:error, :not_found} -> json(conn, 404, %{error: "vendor not found"})
      {:error, cs} -> json(conn, 400, %{error: changeset_errors(cs)})
    end
  end

  delete "/vendors/:name" do
    case Vendors.delete(conn.params["name"]) do
      {:ok, _} -> send_resp(conn, 204, "")
      {:error, :not_found} -> json(conn, 404, %{error: "vendor not found"})
    end
  end

  # --- Notification delivery ---

  post "/notifications/:vendor_name" do
    idempotency_key =
      case get_req_header(conn, "idempotency-key") do
        [key | _] -> key
        [] -> generate_key()
      end

    with {:ok, vendor} <- Vendors.get(conn.params["vendor_name"]),
         {:ok, rendered_body} <- render_body(vendor.body_tpl, conn.body_params["body"]),
         merged_headers = merge_headers(conn.body_params["extra_headers"], vendor.headers),
         args = %{
           idempotency_key: idempotency_key,
           vendor_name: vendor.name,
           target_url: vendor.target_url,
           method: vendor.method,
           headers: merged_headers,
           body: rendered_body
         },
         {:ok, job} <- Oban.insert(DeliveryWorker.new(args)) do
      json(conn, 202, %{id: job.id, idempotency_key: idempotency_key})
    else
      {:error, :not_found} ->
        json(conn, 404, %{error: "vendor not found"})

      {:error, {:template_error, msg}} ->
        json(conn, 422, %{error: "body_tpl render failed: #{msg}"})

      {:error, cs} ->
        json(conn, 400, %{error: changeset_errors(cs)})
    end
  end

  match _ do
    json(conn, 404, %{error: "not found"})
  end

  # --- Helpers ---

  defp json(conn, status, data) do
    conn
    |> put_resp_content_type("application/json")
    |> send_resp(status, Jason.encode!(data))
  end

  defp vendor_json(v) do
    %{
      name: v.name,
      target_url: v.target_url,
      method: v.method,
      headers: v.headers,
      body_tpl: v.body_tpl
    }
  end

  defp changeset_errors(%Ecto.Changeset{} = cs) do
    cs
    |> Ecto.Changeset.traverse_errors(fn {msg, opts} ->
      Enum.reduce(opts, msg, fn {key, val}, acc ->
        String.replace(acc, "%{#{key}}", to_string(val))
      end)
    end)
    |> Enum.map_join("; ", fn {field, msgs} -> "#{field} #{Enum.join(msgs, ", ")}" end)
  end

  defp generate_key, do: :crypto.strong_rand_bytes(16) |> Base.encode16(case: :lower)

  # Merge: extra_headers as base, vendor headers win (auth credentials cannot be overridden).
  defp merge_headers(nil, vendor_headers), do: vendor_headers
  defp merge_headers(extra, vendor_headers), do: Map.merge(extra, vendor_headers)

  # No template: JSON-encode the body map and forward as-is.
  defp render_body(tpl, body) when tpl in [nil, ""] do
    encoded = if is_map(body), do: Jason.encode!(body), else: ""
    {:ok, encoded}
  end

  # Restricted placeholder syntax: <%= @user_id %>, <%= @event %>, etc.
  # The template is never evaluated as Elixir code.
  defp render_body(tpl, body) do
    case Template.render(tpl, body) do
      {:ok, rendered} -> {:ok, rendered}
      {:error, message} -> {:error, {:template_error, message}}
    end
  end
end
