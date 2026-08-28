# Go vs Elixir：API 通知系统实现对比

两个版本实现完全相同的需求。本文档逐点对比核心设计问题上的差异，说明为什么 Elixir/OTP 在这个场景下是更自然的选择。

---

## 1. 进程隔离与崩溃边界

这是两个版本最根本的差异。

### Go 版本

Worker 使用 goroutine pool。所有 goroutine 共享同一地址空间，没有隔离边界：

```go
// worker.go:54-66
func (w *Worker) Run(ctx context.Context) {
    go w.recoverLoop(ctx)          // 额外的崩溃恢复 goroutine

    var wg sync.WaitGroup
    for i := 0; i < w.concurrency; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            w.runWorker(ctx)        // ← 没有 defer/recover
        }()
    }
    wg.Wait()
}

// worker.go:83-112
func (w *Worker) runWorker(ctx context.Context) {
    for {
        job, _ := w.queue.Dequeue(ctx)
        w.process(ctx, job)        // ← panic 在此无拦截地向上传播
    }
}
```

**当 `process()` 内部发生 panic（例如空指针解引用）：**

```
goroutine #1: delivering job A  ←─ panic!
goroutine #2: delivering job B  ←─ Go runtime: os.Exit(2)
goroutine #3: delivering job C  ←─ 全部终止
goroutine #4: delivering job D  ←─
goroutine #5: delivering job E  ←─
HTTP server                     ←─ 同一进程，一起崩溃
```

整个服务进程终止。DB 里 `status=processing` 的任务需要等到服务重启后，由 `recoverLoop` 在 5 分钟超时后才能重置。

> **可运行验证：**
> ```bash
> cd golang_ver && go run ./demo/panic_demo.go
> ```

### Elixir 版本

每个 Oban job 在独立的 Erlang 进程中执行（~2KB 内存，由 BEAM VM 调度）：

```
[Oban Supervisor]
    ├── [Job A 进程 pid=<0.234.0>]  ←─ raise → 该进程终止
    │                                  Oban 标记为 retryable，计划重试
    ├── [Job B 进程 pid=<0.235.0>]  ←─ 完全不受影响，继续执行
    ├── [Job C 进程 pid=<0.236.0>]  ←─ 完全不受影响
    └── HTTP 服务进程               ←─ 完全不受影响
```

Job A 的进程崩溃是一个孤立事件。Supervisor 收到 `EXIT` 信号后更新 DB 状态，无需任何手动恢复代码。

> **可运行验证（需先 `mix ecto.setup`）：**
> ```bash
> cd elixir_ver && mix demo.crash
> ```

---

## 2. 崩溃恢复：手写 vs 内建

### Go 版本需要手写的 "plumbing"

| 功能 | 代码位置 | 行数 |
|------|----------|------|
| 崩溃检测（polling stuck jobs） | `worker.go:68-81` | 14 行 |
| DB 状态重置 | `queue/postgres/pg.go:111-119` | 9 行 |
| Goroutine pool 管理 | `worker.go:54-66` | 13 行 |
| `SELECT FOR UPDATE SKIP LOCKED` | `queue/postgres/pg.go:58-84` | 27 行 |
| **合计** | | **63 行** |

`recoverLoop` 的局限性：它只能处理"worker 进程整体崩溃后重启"的场景（通过检测 `updated_at` 超时）。对于**进程内 panic**，它完全无效——panic 已经让整个进程退出了，根本等不到 `recoverLoop` 运行。

### Elixir 版本

**0 行**。Oban 的 Supervisor 树负责：
- 检测进程崩溃（OTP `EXIT` 信号，毫秒级）
- 原子更新 job 状态
- 调度重试
- 管理并发度

```elixir
# application.ex — 全部配置在这里，无其他代码
children = [
  RcNotification.Repo,
  {Oban, Application.fetch_env!(:rc_notification, Oban)},  # 一行搞定
  {Bandit, plug: RcNotification.Router, port: port}
]
```

---

## 3. 核心 Worker 代码量对比

实现**完全相同的功能**所需的代码：

