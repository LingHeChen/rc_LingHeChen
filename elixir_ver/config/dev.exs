import Config

config :rc_notification, RcNotification.Repo,
  url: System.get_env("DATABASE_URL", "postgres://notify:notify@localhost:5432/notify_dev")
