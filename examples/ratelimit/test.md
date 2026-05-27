# examples/ratelimit 端到端测试方案

> 本文档与 `verify_ratelimit.sh` 保持一致，新增/修改用例时同步更新本文件。

## 总览

`verify_ratelimit.sh` 端到端验证 polaris-go 限流 SDK 的以下能力：

| 用例组 | 验证能力 | 限流类型 |
|--------|----------|----------|
| 1.x | QPS reject（快速失败） | LOCAL |
| 2.x | QPS unirate（匀速排队） | LOCAL |
| 3.x | 并发数限流 | LOCAL |
| 4.x | 多维 AND 匹配 | LOCAL |
| 5.x | regex_combine 合并阈值开关 | LOCAL |
| 6.x | 分布式集群限流 | GLOBAL |
| 7.x | CustomResponse 自定义返回 | LOCAL + GLOBAL |
| 8.x | Prometheus 监控指标 | LOCAL |
| 9.x | 限流事件上报（RateLimitStart/End） | LOCAL |

## 前置条件

| 依赖 | 说明 |
|------|------|
| Polaris 服务端 | gRPC `:8091`、HTTP `:8090` 可达 |
| Go 工具链 | 编译 provider / consumer |
| `python3` | 拼接 JSON 请求体 |
| `curl` | HTTP 请求 |

## 运行方式

```bash
# 全部用例
./verify_ratelimit.sh

# 仅运行指定用例组（逗号分隔）
./verify_ratelimit.sh --only qps
./verify_ratelimit.sh --only qps,metrics
./verify_ratelimit.sh --only custom-response

# 跳过指定用例组
./verify_ratelimit.sh --skip concurrency,global
```

### 用例组名称对照

| `--only` / `--skip` 名称 | 对应用例 | 说明 |
|---------------------------|----------|------|
| `qps` | 1.x | QPS reject 限流 |
| `unirate` | 2.x | QPS unirate 匀速排队 |
| `concurrency` | 3.x | 并发数限流 |
| `custom` | 4.x | 多维 AND 匹配 |
| `regex` | 5.x | regex_combine 合并阈值 |
| `global` | 6.x | GLOBAL 分布式集群限流 |
| `custom-response` | 7.x | CustomResponse 自定义返回 |
| `metrics` | 8.x | Prometheus 监控指标验证 |
| `events` | 9.x | 限流事件上报验证 |

### 其他参数

| 参数 | 说明 |
|------|------|
| `--polaris-server <ip>` | Polaris 服务端地址（默认 `127.0.0.1`） |
| `--polaris-token <token>` | 鉴权 token（开启鉴权时必填） |
| `--limiter-service <svc>` | 覆盖 polaris.limiter 服务名（本地 limiter 场景） |
| `--keep` | 测试结束后保留 provider/consumer 进程和日志 |
| `--debug` | 所有 binary 开启 Polaris SDK DEBUG 日志 |

## 公共配置

### Provider — Prometheus Metrics（pull 模式）

每个 **provider** 实例的 metrics 端口 = **业务端口 + 10000**，通过环境变量 `${POLARIS_METRICS_PORT}` 注入 `polaris.yaml`：

```yaml
# provider-qps/polaris.yaml & provider-concurrency/polaris.yaml
global:
  statReporter:
    enable: true
    chain: [prometheus]
    plugin:
      prometheus:
        type: pull
        metricHost: 127.0.0.1
        metricPort: "${POLARIS_METRICS_PORT}"
```

### Consumer — Prometheus Metrics（push 模式）

所有 **consumer** 实例使用 push 模式，不暴露 /metrics 端口：

```yaml
# consumer/polaris.yaml
global:
  statReporter:
    enable: true
    chain: [prometheus]
    plugin:
      prometheus:
        type: push
        address: ${POLARIS_SERVER}:9091
        interval: 10s
```

### 事件上报（EventReporter）

```yaml
global:
  eventReporter:
    enable: true
    chain: [pushgateway]
```

限流状态切换（UNLIMITED↔LIMITED）时 SDK 自动构造 `RateLimitStart` / `RateLimitEnd` 事件并 POST 到 `Polaris/polaris.pushgateway`。

---

## 用例 1.x — QPS 限流（reject 策略）

### 规则

