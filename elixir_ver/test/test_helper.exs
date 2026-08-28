# Integration/e2e tests (tagged `@moduletag :integration`) are excluded by
# default so `mix test` is a fast unit suite. Run them explicitly with:
#   mix test --include integration
ExUnit.start(exclude: [:integration])

Ecto.Adapters.SQL.Sandbox.mode(RcNotification.Repo, :manual)

Mox.defmock(RcNotification.HTTPClient.Mock, for: RcNotification.HTTPClient)
