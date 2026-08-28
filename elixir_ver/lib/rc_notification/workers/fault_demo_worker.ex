defmodule RcNotification.Workers.FaultDemoWorker do
  @moduledoc """
  Used exclusively in demos to inject faults and demonstrate OTP process isolation.
  Each job runs in its own Erlang process (~2KB memory). A crash here never affects
  other running jobs or the HTTP server.
  """
  use Oban.Worker, queue: :delivery, max_attempts: 3

  @impl Oban.Worker
  def perform(%Oban.Job{args: %{"fault" => "raise", "id" => id}}) do
    raise RuntimeError, "job #{id} intentionally crashed (simulating a bug)"
  end

  def perform(%Oban.Job{args: %{"fault" => "exit", "id" => id}}) do
    # Process.exit with :kill cannot be caught by try/rescue — it's a hard kill.
    # Oban's supervisor still catches it and marks the job retryable.
    IO.puts("job #{id}: sending exit signal to self")
    Process.exit(self(), :kill)
  end

  def perform(%Oban.Job{args: %{"fault" => "none", "id" => id}}) do
    # Simulate work
    Process.sleep(50)
    IO.puts("job #{id}: completed successfully (pid=#{inspect(self())})")
    :ok
  end
end