| 字段 | 值 |
|------|-----|
| name | `ratelimit-e2e-qps-rule` |
| service / namespace | `QpsRatelimitEchoServer` / `default` |
| resource | `QPS` |
| type | `LOCAL` |
| method | EXACT `/echo` |
| amounts | `maxAmount=2 / validDuration=1s` |
| action | `REJECT` |

### 启动服务

| 角色 | 源码 | 端口 | metrics 端口 |
|------|------|------|-------------|
| provider | `provider-qps` | 18180 | 28180 |
| consumer | `consumer` | 18190 | —（push 模式） |

### 请求链路

```
curl → consumer:18190/echo → (polaris 服务发现) → provider:18180/echo → LimitAPI.GetQuota
```

### 用例 1.1 — QPS 限流触发

| 项 | 内容 |
|-----|------|
| 操作 | 串行 6 次 GET `http://127.0.0.1:18190/echo` |
| 预期 | `200 ≈ 2`、`429 ≥ 2` |
| 判定 | `429 ≥ 2 && other == 0` → PASS |

### 用例 1.2 — 新窗口重新放通 + 再次限流

| 项 | 内容 |
|-----|------|
| 操作 | 等 2s 跨窗口 → 单发 1 次（验证放通）→ 再串行 6 次（验证再次限流） |
| 预期 | 单发 200；后续 `429 ≥ 2 && other == 0` |
| 判定 | 单发 200 且突发 `429 ≥ 2` → PASS |

---

## 用例 2.x — QPS 限流（unirate 匀速排队策略）

### 规则

| 字段 | 值 |
|------|-----|
| name | `ratelimit-e2e-unirate-rule` |
| service / namespace | `UnirateRatelimitEchoServer` / `default` |
| resource | `QPS` |
| type | `LOCAL` |
| method | EXACT `/echo` |
| amounts | `maxAmount=4 / validDuration=2s`（即 2 QPS） |
| action | `UNIRATE` |
| max_queue_delay | `1s` |

### 启动服务

| 角色 | 源码 | 端口 | metrics 端口 |
|------|------|------|-------------|
| provider | `provider-qps` | 18182 | 28182 |
| consumer | `consumer` | 18192 | —（push 模式） |

### 请求链路

```
curl → consumer:18192/echo → provider:18182/echo → LimitAPI.GetQuota (unirate 排队)
```

### 用例 2.1 — 匀速排队全部通过

| 项 | 内容 |
|-----|------|
| 操作 | **串行** 3 次 GET |
| 预期 | 全部 200，总耗时 ≥ 700ms（SDK 排队 sleep） |
| 判定 | `200 == 3 && 耗时 ≥ 700ms` → PASS |

### 用例 2.2 — 排队超 maxQueueDelay 触发丢弃

| 项 | 内容 |
|-----|------|
| 操作 | **并发** 6 次 GET（并发让排队全部堆积，后发请求 wait > 1s） |
| 预期 | `429 ≥ 2`，无 other |
| 判定 | `limited ≥ 2 && other == 0` → PASS |

### 用例 2.3 — 冷却后规则持续生效

| 项 | 内容 |
|-----|------|
| 操作 | 等 3s 冷却 → 再次**并发** 6 次 |
| 预期 | 与 2.2 一致 |
| 判定 | `limited ≥ 2 && ok ≥ 1 && other == 0` → PASS |

---

## 用例 3.x — 并发数限流

### 规则

| 字段 | 值 |
|------|-----|
| name | `ratelimit-e2e-concurrency-rule` |
| service / namespace | `ConcurrencyEchoServer` / `default` |
| resource | `CONCURRENCY` |
| type | `LOCAL` |
| method | EXACT `/slow` |
| concurrencyAmount.maxAmount | `2` |

### 启动服务

| 角色 | 源码 | 端口 | metrics 端口 |
|------|------|------|-------------|
| provider | `provider-concurrency` | 18181 | 28181 |
| consumer | `consumer` | 18191 | —（push 模式） |

### 请求链路

```
curl → consumer:18191/slow?ms=N → provider:18181/slow (sleep N ms, defer future.Release())
```

### 用例 3.1 — 并发超限触发限流

| 项 | 内容 |
|-----|------|
| 操作 | **并发** 5 次 GET `/slow?ms=1500` |
| 预期 | `200 ≤ 2`、`429 ≥ 3` |
| 判定 | `ok ≤ 2 && limited ≥ 3 && other == 0` → PASS |

### 用例 3.2 — Release 归还后放通 + 再次限流

