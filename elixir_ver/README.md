# rc_LingHeChen — Elixir 版本

> **Elixir 实现版本。** 本目录是对同一需求的 Elixir 重写。为什么做两个版本，见[根目录 README](../README.md)。
> 与 Go 版本的逐点技术对比见 [`COMPARISON.md`](../COMPARISON.md)。
> AI 使用说明见 [`AI_USAGE.md`](../AI_USAGE.md)。

---

## 问题理解

本质上这是一个**异步可靠投递**问题，核心矛盾是：

- 业务系统要求**低延迟**（提交即返回）
- 外部 API 要求**可靠到达**（对端可能随时不可用）

解决方法是解耦：业务系统写队列，worker 异步投递。同时，不同外部供应商的 URL、Header、Body 格式差异很大，本服务通过**供应商配置**统一持有这些信息，业务系统只需传入语义数据。

---

## 架构

```
业务系统
  │
  │ POST /notifications/:vendor_name
  │ Header: Idempotency-Key: <key>
  ▼
┌─────────────┐    Oban.insert    ┌──────────────────────┐
│  HTTP 接收层 │ ───────────────▶ │    oban_jobs 表       │
│   (Plug)    │   返回 202        │  (PostgreSQL-backed)  │
└─────────────┘                  └──────────┬───────────┘
                                            │  SELECT FOR UPDATE SKIP LOCKED
                                            │  (由 Oban 内部管理)
                                            ▼
                                 ┌──────────────────────┐
                                 │   Oban Worker Pool    │
                                 │  (5 Erlang 进程)      │
                                 └──────────┬───────────┘
                                            │
                               ┌────────────┴────────────┐
                               │                         │
                          :ok (成功)            {:error, _} 重试
                          状态 → completed      指数退避 + jitter
                                               超限 → {:discard, _}
                                               状态 → discarded

供应商配置（vendor_configs 表）在入队时展开（body 渲染、header 合并），
变更不影响已入队的任务。
```

### 与 Go 版本的架构差异

Go 版本手动实现了 worker pool（goroutine）、`SELECT FOR UPDATE SKIP LOCKED`、`recoverLoop`、指数退避。

本版本使用 **Oban**，它在 PostgreSQL 之上提供了完整的队列语义，由 OTP Supervisor 树管理生命周期。每个 job 在独立的 Erlang 进程中执行，崩溃自动隔离，无需任何手写恢复代码。

---

## 关键设计决策

### 1. 投递语义：At-least-once

选择**至少一次**。Exactly-once 需要对端支持幂等，无法单方面保证。  
Oban 在 worker 的 `perform/1` 返回前崩溃时，会将 job 标记为 retryable 并重新调度，可能导致重复投递。外部系统应设计为幂等。

### 2. 供应商配置 + Body 模板（受限占位符语法）

供应商的 `target_url`、`method`、`headers` 统一在本服务配置，由 `/vendors` CRUD 管理。  
业务系统只传入语义 body，本服务按 `body_tpl` 模板渲染后转发。

**模板语法：EEx 风格的受限占位符（不执行 Elixir 代码）**
```
Go 版本：  {"crm_id":"{{.user_id}}"}
本版本：   {"crm_id":"<%= @user_id %>"}
```

只允许 `<%= @field %>`，其他 `<% ... %>` 表达式会在保存 Vendor 时被拒绝。实现使用字符串替换，不调用 `EEx.eval_string`，也不会将业务字段转换成 Atom。

Header 合并策略：调用方的 `extra_headers` 为基础，供应商 headers 覆盖（鉴权信息不可被调用方覆盖）。

### 3. Oban 替代手写队列

Oban 使用 PostgreSQL `SELECT FOR UPDATE SKIP LOCKED` 实现队列语义，与 Go 版本的底层机制完全相同，但封装了所有并发控制、崩溃恢复、状态机管理的细节。DB 已是依赖项，零额外基础设施。

不使用 Oban 的替代方案，是像 Go 版一样基于 PostgreSQL 自行实现 `SKIP LOCKED` 出队、worker pool、重试调度、任务状态机和 stuck-job 恢复扫描。这个方案可控但需要维护更多并发与故障恢复代码；对 MVP 而言，使用成熟调度框架比重复实现基础设施更稳妥。若未来 Oban 的约束不再适合，也可以替换为自研 DB queue 或 SQS/RabbitMQ，但需要重新实现 worker 接口和状态映射。

### 4. 4xx / 5xx 区别对待

