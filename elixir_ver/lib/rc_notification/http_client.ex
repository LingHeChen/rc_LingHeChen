defmodule RcNotification.HTTPClient do
  @moduledoc """
  Behaviour + dispatcher for outbound HTTP delivery. The concrete implementation
  is resolved at runtime from the `:http_client` app env, which lets tests swap in
  a Mox mock while dev/prod use the real `Req`-based client.
  """
  @callback request(method :: String.t(), url :: String.t(), headers :: map(), body :: binary()) ::
              {:ok, non_neg_integer()} | {:error, term()}

  def request(method, url, headers, body) do
    impl = Application.get_env(:rc_notification, :http_client, __MODULE__.Req)
    impl.request(method, url, headers, body)
  end
end

defmodule RcNotification.HTTPClient.Req do
  @moduledoc """
  Production `RcNotification.HTTPClient` implementation backed by the `Req` library.
  Returns only the HTTP status code (or an error) — the delivery worker decides
  retry/discard semantics from the status.
  """
  @behaviour RcNotification.HTTPClient

  @impl true
  def request(method, url, headers, body) do
    with {:ok, method_atom} <- to_method_atom(method) do
      case Req.request(method: method_atom, url: url, headers: headers, body: body) do
        {:ok, %Req.Response{status: status}} -> {:ok, status}
        {:error, reason} -> {:error, reason}
      end
    end
  end

  defp to_method_atom("GET"), do: {:ok, :get}
  defp to_method_atom("POST"), do: {:ok, :post}
  defp to_method_atom("PUT"), do: {:ok, :put}
  defp to_method_atom("PATCH"), do: {:ok, :patch}
  defp to_method_atom("DELETE"), do: {:ok, :delete}
  defp to_method_atom(method), do: {:error, {:unsupported_method, method}}
end
