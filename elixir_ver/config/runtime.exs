import Config

if url = System.get_env("DATABASE_URL") do
  config :rc_notification, RcNotification.Repo, url: url
end

if port = System.get_env("PORT") do
  config :rc_notification, :http_port, String.to_integer(port)
end