- `5xx` / 网络错误 / `429 Too Many Requests` / `408 Request Timeout`：临时故障，返回 `{:error, reason}`，Oban 安排重试
- 其他 `4xx`：请求配置错误，返回 `{:discard, reason}`，Oban 将 job 移入 discarded（等价 Go 版本的 DLQ）

### 5. 退避 + Jitter

通过 `backoff/1` callback 覆盖 Oban 默认退避，精确匹配 Go 版本的策略：  
`1m → 2m → 4m → … → 24h cap`，每次加 ±20% 随机抖动。

### 6. 幂等性

Oban unique jobs：`unique: [period: :infinity, keys: [:vendor_name, :idempotency_key], states: [所有状态]]`。  
同一供应商下重复 key 的请求返回已有 job 的 id，不重复入队；不同供应商可以复用同一个业务 key。  
**注意：** `Oban.Plugins.Pruner` 默认 7 天后清理已完成的 job，7 天后相同 key 可重新入队（Go 版本通过 DB 唯一约束永久防重，无此限制）。

### 7. 外部系统长期不可用

每个任务最多尝试 **10 次**。临时故障按照指数退避和 ±20% jitter 重试；达到上限仍未成功时，Oban 将任务标记为 `discarded`，保留尝试次数和错误信息供排查。永久性 4xx 直接 `discard`，不会继续占用队列。

MVP 不提供 discarded-job 重投 UI。运维人员通过日志和 `oban_jobs` 状态判断故障原因，确认供应商恢复或配置修正后再人工处理。只有当失败任务需要频繁干预时，才增加告警、批量重投接口和带权限审计的管理界面。

### 8. 当前可观测性取舍

当前提供 `/healthz` 存活检查、应用日志、Oban 持久化任务状态，以及 Oban 内建 Telemetry 事件。`/healthz` 只表示 HTTP 进程可以响应，不代表数据库和外部供应商全部健康。

MVP 暂不新增 `/metrics`。直接在应用内维护计数器会遇到多实例聚合和重启丢失问题，而当前尚无明确 SLO。进入生产化阶段后，优先把 Oban/Bandit Telemetry 接入 Prometheus 或 OpenTelemetry，暴露 enqueue 数量、投递成功率、重试率、discarded 数量、队列积压和投递延迟；如果部署平台需要，再单独增加包含数据库探测的 readiness 接口。

---

## 系统边界

**解决：**
- 接收通知请求并持久化（防丢失）
- 供应商配置统一管理，入队时快照（body 渲染 + header 合并）
- Body 模板渲染（受限占位符，不执行代码）
- 异步投递 + 指数退避重试 + Discard（DLQ 等价）
- 入口幂等（`Idempotency-Key`，重复请求返回原 job id）
- Crash recovery（OTP Supervisor 自动）

**明确不解决（MVP 范围外）：**
- 消息顺序保证
- Rate limiting per vendor
- Discarded job 重投 UI
- 实时监控大盘和 `/metrics`（已有日志、任务状态和 Telemetry；达到需要 SLO/自动告警的阶段再接入）
- 公网级认证与任意 URL 防护：MVP 假定部署在已认证的内部网关后，`/vendors` 仅对管理员开放；生产化时应增加目标域名 allowlist、凭证加密与响应脱敏

---

## 演进路径

| 触发条件 | 演进方向 |
|---------|---------|
| 需要可观测性 | Oban 内建 Telemetry 事件，直接接入 Prometheus / Grafana |
| 供应商鉴权复杂化 | vendor config 增加签名、OAuth token 刷新字段 |
| 需要 Discarded job 重投 | Oban Web（开源 UI）或自定义 admin 接口 |
| 写入吞吐达到 DB 瓶颈 | Oban Pro 支持多队列分片；或换 SQS/RabbitMQ（需替换 worker） |

---

## API

### 供应商配置

```
POST   /vendors              创建供应商
GET    /vendors              列出所有供应商
GET    /vendors/:name        获取供应商
PUT    /vendors/:name        更新供应商
DELETE /vendors/:name        删除供应商
```

**创建供应商 Request**
```json
{
  "name": "crm",
  "target_url": "https://api.crm.com/webhook",
  "method": "POST",
  "headers": { "Authorization": "Bearer secret", "Content-Type": "application/json" },
  "body_tpl": "{\"crm_id\":\"<%= @user_id %>\",\"event\":\"<%= @event %>\"}"
}
```

`body_tpl` 为空时原样转发调用方 body（JSON 编码）。

### 发送通知

```
POST /notifications/:vendor_name
Header: Idempotency-Key: <key>   // 可选，省略则自动生成
```

