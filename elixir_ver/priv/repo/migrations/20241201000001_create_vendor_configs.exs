defmodule RcNotification.Repo.Migrations.CreateVendorConfigs do
  use Ecto.Migration

  def change do
    create table(:vendor_configs, primary_key: false) do
      add :name, :string, primary_key: true
      add :target_url, :text, null: false
      add :method, :string, size: 10, null: false, default: "POST"
      add :headers, :map, null: false, default: %{}
      add :body_tpl, :text, null: false, default: ""

      timestamps()
    end
  end
end
