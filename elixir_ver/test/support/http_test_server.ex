defmodule RcNotification.HTTPTestServer do
  @moduledoc false

  @behaviour Plug

  def init(state), do: state

  def call(conn, state) do
    handler =
      Agent.get_and_update(state, fn %{calls: calls, handler: handler} = current ->
        {handler, %{current | calls: calls + 1}}
      end)

    handler.(conn)
  end

  def calls(state), do: Agent.get(state, & &1.calls)
end
