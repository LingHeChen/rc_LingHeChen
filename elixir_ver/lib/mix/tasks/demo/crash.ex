defmodule Mix.Tasks.Demo.Crash do
  @shortdoc "Demonstrate OTP process isolation: 1 crashing job never affects others"

  @moduledoc """
  Inserts 10 jobs: jobs 1-4 are normal, job 5 intentionally raises, jobs 6-10 are normal.
  In Elixir: job 5's process crashes, Oban marks it retryable, jobs 1-4 and 6-10 complete.
  In Go:     a panic in a goroutine without recover() kills ALL goroutines + the HTTP server.

  Run with:
      mix demo.crash
  """

  use Mix.Task

  alias RcNotification.Workers.FaultDemoWorker

  @impl Mix.Task
  def run(_args) do
    Mix.Task.run("app.start")

    IO.puts("""

    ╔══════════════════════════════════════════════════════════╗
    ║   OTP Process Isolation Demo                             ║
    ║   Inserting 10 jobs. Job #5 will intentionally crash.    ║
    ╚══════════════════════════════════════════════════════════╝
    """)

    jobs =
      for i <- 1..10 do
        fault = if i == 5, do: "raise", else: "none"
        label = if i == 5, do: " ← WILL CRASH", else: ""
        IO.puts("Enqueueing job #{i} (fault=#{fault})#{label}")
        {:ok, job} = Oban.insert(FaultDemoWorker.new(%{"id" => i, "fault" => fault}))
        job
      end

    IO.puts("\nDraining the :delivery queue...\n")

    # Drain executes all pending jobs synchronously in the test/dev process
    result = Oban.drain_queue(queue: :delivery, with_scheduled: true)

    IO.puts("""

    ══════════════════════════════════════════════════════════
    Results: #{inspect(result)}
    ══════════════════════════════════════════════════════════

    What happened:
      • Job #5 raised a RuntimeError → its Erlang process crashed
      • Oban supervisor caught the exit signal
      • Job #5 marked as 'retryable' (attempt 1 of 3)
      • Jobs 1-4 and 6-10: completed ✓ — unaffected by job #5's crash

    In the Go version:
      • Worker goroutines share no crash isolation
      • A panic in w.process() without defer/recover propagates upward
      • The goroutine dies; with 5 goroutines one slot is permanently lost
      • A panic at the top level kills the entire process (HTTP server included)
      • The Go version has a recoverLoop() for DB-detected stuck jobs,
        but that does NOT protect against in-process panics.
    """)

    # Show job states from the DB
    jobs_with_state =
      Enum.map(jobs, fn job ->
        db_job = Oban.Job |> RcNotification.Repo.get(job.id)
        "  job #{db_job.args["id"]}: state=#{db_job.state}, attempt=#{db_job.attempt}"
      end)

    IO.puts("Final job states in DB:\n" <> Enum.join(jobs_with_state, "\n"))
  end
end
