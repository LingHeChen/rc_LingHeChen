import Config

config :rc_notification, RcNotification.Repo,
  url: System.get_env("DATABASE_URL", "postgres://notify:notify@localhost:5432/notify_test"),
  pool: Ecto.Adapters.SQL.Sandbox,
  pool_size: 10

# Oban: manual mode — jobs are inserted but not executed automatically.
# Use assert_enqueued/1 to verify, or Oban.drain_queue/1 to run inline.
config :rc_notification, Oban, testing: :manual

config :rc_notification, :http_client, RcNotification.HTTPClient.Mock
