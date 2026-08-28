defmodule RcNotification.Workers.DeliveryWorkerTest do
  use ExUnit.Case, async: true

  import Mox

  alias RcNotification.Workers.DeliveryWorker

  setup :verify_on_exit!

  defp make_job(args \\ %{}) do
    base = %{
      "target_url" => "https://example.com/webhook",
      "method" => "POST",
      "headers" => %{"Content-Type" => "application/json"},
      "body" => ~s({"event":"test"}),
      "idempotency_key" => "key-123"
    }

    %Oban.Job{args: Map.merge(base, args), attempt: 1}
  end

  test "returns :ok on 2xx response" do
    expect(RcNotification.HTTPClient.Mock, :request, fn _, _, _, _ -> {:ok, 200} end)
    assert :ok = DeliveryWorker.perform(make_job())
  end

  test "returns :ok on 201" do
    expect(RcNotification.HTTPClient.Mock, :request, fn _, _, _, _ -> {:ok, 201} end)
    assert :ok = DeliveryWorker.perform(make_job())
  end

  test "returns retriable error on 5xx" do
    expect(RcNotification.HTTPClient.Mock, :request, fn _, _, _, _ -> {:ok, 500} end)
    assert {:error, msg} = DeliveryWorker.perform(make_job())
    assert msg =~ "500"
  end

  test "returns retriable error on 503" do
    expect(RcNotification.HTTPClient.Mock, :request, fn _, _, _, _ -> {:ok, 503} end)
    assert {:error, _} = DeliveryWorker.perform(make_job())
  end

  test "discards on permanent 400" do
    expect(RcNotification.HTTPClient.Mock, :request, fn _, _, _, _ -> {:ok, 400} end)
    assert {:discard, msg} = DeliveryWorker.perform(make_job())
    assert msg =~ "400"
  end

  test "discards on 401" do
    expect(RcNotification.HTTPClient.Mock, :request, fn _, _, _, _ -> {:ok, 401} end)
    assert {:discard, _} = DeliveryWorker.perform(make_job())
  end

  test "retries on 429 (rate limit)" do
    expect(RcNotification.HTTPClient.Mock, :request, fn _, _, _, _ -> {:ok, 429} end)
    assert {:error, msg} = DeliveryWorker.perform(make_job())
    assert msg =~ "429"
  end

  test "retries on 408 (request timeout)" do
    expect(RcNotification.HTTPClient.Mock, :request, fn _, _, _, _ -> {:ok, 408} end)
    assert {:error, msg} = DeliveryWorker.perform(make_job())
    assert msg =~ "408"
  end

  test "retries on network error" do
    expect(RcNotification.HTTPClient.Mock, :request, fn _, _, _, _ -> {:error, :econnrefused} end)
    assert {:error, msg} = DeliveryWorker.perform(make_job())
    assert msg =~ "network error"
  end

  describe "backoff/1" do
    test "increases exponentially" do
      delays =
        for attempt <- 1..10 do
          DeliveryWorker.backoff(%Oban.Job{attempt: attempt})
        end

      assert Enum.all?(delays, &(&1 > 0))
      # Attempt 1: ~60s, attempt 10: ~24h cap
      assert hd(delays) < 120
      assert List.last(delays) > 3600
    end

    test "caps at 24 hours" do
      # attempt 100 should still be capped at ~24h
      delay = DeliveryWorker.backoff(%Oban.Job{attempt: 100})
      assert delay <= 24 * 60 * 60 * 1.3
    end
  end
end
