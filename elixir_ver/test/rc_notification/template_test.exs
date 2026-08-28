defmodule RcNotification.TemplateTest do
  use ExUnit.Case, async: true

  alias RcNotification.Template

  test "renders only named placeholders without creating atoms" do
    template = ~s({"id":"<%= @user_id %>","count":<%= @count %>})

    assert {:ok, ~s({"id":"u42","count":3})} =
             Template.render(template, %{"user_id" => "u42", "count" => 3})
  end

  test "rejects arbitrary Elixir expressions" do
    assert {:error, _} = Template.validate("<%= System.get_env(\"DATABASE_URL\") %>")
  end

  test "requires an object body" do
    assert {:error, _} = Template.render("<%= @id %>", id: 1)
  end
end