| 项 | 内容 |
|-----|------|
| 操作 | 等 3s（上批请求结束、Release 归还）→ 单发 `/slow?ms=200`（验证归还）→ 并发 5 次 `/slow?ms=1500` |
| 预期 | 单发 200；后续 `ok ≤ 2 && 429 ≥ 3` |
| 判定 | 单发 200 且突发满足条件 → PASS |

### 用例 3.3 — 低于上限全放通（反向用例）

| 项 | 内容 |
|-----|------|
| 操作 | 并发 2 次 `/slow?ms=600`（≤ 上限） |
| 预期 | `200 == 2 && 429 == 0` |
| 判定 | 全部通过 → PASS |

---

## 用例 4.x — 多维 AND 匹配

### 规则

| 字段 | 值 |
|------|-----|
| name | `ratelimit-e2e-custom-match-rule` |
| service / namespace | `CustomMatchEchoServer` / `default` |
| resource | `QPS` |
| type | `LOCAL` |
| method | EXACT `/echo` |
| amounts | `maxAmount=2 / 1s` |
| action | `REJECT` |
| arguments（AND） | HEADER `x-tenant=gold` + QUERY `region=cn-east` + CALLER_SERVICE `default/CustomCallerService` + CALLER_IP `10.0.0.1` + CALLER_METADATA `env=prod` |

### 启动服务

| 角色 | 源码 | 端口 | metrics 端口 | 额外参数 |
|------|------|------|-------------|---------|
| provider | `provider-qps` | 18183 | 28183 | — |
| consumer | `consumer` | 18193 | —（push 模式） | `--caller-service=default/CustomCallerService --caller-ip=10.0.0.1 --caller-metadata=env=prod` |

### 请求链路（维度注入）

| 维度 | 来源 |
|------|------|
| HEADER `x-tenant` | curl `-H 'x-tenant: gold'` |
| QUERY `region` | curl `?region=cn-east` |
| METHOD | provider `quotaReq.SetMethod("/echo")` |
| CALLER_SERVICE | consumer `--caller-service` → `X-Polaris-Caller-Service` header |
| CALLER_IP | consumer `--caller-ip` → `X-Polaris-Caller-IP` header |
| CALLER_METADATA | consumer `--caller-metadata` → `X-Polaris-Caller-Metadata-env` header |

### 用例 4.1 — 全维度命中触发限流

| 项 | 内容 |
|-----|------|
| 操作 | 串行 5 次 GET `/echo?region=cn-east` + header `x-tenant: gold` |
| 预期 | `429 ≥ 2` |
| 判定 | `429 ≥ 2 && other == 0` → PASS |

### 用例 4.2 — 单维不命中全放通（反向验证 AND）

| 项 | 内容 |
|-----|------|
| 操作 | 同 4.1 但 query 改为 `?region=cn-west` |
| 预期 | 全部 200 |
| 判定 | `200 == 5 && 429 == 0` → PASS |

### 用例 4.3 — 新窗口持续生效

| 项 | 内容 |
|-----|------|
| 操作 | 等 2s → 再次全命中模式 5 次 |
| 预期 | `429 ≥ 2` |
| 判定 | `429 ≥ 2 && other == 0` → PASS |

---

## 用例 5.x — regex_combine 合并阈值

### 规则

| 字段 | 值 |
|------|-----|
| name | `ratelimit-e2e-regex-combine-rule` |
| service / namespace | `RegexCombineEchoServer` / `default` |
| resource | `QPS` |
| type | `LOCAL` |
| method | **REGEX** `/users/.*/orders` |
| amounts | `maxAmount=4 / 1s` |
| action | `REJECT` |
| regex_combine | 初始 `false`；用例 5.2 通过 PUT 翻为 `true` |

### 启动服务

| 角色 | 源码 | 端口 | metrics 端口 |
|------|------|------|-------------|
| provider | `provider-qps` | 18184 | 28184 |
| consumer | `consumer` | 18194 | —（push 模式） |

### 用例 5.1 — regex_combine=false 各 path 独享

| 项 | 内容 |
|-----|------|
| 操作 | 并发打 `/users/100/orders` × 5 + `/users/200/orders` × 5（共 10） |
| 预期 | 两条 path 各独享 4/1s → 总 `429 ≥ 2`、`200 ≤ 10` |
| 判定 | `limited ≥ 2 && ok ≤ 10 && other == 0` → PASS |

### 用例 5.2 — regex_combine=true 共享阈值

