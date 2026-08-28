defmodule RcNotification.DataCase do
  @moduledoc """
  ExUnit case template for tests that touch the database. Checks out an
  `Ecto.Adapters.SQL.Sandbox` connection per test (shared mode for non-async
  tests so spawned processes see the same transaction) and imports Ecto helpers.
  """
  use ExUnit.CaseTemplate

  alias Ecto.Adapters.SQL.Sandbox

  using do
    quote do
      alias RcNotification.Repo
      import Ecto
      import Ecto.Changeset
      import RcNotification.DataCase
    end
  end

  setup tags do
    :ok = Sandbox.checkout(RcNotification.Repo)

    unless tags[:async] do
      Sandbox.mode(RcNotification.Repo, {:shared, self()})
    end

    :ok
  end

  def errors_on(changeset) do
    Ecto.Changeset.traverse_errors(changeset, fn {msg, opts} ->
      Enum.reduce(opts, msg, fn {key, val}, acc ->
        String.replace(acc, "%{#{key}}", to_string(val))
      end)
    end)
  end
end
