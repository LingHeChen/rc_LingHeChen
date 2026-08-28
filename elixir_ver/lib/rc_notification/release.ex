defmodule RcNotification.Release do
  @moduledoc """
  Release-time tasks that run without Mix available (in a built release).
  `migrate/0` is invoked from the release binary before the app starts, e.g.
  `bin/rc_notification eval 'RcNotification.Release.migrate()'`.
  """
  @app :rc_notification

  def migrate do
    Application.load(@app)

    for repo <- Application.fetch_env!(@app, :ecto_repos) do
      {:ok, _, _} = Ecto.Migrator.with_repo(repo, &Ecto.Migrator.run(&1, :up, all: true))
    end
  end
end