| 项 | 内容 |
|-----|------|
| 操作 | PUT 翻转 `regex_combine=true` → 等 3s → 同 5.1 请求 |
| 预期 | 两条 path 共享 4/1s → 总 `200 ≈ 4`、`429 ≈ 6` |
| 判定 | `limited ≥ 2 && ok ≤ 8 && other == 0` → PASS |
| 收尾 | 自动 PUT 恢复 `regex_combine=false` |

---

## 用例 6.x — GLOBAL 分布式集群限流

### 规则

| 字段 | 值 |
|------|-----|
| name | `ratelimit-e2e-global-rule` |
| service / namespace | `GlobalRatelimitEchoServer` / `default` |
| resource | `QPS` |
| type | **`GLOBAL`** |
| method | EXACT `/echo` |
| amounts | `maxAmount=4 / 1s` |
| action | `REJECT` |
| failover | `FAILOVER_LOCAL` |
| 远端集群 | `Polaris/polaris.limiter`（可由 `--limiter-service` 覆盖） |

### 启动服务

| 角色 | 源码 | 端口 | metrics 端口 | 说明 |
|------|------|------|-------------|------|
| provider A | `provider-qps` | 18185 | 28185 | 实例 A |
| provider B | `provider-qps` | 18186 | 28186 | 实例 B |
| provider (failover) | `provider-qps` | 18187 | 28187 | 用例 6.5 专用 |
| consumer | `consumer` | 18195 | —（push 模式） | 6.1/6.2 使用 |
| consumer (failover) | `consumer` | 18197 | —（push 模式） | 用例 6.5 专用 |

### 用例 6.0 — polaris.limiter 探测（前置条件）

| 项 | 内容 |
|-----|------|
| 操作 | GET `/naming/v1/instances?service=polaris.limiter&namespace=Polaris` |
| 判定 | 有健康实例 → 继续 6.1~6.5；无实例 → SKIP |

### 用例 6.1 — 多窗口聚合触发限流

| 项 | 内容 |
|-----|------|
| 操作 | 连续 4 个 1s 窗口，每窗口经 consumer 并发 8 次（共 32 次） |
| 预期 | `limited ≥ 4` |
| 判定 | `limited ≥ 4 && other == 0` → PASS |

### 用例 6.2 — 新窗口持续生效

| 项 | 内容 |
|-----|------|
| 操作 | 等 1 窗口 → 再连续 4 窗口并发 8 次 |
| 预期 | `limited ≥ 4` |
| 判定 | 同 6.1 |

### 用例 6.3 — 多实例共享配额（核心语义）

| 项 | 内容 |
|-----|------|
| 操作 | 直接打 A:18185 并发 5 + B:18186 并发 5，连续 4 窗口（共 40 次） |
| 预期 | GLOBAL 合计仅 4/窗口 → `ok ≤ 24`、`limited ≥ 4` |
| 判定 | `ok ≤ 24 && limited ≥ 4 && other == 0` → PASS |
| 备注 | 若 `ok > 24`，说明退化为 LOCAL，检查 polaris.limiter 连通性 |

### 用例 6.4 — GLOBAL + regex_combine 共享

| 项 | 内容 |
|-----|------|
| 操作 | PUT regex 规则切到 `GLOBAL+regex_combine=true` → 打 provider:18184 两条 path × 4 窗口 |
| 前提 | 必须先跑过 5.x（复用 regex provider） |
| 预期 | `ok ≤ 24`、`limited ≥ 4` |
| 收尾 | 自动 PUT 恢复 `LOCAL+regex_combine=false` |

### 用例 6.5 — 远端不可达降级（FAILOVER_LOCAL）

| 项 | 内容 |
|-----|------|
| 操作 | `POLARIS_LIMITER_SVC=不存在的服务` 启动新 provider+consumer → 4 窗口并发 8 次 |
| 预期 | 本地配额生效：`limited ≥ 4` |
| 判定 | `limited ≥ 4 && other == 0` → PASS |

---

## 用例 7.x — CustomResponse 自定义返回

### 规则（LOCAL）

| 字段 | 值 |
|------|-----|
| name | `ratelimit-e2e-custom-response-rule` |
| service / namespace | `CustomResponseEchoServer` / `default` |
| resource | `QPS` |
| type | `LOCAL` |
| method | EXACT `/echo` |
| amounts | `maxAmount=2 / 1s` |
| action | `REJECT` |
| customResponse.body | `{"code":429,"reason":"verify_ratelimit e2e custom body","trace":"verify"}` |

