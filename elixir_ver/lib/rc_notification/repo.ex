defmodule RcNotification.Repo do
  use Ecto.Repo,
    otp_app: :rc_notification,
    adapter: Ecto.Adapters.Postgres
end
