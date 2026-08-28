defmodule RcNotification.VendorsTest do
  use RcNotification.DataCase

  alias RcNotification.Vendors

  describe "create/1" do
    test "creates a vendor with required fields" do
      assert {:ok, v} = Vendors.create(%{"name" => "crm", "target_url" => "https://api.crm.com"})
      assert v.name == "crm"
      assert v.method == "POST"
      assert v.headers == %{}
      assert v.body_tpl == ""
    end

    test "creates a vendor with all fields" do
      assert {:ok, v} =
               Vendors.create(%{
                 "name" => "ads",
                 "target_url" => "https://ads.example.com/hook",
                 "method" => "POST",
                 "headers" => %{"Authorization" => "Bearer secret"},
                 "body_tpl" => ~s({"ad_id":"<%= @user_id %>"})
               })

      assert v.headers == %{"Authorization" => "Bearer secret"}
    end

    test "rejects missing name" do
      assert {:error, cs} = Vendors.create(%{"target_url" => "https://example.com"})
      assert "can't be blank" in errors_on(cs).name
    end

    test "rejects missing target_url" do
      assert {:error, cs} = Vendors.create(%{"name" => "x"})
      assert "can't be blank" in errors_on(cs).target_url
    end

    test "rejects invalid method" do
      assert {:error, cs} =
               Vendors.create(%{
                 "name" => "x",
                 "target_url" => "https://x.com",
                 "method" => "CONNECT"
               })

      assert errors_on(cs).method != []
    end

    test "rejects executable template expressions" do
      assert {:error, cs} =
               Vendors.create(%{
                 "name" => "x",
                 "target_url" => "https://x.com",
                 "body_tpl" => "<%= System.get_env(\"DATABASE_URL\") %>"
               })

      assert errors_on(cs).body_tpl != []
    end
  end

  describe "get/1" do
    test "returns vendor when it exists" do
      {:ok, _} = Vendors.create(%{"name" => "ads", "target_url" => "https://ads.com"})
      assert {:ok, v} = Vendors.get("ads")
      assert v.name == "ads"
    end

    test "returns not_found for unknown vendor" do
      assert {:error, :not_found} = Vendors.get("nonexistent")
    end
  end

  describe "list/0" do
    test "returns all vendors" do
      {:ok, _} = Vendors.create(%{"name" => "v1", "target_url" => "https://v1.com"})
      {:ok, _} = Vendors.create(%{"name" => "v2", "target_url" => "https://v2.com"})
      vendors = Vendors.list()
      names = Enum.map(vendors, & &1.name)
      assert "v1" in names
      assert "v2" in names
    end
  end

  describe "update/2" do
    test "updates vendor fields" do
      {:ok, _} = Vendors.create(%{"name" => "crm", "target_url" => "https://old.com"})
      assert {:ok, v} = Vendors.update("crm", %{"target_url" => "https://new.com"})
      assert v.target_url == "https://new.com"
    end

    test "returns not_found for unknown vendor" do
      assert {:error, :not_found} = Vendors.update("ghost", %{"target_url" => "https://x.com"})
    end
  end

  describe "delete/1" do
    test "removes the vendor" do
      {:ok, _} = Vendors.create(%{"name" => "temp", "target_url" => "https://temp.com"})
      assert {:ok, _} = Vendors.delete("temp")
      assert {:error, :not_found} = Vendors.get("temp")
    end

    test "returns not_found for unknown vendor" do
      assert {:error, :not_found} = Vendors.delete("ghost")
    end
  end
end