### 规则（GLOBAL）

| 字段 | 值 |
|------|-----|
| name | `ratelimit-e2e-global-custom-response-rule` |
| service / namespace | `GlobalCustomResponseEchoServer` / `default` |
| type | `GLOBAL` |
| failover | `FAILOVER_LOCAL` |
| 其余 | 同 LOCAL 版 |

### 启动服务

| 角色 | 源码 | 端口 | metrics 端口 | 用于 |
|------|------|------|-------------|------|
| provider (LOCAL) | `provider-qps` | 18188 | 28188 | 用例 7.1 |
| consumer (LOCAL) | `consumer` | 18198 | —（push 模式） | 用例 7.1 |
| provider (GLOBAL) | `provider-qps` | 18189 | 28189 | 用例 7.2 |
| consumer (GLOBAL) | `consumer` | 18199 | —（push 模式） | 用例 7.2 |

### 用例 7.1 — LOCAL 自定义返回

| 项 | 内容 |
|-----|------|
| 链路 | curl → consumer:18198 → provider:18188 |
| 操作 | 串行 6 次 GET /echo（前 5 次预热 + 最后 1 次抓取 body/header，同一 1s 窗口内完成） |
| 断言 1 | `status == 429` |
| 断言 2 | body == 规则配置的 `customResponse.body`（字面量精确匹配） |
| 断言 3 | 响应头 `X-Polaris-RateLimit-Rule` == `ratelimit-e2e-custom-response-rule` |
| 判定 | 3 个断言全 PASS → PASS |
| 原理 | `QuotaResponse.GetActiveRule().GetCustomResponse().GetBody()` 透传规则 body；`GetActiveRuleName()` 写入响应头 |

### 用例 7.2 — GLOBAL 自定义返回

| 项 | 内容 |
|-----|------|
| 链路 | curl → consumer:18199 → provider:18189 |
| 操作 | 同 7.1（合并策略） |
| 断言 | 同 7.1，规则名为 `ratelimit-e2e-global-custom-response-rule` |
| 前提 | polaris.limiter 可用（不可用 → SKIP） |
| 原理 | 验证 GLOBAL / FAILOVER_LOCAL 路径下 customResponse 仍能透传（静态属性，不依赖远端） |

---

## 用例 8.x — 限流监控指标验证

### 规则

复用 `ratelimit-e2e-qps-rule`（QPS LOCAL，maxAmount=2/1s，作用于 `QpsRatelimitEchoServer` 的 `/echo`）

### 启动服务

| 角色 | 源码 | 端口 | metrics 端口 |
|------|------|------|-------------|
| provider | `provider-qps` | 18200 | **28200** |

> **不启动 consumer**。请求直接发到 provider，绕过服务发现 LB，确保所有请求命中同一实例。

### 用例 8.1 — /metrics 指标存在性 + 数值校验

| 项 | 内容 |
|-----|------|
| 操作 | 直接向 `http://127.0.0.1:18200/echo` 发 6 次请求 → 等 30s 聚合 → curl `http://127.0.0.1:28200/metrics` |
| 断言 1 | `/metrics` 端口可达（HTTP 200） |
| 断言 2 | `ratelimit_rq_total` / `ratelimit_rq_pass` / `ratelimit_rq_limit` 三个指标存在 |
| 断言 3 | label 包含 `callee_namespace` / `callee_service` / `callee_method` / `rule_name` |
| 断言 4 | `pass > 0` 且 `limit > 0` |
| 断言 5 | `total == pass + limit` |
| 判定 | 5 个断言全 PASS → PASS |

**为什么绕过 consumer？** consumer 通过 polaris 服务发现会把请求负载均衡到所有 `QpsRatelimitEchoServer` 实例（含其他用例启动的），导致本 provider 只看到部分数据。直接发到 provider:18200 确保 6 次请求（阈值 2/1s → pass≈2 limit≈4）全部记录在同一实例。

**metrics 时间语义**：Prometheus pull 模式下指标不带时间戳；聚合周期 30s 内的 ReportStat 数据刷入 registry，连续 60s 无新数据后 GC 清除。脚本控制了"先发请求 → 再抓 /metrics"的时序，数据必然属于那 6 次请求。

---

## 用例 9.x — 限流事件上报验证

### 规则

