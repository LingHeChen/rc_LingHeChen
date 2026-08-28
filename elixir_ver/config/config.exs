import Config

config :rc_notification, ecto_repos: [RcNotification.Repo]

config :rc_notification, RcNotification.Repo,
  url: "postgres://notify:notify@localhost:5432/notify",
  pool_size: 10

config :rc_notification, Oban,
  repo: RcNotification.Repo,
  plugins: [
    {Oban.Plugins.Pruner, max_age: 7 * 24 * 60 * 60}
  ],
  stage_interval: 1_000,
  queues: [delivery: 5]

config :rc_notification, :http_port, 8080
config :rc_notification, :http_client, RcNotification.HTTPClient.Req

import_config "#{config_env()}.exs"
