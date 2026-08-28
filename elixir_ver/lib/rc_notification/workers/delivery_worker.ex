defmodule RcNotification.Workers.DeliveryWorker do
  @moduledoc """
  Oban worker that performs a single notification delivery. Maps the target's
  HTTP response to Oban semantics: 2xx → `:ok`, 5xx/408/429/network → retry,
  other 4xx → `:discard` (DLQ). Retries use exponential backoff with ±20% jitter.
  """
  use Oban.Worker,
    queue: :delivery,
    max_attempts: 10,
    unique: [
      period: :infinity,
      keys: [:vendor_name, :idempotency_key],
      states: [:available, :scheduled, :executing, :retryable, :completed, :discarded]
    ]

  alias RcNotification.HTTPClient

  @impl Oban.Worker
  def perform(%Oban.Job{
        args: %{
          "target_url" => url,
          "method" => method,
          "headers" => headers,
          "body" => body
        }
      }) do
    case HTTPClient.request(method, url, headers, body) do
      {:ok, status} when status in 200..299 ->
        :ok

      {:ok, status} when status >= 500 ->
        {:error, "server error: HTTP #{status}"}

      {:ok, 408} ->
        {:error, "transient: HTTP 408 Request Timeout"}

      {:ok, 429} ->
        {:error, "transient: HTTP 429 Too Many Requests"}

      {:ok, status} when status >= 400 ->
        {:discard, "permanent client error: HTTP #{status}"}

      {:error, reason} ->
        {:error, "network error: #{inspect(reason)}"}
    end
  end

  # Exponential backoff with ±20% jitter, matching the Go version:
  # 1m → 2m → 4m → ... → 24h cap
  @impl Oban.Worker
  def backoff(%Oban.Job{attempt: attempt}) do
    base_ms = :math.pow(2, attempt - 1) * 60_000
    cap_ms = 24 * 60 * 60 * 1_000
    delay_ms = min(base_ms, cap_ms)
    jitter = delay_ms * 0.4 * (:rand.uniform() - 0.5)
    round((delay_ms + jitter) / 1_000)
  end
end