复用 `ratelimit-e2e-qps-rule`（QPS LOCAL，maxAmount=2/1s，作用于 `QpsRatelimitEchoServer` 的 `/echo`）

### 启动服务

| 角色 | 源码/工具 | 端口 | 说明 |
|------|-----------|------|------|
| mock event server | `mock_event_server.py` | 19090 | 模拟 pushgateway，捕获事件到 JSON 文件 |
| provider | `provider-qps` | 18202 | `POLARIS_EVENT_ADDR=127.0.0.1:19090` |

### 配置

provider 的 `polaris.yaml` 中 pushgateway 插件配置了 `address: "${POLARIS_EVENT_ADDR}"`：
- 设置时（如用例 9.x）→ SDK 直接 POST 事件到 mock server
- 未设置时 → 走 polaris 服务发现（`Polaris/polaris.pushgateway`），保持其他用例行为不变

### 事件触发原理

```
请求 → AllocateQuota() → atomic.SwapInt64(&lastCode, currentCode)
                               ↓ (previousCode != currentCode)
                         reportRateLimitEvent()
                               ↓
                         BuildRateLimitEvent() → {event_name, rule_name, ...}
                               ↓
                         EventReporter.ReportEvent() → 队列 → 每秒 batch flush
                               ↓
                         HTTP POST → mock_event_server → captured_events.json
```

### 用例 9.1 — RateLimitStart 事件

| 项 | 内容 |
|-----|------|
| 操作 | 直接向 provider:18202/echo 发 6 次请求（触发 UNLIMITED→LIMITED） → 等 3s batch flush |
| 断言 | `captured_events.json` 中存在 `event_name=RateLimitStart` 且 `rule_name=ratelimit-e2e-qps-rule` |
| 判定 | 事件存在且字段匹配 → PASS |

### 用例 9.2 — RateLimitEnd 事件

| 项 | 内容 |
|-----|------|
| 操作 | 等 2s 窗口恢复 → 发 1 次请求（触发 LIMITED→UNLIMITED） → 等 3s flush |
| 断言 | `captured_events.json` 中存在 `event_name=RateLimitEnd` 且 `rule_name=ratelimit-e2e-qps-rule` |
| 判定 | 事件存在且字段匹配 → PASS |

---

## 判定与汇总

```
✅ [编号 名称] PASS - 详情
❌ [编号 名称] FAIL - 详情
⏭️  [编号 名称] SKIP - 详情（环境不具备前置条件，不计入失败）
```

退出码：全部 PASS → 0；N 个 FAIL → N

## 失败排查

| 现象 | 排查方向 |
|------|----------|
| `HTTP=403002` | Polaris 开启鉴权，用 `--polaris-token` 传入 token |
| `provider 30 秒内未就绪` | 查 `.logs/<provider>.log`，常见为 polaris 不可达 |
| 2.1 实际 429 | `max_queue_delay` 太小 → 控制台调大 |
| 2.1 耗时 < 700ms | 规则 action 不是 `UNIRATE`（被当 reject 处理） |
| 3.1 出现 `200 > 2` | SDK 尚未拉到规则（脚本预留了 4s，检查网络） |
| 3.2 单发 429 | 上批请求未 Release → 检查 `defer future.Release()` |
| 6.3 `ok > 24` | polaris.limiter 不可达 → FAILOVER_LOCAL 退化为各自独享 |
| 7.1 status=200 | 窗口跨秒（已用合并策略修复）；或规则不存在 |
| 8.1 `callee_namespace=""` | label supplier 误用 `EmptyInstanceGauge.GetNamespace()`（已修复为 `val.Namespace`） |
| 8.1 `limit=0` | 请求被 LB 分散（已修复为直接发 provider） |
| 8.1 端口不可达 | 需至少一次 GetQuota 调用后 SDK 才 prepare registry |

调用 `--keep` 保留进程和日志便于排查；`./cleanup.sh -f` 一键清理。

## 与单元测试的关系

本脚本验证**完整链路**（HTTP → SDK → 限流插件 → 计数 → 释放），单元测试验证组件正确性：

- `plugin/ratelimiter/reject_concurrency/bucket_concurrency_test.go`
- `pkg/flow/quota/resolver_test.go`
- `pkg/flow/quota/window_concurrency_test.go`
- `pkg/model/quota_test.go`（ActiveRule nil 安全）
- `pkg/model/event/ratelimit_test.go`（事件状态变化）
- `pkg/flow/sync_flow_test.go`（监控 Labels 格式化）