| 文件 | 功能 | 行数 |
|------|------|------|
| `golang_ver/internal/worker/worker.go` | worker pool + retry + recovery | **188 行** |
| `golang_ver/internal/queue/postgres/pg.go` | queue 实现（SELECT FOR UPDATE 等） | **119 行** |
| Go 合计 | | **307 行** |
| `elixir_ver/lib/rc_notification/workers/delivery_worker.ex` | deliver + backoff | **58 行** |
| Elixir 合计（Oban 处理其余） | | **58 行** |

Go 版本需要 307 行来实现 Elixir 版本 58 行覆盖的功能。差值（249 行）是**领域无关的基础设施代码**，在 Elixir 版本中由经过生产验证的 Oban 库承担。

---

## 4. 退避逻辑对比

两个版本实现相同的指数退避 + ±20% jitter：

**Go（`worker.go:179-188`，10 行）：**
```go
func nextRetryDelay(attempts int) time.Duration {
    base := float64(time.Minute)
    cap := float64(24 * time.Hour)
    delay := base * math.Pow(2, float64(attempts-1))
    if delay > cap {
        delay = cap
    }
    jitter := (rand.Float64()*0.4 - 0.2) * delay
    return time.Duration(delay + jitter)
}
```

**Elixir（`delivery_worker.ex`，7 行）：**
```elixir
def backoff(%Oban.Job{attempt: attempt}) do
  base_ms = :math.pow(2, attempt - 1) * 60_000
  cap_ms = 24 * 60 * 60 * 1_000
  delay_ms = min(base_ms, cap_ms)
  jitter = delay_ms * 0.4 * (:rand.uniform() - 0.5)
  round((delay_ms + jitter) / 1_000)
end
```

逻辑完全等价。Elixir 版本还可以省略 `backoff/1`——Oban 有内建的默认退避策略，这里只是为了精确匹配 Go 版本的行为而覆盖它。

---

## 5. 幂等性实现对比

**Go 版本（手写 SQL）：**
```sql
INSERT INTO notification_jobs (id, vendor_name, idempotency_key, ...)
VALUES (?, ?, ?, ...)
ON CONFLICT (vendor_name, idempotency_key) DO NOTHING
```
通过数据库唯一约束实现。永久有效（只要记录不被删除）。

**Elixir 版本（Oban unique jobs）：**
```elixir
use Oban.Worker,
  unique: [
    period: :infinity,
    keys: [:vendor_name, :idempotency_key],
    states: [:available, :scheduled, :executing, :retryable, :completed, :discarded]
  ]
```
两版都按 `(vendor_name, idempotency_key)` 去重，避免同一业务事件发送给不同供应商时互相抑制。Oban 覆盖所有 job 状态。注意：`Oban.Plugins.Pruner` 会在 7 天后清理已完成的 job，这意味着 7 天后同一供应商下相同的 idempotency key 可以重新入队。Go 版本无此限制。

**取舍**：Go 版本的永久唯一性更严格；Elixir 版本与"7 天幂等保护"在大多数业务场景下足够，且避免了 notification_jobs 表无限增长。

---

## 6. 可观测性

**Go 版本（现状）：** `slog` 结构化日志，无 metrics。

**Elixir 版本扩展路径：** Oban 内建 Telemetry 事件，接入 Prometheus 只需几行：

```elixir
# 无需修改任何业务代码，Oban 自动发出这些事件：
# [:oban, :job, :start]
# [:oban, :job, :stop]    → 包含 duration、queue、worker、state
# [:oban, :job, :exception]
```

---

## 总结

| 维度 | Go 版本 | Elixir 版本 |
|------|---------|-------------|
| 进程崩溃影响范围 | 整个服务进程 | 只影响当前 job 进程 |
| 崩溃恢复代码量 | 63 行手写 | 0 行（OTP 内建） |
| Worker 总代码量 | 307 行 | 58 行 |
| panic 防护 | 需手动 `defer/recover` | 自动，无需代码 |
| 幂等性 | 按供应商永久约束 | 按供应商 7 天（Oban pruner） |
| 监控接入 | 手动实现 | Telemetry 内建 |

Elixir 的优势不在于性能，而在于 OTP 的**错误隔离模型**与这个问题的结构天然契合：每个通知投递就是一个独立的、可以失败并重试的任务。这正是 Erlang 进程模型的设计目标。
