# rc_LingHeChen — API 通知投递服务

> **作业要求：** 设计并实现一个内部服务，接收业务系统提交的外部 HTTP 通知请求，并尽可能可靠地投递到目标地址。

本仓库包含**同一需求的两个实现版本**：

| 版本 | 目录 | 技术栈 |
|------|------|--------|
| 第一版 | [`golang_ver/`](./golang_ver/) | Go · Gin · PostgreSQL queue |
| 第二版 | [`elixir_ver/`](./elixir_ver/) | Elixir · Plug/Bandit · Oban · PostgreSQL |

**文档导航：**
[AI 使用说明](./AI_USAGE.md) ·
[Go vs Elixir 逐点对比](./COMPARISON.md) ·
[Go 版设计文档](./golang_ver/README.md) ·
[Elixir 版设计文档](./elixir_ver/README.md)

---

## 为什么做两个版本

### 先有 Go 版本

Go 是完成这道题的自然选择：生态成熟、部署简单、团队熟悉。Go 版本在 README 中明确说明了决策过程：当时在 Go / Rust / Elixir 之间做了权衡，选择 Go 的理由是"投入产出比最好，维护成本最低"，同时也承认"Elixir 的 OTP 模型在设计上更契合"。

这句话留下了一个待验证的判断。

### 然后验证这个判断

写完 Go 版本后，用 Elixir 重写了一遍，目的是**通过可运行的代码来验证"更契合"到底体现在哪里**，而不只是停留在文档层面的断言。

结论是：这个系统的核心问题结构——**每个通知是一个独立的、可以失败并重试的任务**——和 Erlang 进程模型的设计目标完全吻合。具体体现在三个可量化的差异：

**1. 进程崩溃的边界不同**

Go 的 goroutine 没有隔离边界。`deliver()` 里的 panic 如果没有 `recover()`，会通过 `runWorker → Run` 向上传播，Go runtime 打印 stack trace 后 `os.Exit(2)`——整个进程终止，HTTP server 一起崩溃。

Elixir 里每个 Oban job 在独立的 Erlang 进程中运行。job 进程崩溃是一个孤立事件，Supervisor 收到 `EXIT` 信号后更新 DB 状态并安排重试，其他 job 和 HTTP server 完全不受影响。

**2. 崩溃恢复的代码量不同**

Go 版本需要手写 `recoverLoop()`（轮询 DB 检测超时任务）、`RecoverStuck()`（重置 stuck 状态）、`sync.WaitGroup`（管理 goroutine pool），合计约 **63 行基础设施代码**。而且 `recoverLoop` 只能处理"进程重启后的恢复"，对进程内 panic 无效。

Elixir 版本：**0 行**。OTP Supervisor 树自动处理所有这些情况。

**3. Worker 总代码量不同**

实现完全相同的 worker 功能（并发投递 + 退避重试 + DLQ + crash recovery）：Go 需要 **307 行**，Elixir 需要 **58 行**。差值是领域无关的基础设施代码，在 Elixir 版本中由经过生产验证的 Oban 承担。

详细的逐点对比见 [`COMPARISON.md`](./COMPARISON.md)。

### 这不是"Elixir 总比 Go 好"

这是"对这个特定问题，Elixir/OTP 的抽象层次更匹配"。

如果需求变成"处理海量实时数据流"或"对延迟极敏感的 RPC 服务"，Go 可能是更合适的选择。工程决策的质量在于识别问题的结构，然后选用与该结构契合的工具——而不是默认选用最熟悉的那个。

---

## 快速开始

### Go 版本

```bash
cd golang_ver
docker compose up --build
```

```bash
# 注册供应商
curl -X POST http://localhost:8080/vendors \
  -H "Content-Type: application/json" \
  -d '{"name":"crm","target_url":"https://httpbin.org/post","headers":{"Content-Type":"application/json"},"body_tpl":"{\"crm_id\":\"{{.user_id}}\"}"}'

# 发送通知
curl -X POST http://localhost:8080/notifications/crm \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: order-001" \
  -d '{"body":{"user_id":"u42"}}'
```

### Elixir 版本

```bash
cd elixir_ver
docker compose up --build
```

```bash
# 注册供应商（body_tpl 使用受限占位符语法，不执行 Elixir 代码）
curl -X POST http://localhost:8080/vendors \
  -H "Content-Type: application/json" \
  -d '{"name":"crm","target_url":"https://httpbin.org/post","headers":{"Content-Type":"application/json"},"body_tpl":"{\"crm_id\":\"<%= @user_id %>\"}"}'

# 发送通知（接口完全相同）
curl -X POST http://localhost:8080/notifications/crm \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: order-001" \
  -d '{"body":{"user_id":"u42"}}'
```

---

## 仓库结构

```
rc_LingHeChen/
├── README.md              ← 本文件
├── AI_USAGE.md            ← AI 使用说明
├── COMPARISON.md          ← 逐点技术对比（代码量、进程隔离、崩溃恢复）
├── .github/workflows/     ← 两个版本可被 GitHub Actions 发现的 CI
├── demo/
│   └── k6_loadtest.js     ← k6 压测脚本（两版本通用）
├── golang_ver/            ← Go 实现
│   ├── README.md
│   ├── cmd/ internal/ migrations/ tests/
│   ├── demo/panic_demo.go ← Go panic 行为演示
│   └── docker-compose.yml
└── elixir_ver/            ← Elixir 实现
    ├── README.md
    ├── lib/ test/ priv/
    ├── demo/              ← mix demo.crash（OTP 进程隔离演示）
    └── docker-compose.yml
```

---

## API 接口（两版本完全一致）

```
GET  /healthz
POST /vendors
GET  /vendors
GET  /vendors/:name
PUT  /vendors/:name
DELETE /vendors/:name
POST /notifications/:vendor_name   Header: Idempotency-Key
```

两版本的 HTTP 接口、行为语义（at-least-once、按供应商隔离的幂等去重、退避策略）完全相同，可以互换部署。唯一的外部差异是 body 模板语法：Go 版用 `{{.key}}`，Elixir 版用受限的 `<%= @key %>` 占位符；后者不会执行 Elixir 代码。
