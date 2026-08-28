defmodule RcNotification.Application do
  @moduledoc """
  OTP application entrypoint. Boots the supervision tree: Repo, Oban (queues +
  plugins), and the Bandit HTTP server. Restart strategy is `:one_for_one`.
  """
  use Application

  @impl true
  def start(_type, _args) do
    port = Application.get_env(:rc_notification, :http_port, 8080)

    children = [
      RcNotification.Repo,
      {Oban, Application.fetch_env!(:rc_notification, Oban)},
      {Bandit, plug: RcNotification.Router, port: port}
    ]

    opts = [strategy: :one_for_one, name: RcNotification.Supervisor]
    Supervisor.start_link(children, opts)
  end
end
