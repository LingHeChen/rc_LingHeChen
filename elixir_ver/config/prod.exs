import Config

# Compile-time production config. Runtime values (DATABASE_URL, PORT) are read
# in config/runtime.exs so the same release binary works across environments.
config :logger, level: :info
