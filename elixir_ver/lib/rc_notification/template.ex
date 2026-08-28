defmodule RcNotification.Template do
  @moduledoc """
  Restricted placeholder renderer for vendor request bodies.

  It intentionally supports only `<%= @field %>` placeholders. Unlike EEx, the
  template is never compiled or evaluated as Elixir code.
  """

  @placeholder ~r/<%=\s*@([A-Za-z_][A-Za-z0-9_]*)\s*%>/

  def validate(template) when is_binary(template) do
    remainder = Regex.replace(@placeholder, template, "")

    if String.contains?(remainder, ["<%", "%>"]) do
      {:error, "only <%= @field %> placeholders are allowed"}
    else
      :ok
    end
  end

  def render(template, body) when is_binary(template) and is_map(body) do
    with :ok <- validate(template) do
      Regex.replace(@placeholder, template, fn _match, key ->
        body
        |> Map.get(key)
        |> encode_value()
      end)
      |> then(&{:ok, &1})
    end
  end

  def render(_template, _body), do: {:error, "body must be a JSON object when body_tpl is set"}

  defp encode_value(nil), do: ""
  defp encode_value(value) when is_binary(value), do: value
  defp encode_value(value) when is_number(value), do: to_string(value)
  defp encode_value(value) when is_boolean(value), do: to_string(value)
  defp encode_value(value), do: Jason.encode!(value)
end
