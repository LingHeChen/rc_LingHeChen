defmodule RcNotification.Vendors do
  @moduledoc """
  CRUD context for vendor configurations (target URL, method, headers, body
  template). Business systems reference a vendor by name; this module is the
  single source of truth for how each vendor's outbound request is shaped.
  """
  alias RcNotification.{Repo, Schemas.VendorConfig}

  def list, do: Repo.all(VendorConfig)

  def get(name) do
    case Repo.get(VendorConfig, name) do
      nil -> {:error, :not_found}
      config -> {:ok, config}
    end
  end

  def create(attrs) do
    %VendorConfig{}
    |> VendorConfig.changeset(attrs)
    |> Repo.insert()
  end

  def update(name, attrs) do
    with {:ok, config} <- get(name) do
      config
      |> VendorConfig.update_changeset(attrs)
      |> Repo.update()
    end
  end

  def delete(name) do
    with {:ok, config} <- get(name) do
      Repo.delete(config)
    end
  end
end
