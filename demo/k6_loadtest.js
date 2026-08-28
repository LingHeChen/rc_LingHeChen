/**
 * k6 压力测试脚本 — 对比 Go 版本与 Elixir 版本
 *
 * 安装 k6：https://k6.io/docs/getting-started/installation/
 *
 * 运行（先启动对应版本的服务）：
 *   k6 run --env BASE_URL=http://localhost:8080 demo/k6_loadtest.js
 *
 * 测试项目：
 *   1. POST /vendors     — 创建供应商（setup）
 *   2. POST /notifications/:vendor — 提交通知（主要吞吐量测试）
 *   3. 重复 Idempotency-Key    — 验证幂等去重在高并发下的行为
 */

import http from "k6/http";
import { check, sleep } from "k6";
import { Counter, Rate, Trend } from "k6/metrics";

// ── 自定义指标 ──────────────────────────────────────────────────────────────
const enqueueErrors = new Counter("enqueue_errors");
const enqueueSuccess = new Counter("enqueue_success");
const idempotentConflicts = new Counter("idempotent_conflicts"); // 同 key 重复返回的次数
const enqueueDuration = new Trend("enqueue_duration_ms", true);

// ── 测试配置 ────────────────────────────────────────────────────────────────
export const options = {
  scenarios: {
    // 场景 1：基础吞吐量 — 100 VU 并发跑 30 秒
    throughput: {
      executor: "constant-vus",
      vus: 100,
      duration: "30s",
      tags: { scenario: "throughput" },
    },
    // 场景 2：幂等去重压力 — 10 VU 各发 50 次相同 key
    idempotency_stress: {
      executor: "constant-vus",
      vus: 10,
      duration: "10s",
      startTime: "32s", // 在 throughput 结束后运行
      tags: { scenario: "idempotency" },
      env: { SAME_KEY: "stress-idem-key" },
    },
  },
  thresholds: {
    http_req_duration: ["p(95)<200"],   // 95% 请求在 200ms 内
    http_req_failed: ["rate<0.01"],     // 错误率 < 1%
  },
};

const BASE_URL = __ENV.BASE_URL || "http://localhost:8080";

// ── Setup：创建测试供应商 ────────────────────────────────────────────────────
export function setup() {
  const res = http.post(
    `${BASE_URL}/vendors`,
    JSON.stringify({
      name: "k6-test-vendor",
      target_url: "https://httpbin.org/post",
      method: "POST",
      headers: { "Content-Type": "application/json" },
    }),
    { headers: { "Content-Type": "application/json" } }
  );

  check(res, { "vendor created (201 or already exists 400)": (r) => r.status === 201 || r.status === 400 });
  return { vendorName: "k6-test-vendor" };
}

// ── 主测试函数 ───────────────────────────────────────────────────────────────
export default function (data) {
  const vendorName = data.vendorName;
  const idempotencyKey = __ENV.SAME_KEY || `vu-${__VU}-iter-${__ITER}`;

  const payload = JSON.stringify({
    body: {
      event: "user.registered",
      user_id: `user-${__VU}-${__ITER}`,
    },
  });

  const headers = {
    "Content-Type": "application/json",
    "Idempotency-Key": idempotencyKey,
  };

  const start = Date.now();
  const res = http.post(`${BASE_URL}/notifications/${vendorName}`, payload, { headers });
  const duration = Date.now() - start;

  enqueueDuration.add(duration);

  if (res.status === 202) {
    enqueueSuccess.add(1);

    // 检查是否是幂等冲突（同一个 id 被多次返回）
    try {
      const body = JSON.parse(res.body);
      if (__ENV.SAME_KEY && body.idempotency_key === __ENV.SAME_KEY) {
        idempotentConflicts.add(1);
      }
    } catch (_) {}
  } else {
    enqueueErrors.add(1);
    console.error(`Unexpected status ${res.status}: ${res.body}`);
  }

  check(res, {
    "status is 202": (r) => r.status === 202,
    "response has id": (r) => {
      try {
        return JSON.parse(r.body).id !== undefined;
      } catch (_) {
        return false;
      }
    },
  });

  sleep(0.01); // 10ms 间隔，控制 RPS
}

// ── Teardown：清理 ───────────────────────────────────────────────────────────
export function teardown(data) {
  const res = http.del(`${BASE_URL}/vendors/${data.vendorName}`);
  check(res, { "vendor deleted": (r) => r.status === 204 });
}
