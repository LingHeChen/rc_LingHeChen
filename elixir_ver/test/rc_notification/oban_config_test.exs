defmodule RcNotification.ObanConfigTest do
  @moduledoc """
  Regression test for the "boots in prod but the whole test suite is green" class
  of bug.

  Oban forcibly rewrites `plugins: []` and `queues: []` whenever it runs under
  `testing: :manual` or `testing: :inline` (see `Oban.Config.new/1`). config/test.exs
  sets `testing: :manual`, so the normal suite never validates the real plugin/queue
  list. An invalid plugin reference (e.g. a plugin module that was removed in a newer
  Oban version) therefore compiles, passes every test, and only blows up at runtime
  when the supervision tree actually starts.

  This test validates the configured Oban options exactly as the production
  supervision tree would — WITHOUT the test-only `testing:` override — so a broken
  plugin/queue configuration fails here instead of in production.

  `Oban.Config.new/1` only validates options (it loads plugin modules via
  `Code.ensure_loaded?`); it does not touch the database, so this stays a fast,
  DB-free unit test.
  """
  use ExUnit.Case, async: true

  test "the configured Oban options are valid under a real (non-testing) supervisor" do
    prod_opts =
      :rc_notification
      |> Application.fetch_env!(Oban)
      # Strip the test-only override so plugins/queues are validated for real,
      # the way they are when the app boots in dev/prod.
      |> Keyword.delete(:testing)

    assert %Oban.Config{} = Oban.Config.new(prod_opts)
  end

  test "every configured Oban plugin module is loadable" do
    plugins =
      :rc_notification
      |> Application.fetch_env!(Oban)
      |> Keyword.get(:plugins, [])

    for plugin <- plugins do
      module = if is_tuple(plugin), do: elem(plugin, 0), else: plugin

      assert Code.ensure_loaded?(module),
             "configured Oban plugin #{inspect(module)} is not loadable"
    end
  end
end