**Request body**
```json
{
  "body": { "event": "user.registered", "user_id": "u42" },
  "extra_headers": { "X-Trace-Id": "abc" }
}
```

**Response** `202 Accepted`
```json
{
  "id": 1234,
  "idempotency_key": "order-paid-12345"
}
```

`id` 为 Oban job id（integer，与 Go 版本的 UUID 不同）。  
同一供应商下重复 `Idempotency-Key` 请求返回原 job 的 `id`，不会重复入队。

### GET /healthz

健康检查，返回 `200 OK`。

---

## 本地运行

**依赖：** Docker, Docker Compose

```bash
docker compose up --build
```

单独运行（需要本地 PostgreSQL + Elixir 1.16+）：

```bash
cd elixir_ver

# 安装依赖
mix deps.get

# 创建数据库并执行迁移
DATABASE_URL=postgres://notify:notify@localhost:5432/notify mix ecto.setup

# 启动服务
DATABASE_URL=postgres://notify:notify@localhost:5432/notify mix run --no-halt
```

**完整流程示例：**

```bash
# 1. 注册供应商
curl -X POST http://localhost:8080/vendors \
  -H "Content-Type: application/json" \
  -d '{
    "name": "crm",
    "target_url": "https://httpbin.org/post",
    "headers": {"Content-Type": "application/json"},
    "body_tpl": "{\"crm_id\":\"<%= @user_id %>\"}"
  }'

# 2. 发送通知
curl -X POST http://localhost:8080/notifications/crm \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: order-paid-001" \
  -d '{"body": {"user_id": "u42"}}'
```

---

## 测试

测试分三层，从快到慢、从纯逻辑到真实启动。仓库根目录 CI（`../.github/workflows/elixir_ci.yml`）依次跑完全部三层。

> **注意：** 本服务的 OTP application 在启动时会绑定 HTTP 端口（`http_port`，默认 8080）。`mix test` 会启动完整监督树，所以跑测试时 8080 必须空闲，否则用 `PORT=<空闲端口>` 覆盖。下面示例统一用 `PORT=8099`。

```bash
export DATABASE_URL=postgres://notify:notify@localhost:5432/notify_test
export PORT=8099
```

### 1. 静态门禁

```bash
mix format --check-formatted     # 格式
mix compile --warnings-as-errors # 编译零警告
mix credo --strict               # 静态分析
```

### 2. 单元 + 集成测试（快）

```bash
mix test
```

覆盖 router、vendors、worker 的投递语义（2xx/5xx/4xx/429/408/网络错误映射、退避曲线）。只有 e2e 用例（`@moduletag :integration`）默认排除，保证这一层快。

其中 `test/rc_notification/oban_config_test.exs` 是一条**回归测试**：它用 `Oban.Config.new/1` 校验**去掉 `testing:` 覆盖后**的真实生产 Oban 配置。原因是 Oban 在 `testing: :manual` 模式下会强制清空 `plugins`/`queues` 再校验，导致坏的插件配置在普通测试里永远暴露不出来、只在真实启动时才崩。这条测试把校验拉回到"生产会怎么跑"。

### 3. E2E 测试（需要 DB，用 Bandit 起真实目标 HTTP 服务器）

```bash
mix test --include integration
```

覆盖完整链路：HTTP API → Oban 入队 → worker 投递 → 真实 TCP 请求到目标。包含 body 模板渲染、header 覆盖、5xx→retryable、4xx→discarded、**瞬时失败后重试恢复**、幂等去重。

### 4. Release gate（构建并真正启动 release）

```bash
DATABASE_URL=postgres://notify:notify@localhost:5432/notify_smoke \
PORT=8092 MIX_ENV=prod ./scripts/release_smoke.sh
```

构建 prod release → 迁移 → **以 daemon 启动** → 轮询 `/healthz` → 真实 vendor + notification 冒烟 → 停止。

这一步专门拦"编译通过、测试全绿、但一启动就崩"的 bug（例如无效的 Oban 插件配置——只会在监督树启动时炸）。`mix release` 只组装不启动，拦不住；只有真正 boot 起来打 `/healthz` 才算数。

### OTP 进程隔离 Demo

```bash
mix demo.crash   # 演示 1 个 job 崩溃不影响其他 job 和 HTTP server
```

---

## 环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `DATABASE_URL` | `postgres://notify:notify@localhost:5432/notify` | PostgreSQL 连接串 |
| `PORT` | `8080` | HTTP 监听端口 |
