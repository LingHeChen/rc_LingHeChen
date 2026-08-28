defmodule RcNotification.Schemas.VendorConfig do
  use Ecto.Schema
  import Ecto.Changeset

  alias RcNotification.Template

  @valid_methods ~w(GET POST PUT PATCH DELETE)

  @primary_key {:name, :string, []}
  schema "vendor_configs" do
    field(:target_url, :string)
    field(:method, :string, default: "POST")
    field(:headers, :map, default: %{})
    field(:body_tpl, :string, default: "")

    timestamps()
  end

  def changeset(config, attrs) do
    config
    |> cast(attrs, [:name, :target_url, :method, :headers, :body_tpl])
    |> validate_required([:name, :target_url])
    |> validate_length(:name, min: 1, max: 255)
    |> validate_inclusion(:method, @valid_methods)
    |> validate_template(:body_tpl)
  end

  def update_changeset(config, attrs) do
    config
    |> cast(attrs, [:target_url, :method, :headers, :body_tpl])
    |> validate_required([:target_url])
    |> validate_inclusion(:method, @valid_methods)
    |> validate_template(:body_tpl)
  end

  defp validate_template(changeset, field) do
    case fetch_change(changeset, field) do
      {:ok, ""} ->
        changeset

      {:ok, tpl} ->
        case Template.validate(tpl) do
          :ok -> changeset
          {:error, message} -> add_error(changeset, field, "invalid template: #{message}")
        end

      :error ->
        changeset
    end
  end
end
