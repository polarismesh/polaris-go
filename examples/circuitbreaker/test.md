# examples/circuitbreaker — 用例清单

本文档罗列 `verify_circuitbreaker.sh` 执行的全部用例及其判定准则。脚本中所有 `用例N.M` 编号必须与本文一致。

## 测试拓扑

```
                  ┌──── provider-a (端口 28081，默认 200) ────┐
                  │   /echo /order /info /slow + /switch       │
   Polaris ◀──────┤                                            ├──── 注册到服务 CircuitBreakerCallee
                  │   /echo /order /info /slow + /switch       │
                  └──── provider-b (端口 28082，默认 500) ────┘
                                       ▲
                                       │
   ┌── instance-consumer          (18081)  selfService=CircuitBreakerInstanceCaller
   │   └─ instance-invoke-consumer (18091)  selfService=CircuitBreakerInstanceCaller（共用）
   │
   ├── service-consumer           (18082)  selfService=CircuitBreakerServiceCaller
   │   └─ service-invoke-consumer  (18092)  selfService=CircuitBreakerServiceCaller（共用）
   │
   ├── interface-consumer         (18083)  selfService=CircuitBreakerInterfaceCaller
   │   └─ interface-invoke-consumer(18093) selfService=CircuitBreakerInterfaceCaller（共用）
   │
   ├── old-instance-consumer      (18084)  selfService=CircuitBreakerOldInstanceCaller
   │                                       （旧版散装写法，向后兼容验证）
   ├── http_status-consumer       (18085)  selfService=CircuitBreakerHttpStatusCaller
   │
   ├── default_rule-consumer      (18086)  selfService=CircuitBreakerDefaultRuleCaller
   │                                       （不依赖服务端规则，验证 SDK 本地默认规则）
   ├── modify_rule-consumer       (18087)  selfService=CircuitBreakerModifyRuleCaller
   │                                       （验证 update_circuitbreaker_rule 改参数后熔断按新阈值生效）
   ├── pm-consumer                (18088)  selfService=CircuitBreakerPMCaller
   │                                       （验证接口协议+HTTP方法维度合并匹配）
   ├── pathtype-consumer          (18089)  selfService=CircuitBreakerPathTypeCaller
   │                                       （验证 5 种 MatchString 路径匹配方式）
   └── all-dead-*-consumer        (18090-18092)
           selfService=CircuitBreakerAllDead{Decorator,Invoke,Report}Caller
           （验证全死全活兜底策略，覆盖 Decorator / InvokeHandler / Bare Report 三种调用模式）
```

### Consumer 源码分布

| 源码目录 | 调用模式 | 使用的用例 |
|----------|---------|-----------|
| `newCircuitBreakerCaller/consumer` | Decorator（`MakeFunctionDecorator` + `SetInstance`） | 1, 2, 3, 5, 6, 7, 8, 9, 10(A) |
| `invokeHandlerCaller/consumer` | InvokeHandler（`MakeInvokeHandler` + `AcquirePermission`） | 1, 2, 3, 10(B) |
| `oldInstanceCircuitBreakerCaller/consumer` | Bare Report（`CircuitBreakerAPI.Report` + `UpdateServiceCallResult`） | 4, 10(C) |

三种调用模式的关键差异：

- **Decorator**：通过 `MakeFunctionDecorator` 将"熔断检查 + 业务调用 + 熔断上报"三步封装为单一 `DecoratorFunction`。customer func 内 `SetInstance` 让装饰器自动按 `InstanceResource` 上报。
- **InvokeHandler**：通过 `MakeInvokeHandler` 返回 `InvokeHandler`，暴露 `AcquirePermission / OnSuccess / OnError` 三个生命周期钩子，调用方手动编排。通过 `ResponseContext.Instance` 传入实例触发实例级上报。
- **Bare Report**：完全不使用装饰器或 InvokeHandler，自行调用 `CircuitBreakerAPI.Report` + `ConsumerAPI.UpdateServiceCallResult`。

用例 1/2/3 的 Decorator 和 InvokeHandler 子阶段**共用同一个 selfService 和熔断规则**（Decorator recover 后规则完全恢复，InvokeHandler 重新 trigger）。

## `.build/` 目录映射（SDK 日志排查入口）

每次 consumer / provider 启动时会在 `.build/<name>_run/` 下创建**独立的工作目录**，
内包含 Polaris SDK 配置 (`polaris.yaml`) 和运行时日志 (`polaris/log/`)。

### 目录内部结构

每个 `_run` 目录的结构统一（以 `instance_consumer_run` 为例）：

```
.build/instance_consumer_run/
├── polaris.yaml                              ← SDK 配置（含日志路径/插件/熔断规则）
└── polaris/log/                              ← SDK 运行时日志
    ├── auth/polaris-auth.log                 ← 鉴权日志
    ├── base/polaris.log                      ← 通用日志（版本号、连接状态）
    ├── cache/polaris-cache.log               ← 缓存日志
    ├── circuitbreaker/polaris-circuitbreaker.log ← 熔断器日志（核心排查日志）
    ├── lossless/polaris-lossless.log         ← 无损上下线日志
    ├── network/polaris-network.log           ← 网络日志
    ├── route/polaris-route.log               ← 路由日志
    └── statReport/polaris-statReport.log     ← 指标上报日志（prometheus push 模式用）
```

> **排查熔断问题时，优先看 `polaris-circuitbreaker.log`**（polaris-stat.log 已不再输出熔断日志）。
> 该日志包含规则加载（`ruleName`/`ruleID`/`ruleRev`）、计数器初始化、状态切换（`close → open → half-open → close`）等完整链路信息。

### 各用例对应的 `.build/` 目录

| 用例 | 子阶段 | `.build/` 目录 | 说明 |
|------|--------|---------------|------|
| 公共 | provider-a | `provider_a_run/` | 默认返回 200，通过 `/switch` 控制行为 |
| 公共 | provider-b | `provider_b_run/` | 默认返回 500，通过 `/switch` 控制行为 |
| 1 | Decorator | `instance_consumer_run/` | INSTANCE 级熔断，装饰器模式 |
| 1 | InvokeHandler | `instance_invoke_consumer_run/` | 与 Decorator 共用 selfService + 规则 |
| 2 | Decorator | `service_consumer_run/` | SERVICE 级熔断，装饰器模式 |
| 2 | InvokeHandler | `service_invoke_consumer_run/` | 与 Decorator 共用 selfService + 规则 |
| 2 | fallback 正向 | `service_fallback_consumer_run/` | fallbackConfig.enable=true → HTTP 599 |
| 2 | fallback 反向 | `service_fallback_off_consumer_run/` | fallbackConfig.enable=false → 503 兜底 |
| 3 | Decorator | `interface_consumer_run/` | METHOD 级熔断 + /echo /order /info /slow |
| 3 | InvokeHandler | `interface_invoke_consumer_run/` | 只验证 /echo 路径 |
| 3 | fallback 正向 | `interface_fallback_consumer_run/` | fallbackConfig.enable=true → HTTP 599 |
| 3 | fallback 反向 | `interface_fallback_off_consumer_run/` | fallbackConfig.enable=false → 503 兜底 |
| 4 | - | `old_instance_consumer_run/` | 旧版散装 API（向后兼容验证） |
| 5 | A/B/C 段 | `http_status_consumer_run/` | 三个子阶段共用同一 consumer |
| 6 | - | `default_rule_consumer_run/` | 服务端无规则，依赖 SDK 本地默认规则 |
| 7 | - | `modify_rule_consumer_run/` | 规则参数热修改验证（阈值 3→7） |
| 8 | - | `pm_consumer_run/` | 协议/HTTP 方法维度合并（13 BlockConfig） |
| 9 | - | `pathtype_consumer_run/` | 5 种 MatchString 路径匹配方式 |
| 10 | A Decorator | `decorator_consumer_run/` | 第 1 次启动：enableRecoverAll=true |
| 10 | A Decorator | `decorator_consumer_b_run/` | 第 2 次启动：enableRecoverAll=false |
| 10 | A Decorator | `decorator_consumer_c_run/` | 第 3 次启动：恢复 enableRecoverAll=true |
| 10 | B InvokeHandler | `invoke_consumer_run/` | 第 1 次启动：enableRecoverAll=true |
| 10 | B InvokeHandler | `invoke_consumer_b_run/` | 第 2 次启动：enableRecoverAll=false |
| 10 | B InvokeHandler | `invoke_consumer_c_run/` | 第 3 次启动：恢复 enableRecoverAll=true |
| 10 | C Bare Report | `report_consumer_run/` | 第 1 次启动：enableRecoverAll=true |
| 10 | C Bare Report | `report_consumer_b_run/` | 第 2 次启动：enableRecoverAll=false |
| 10 | C Bare Report | `report_consumer_c_run/` | 第 3 次启动：恢复 enableRecoverAll=true |

> **用例 10** 三个子阶段各重启 consumer 三次（enableRecoverAll=true → false → true），
> `_run` 为第一次启动（全死全活开启），`_b_run` 为第二次启动（全死全活关闭），
> `_c_run` 为第三次启动（恢复全死全活开启）。
>
> **用例 1/2/3/4** 的可观测性验证（metrics curl + events 解析）内嵌在各用例的
> `stop_consumer` 之前，**不产生新的 `_run` 目录**——consumer 启动时通过 yaml
> 同时配 prometheus pull + pushgateway mock，一次熔断 trigger 同时产出 gauge + event。

### 排查示例

```bash
# 查看用例 5 B 段失败时 SDK 的熔断规则加载和状态切换
grep -E "rule|counter|OPEN|close|half" \
  .build/http_status_consumer_run/polaris/log/circuitbreaker/polaris-circuitbreaker.log

# 查看用例 10 Decorator 子阶段全死全活关闭时的实例选择
grep -E "instance|healthy|isolated|fallback" \
  .build/decorator_consumer_b_run/polaris/log/base/polaris.log

# 查看 provider 端日志
cat .build/provider_b_run/polaris/log/base/polaris.log
```



## Provider 暴露的接口

| 路径    | 默认行为                | 控制开关                                      | 用途                                        |
|---------|-------------------------|-----------------------------------------------|---------------------------------------------|
| `/echo` | a=200, b=500            | `/switch?openError=true\|false`               | 用例 1/2/3 主链路                           |
| `/order`| a=200, b=200            | `/switch?openErrorOrder=true\|false`          | 用例 3 多接口隔离对比                       |
| `/info` | 恒 500                  | 无                                            | 用例 3 验证"未配置规则的接口不会被熔断"     |
| `/slow` | 200，sleep `slowDelayMs`| `/switch?slowDelayMs=<int>`                   | 用例 3 验证"错误判断条件支持时延"           |
| `/switch`| 200                    | `openError` / `openErrorOrder` / `slowDelayMs`| 运行时切换上述行为                          |
| `/health`| 恒 200                 | 无                                            | 启动探针                                    |
| `/api/` | a=200, b=500（随 needErr）| 同 `/switch?openError`                    | catch-all 覆盖 `/api/protocol/*` / `/api/method/*` / `/api/pathtype/*` 等路径 |

## 熔断规则清单

共 **8 条持久化规则**，按 Level 和用例分组：

| 规则名 | Level | source.service | 用例 |
|--------|-------|----------------|------|
| cb-instance-CircuitBreakerInstanceCaller | INSTANCE | CircuitBreakerInstanceCaller | 1 Decorator + InvokeHandler |
| cb-service-CircuitBreakerServiceCaller | SERVICE | CircuitBreakerServiceCaller | 2 Decorator + InvokeHandler |
| cb-interface-CircuitBreakerInterfaceCaller | METHOD | CircuitBreakerInterfaceCaller | 3 Decorator + InvokeHandler |
| cb-instance-CircuitBreakerOldInstanceCaller | INSTANCE | CircuitBreakerOldInstanceCaller | 4 |
| cb-instance-CircuitBreakerHttpStatusCaller | INSTANCE | CircuitBreakerHttpStatusCaller | 5 A/B + 10（共用同名规则） |
| cb-service-CircuitBreakerHttpStatusCaller | SERVICE | CircuitBreakerHttpStatusCaller | 5 C |
| cb-instance-CircuitBreakerModifyRuleCaller | INSTANCE | CircuitBreakerModifyRuleCaller | 7 |
| cb-proto-meth-CircuitBreakerPMCaller | METHOD | CircuitBreakerPMCaller | 8 |
| cb-pathtype-CircuitBreakerPathTypeCaller | METHOD | CircuitBreakerPathTypeCaller | 9 |

> 用例 6 不创建规则（验证 SDK 本地默认规则），不计入。
> 用例 10 复用 `cb-instance-CircuitBreakerHttpStatusCaller`（`source=*/*`），与用例 5 的 INSTANCE 规则同名。首次创建时以先运行到的用例为准（用例 5 或用例 10），后运行的通过幂等创建（code=200001）复用已有规则。
> 用例 10 的规则 `source=*/*`（通配所有 caller），与用例 5 的 `source.service=CircuitBreakerHttpStatusCaller` 不同。若先运行用例 10，则用例 5 的规则会被覆盖为 `source=*/*`，不影响用例 5 的验证（用例 5 的 caller 仍在匹配范围内）。

## 公共流程

每次运行 `verify_circuitbreaker.sh`：

1. 步骤 A：环境准备（检查 Go/python3/curl，创建 `.build/`、`.logs/`，生成规则模板 `_gen_rule.py`）
2. 步骤 B：启动 provider-a + provider-b
3. 步骤 C：依次执行用例 1-10
   - 默认 `RUN_CASES=instance,service,interface,old_instance,http_status,default_rule,modify_rule,protocol_method,pathtype,all_dead_fallback`，可通过 `--only` 缩小范围
   - 每个用例开始时打印结构化 `print_block` 概览（Caller 写法 / 规则配置 / 验证步骤 / 预期结果 / 判定标准）
   - 创建规则前会通过 `inspect_caller_rules` + `inspect_callee_rules` 巡检主调与被调维度的现有熔断规则
     - 默认仅 WARN，列出陌生规则
     - `STRICT_RULE_CHECK=true` 时遇到陌生规则直接 FAIL（避免规则污染）
4. 步骤 D：结果汇总，trap EXIT 时自动删除创建的熔断规则并停止进程

---

## 用例 1：实例级（INSTANCE）熔断

### Caller 写法
- **Decorator 子阶段**：`newCircuitBreakerCaller/consumer`，`MakeFunctionDecorator` + `RequestContext.Method=/echo` + `SetInstance`
- **InvokeHandler 子阶段**：`invokeHandlerCaller/consumer`，`MakeInvokeHandler` + `AcquirePermission` + `OnSuccess/OnError`
- 两个子阶段共用 selfService=`CircuitBreakerInstanceCaller`，Decorator 端口 18081，InvokeHandler 端口 18091

### 规则
- `level=INSTANCE`，`name=cb-instance-CircuitBreakerInstanceCaller`
- `rule_matcher.source.service=CircuitBreakerInstanceCaller`
- `rule_matcher.destination.service=CircuitBreakerCallee`
- `block_configs[0]`：
  - `error_conditions`：`RET_CODE RANGE 500~599`（4xx 不计入熔断）
  - `trigger_conditions`：`CONSECUTIVE_ERROR=5` + `ERROR_RATE 50%@30s, minRequest=10`
- `recover_condition.sleep_window=6s`，`consecutiveSuccess=1`

### 验证步骤
- 1.1 复位 provider：a=200，b=500
- 1.2 启动 instance consumer
- 1.3 创建/更新规则
- ── Decorator 第 1 轮 ──
- 1.4 触发：连发 `INSTANCE_R1_TRIGGER_COUNT=30` 次 `/echo`，让 b 累计 5 次失败 → 实例 b 被摘除
- 1.5 验证：再发 `RECOVERY_REQUEST_COUNT`（默认 10）次，流量应全部走 a
- 1.6 恢复：b 翻回 200，等 `WAIT_HALF_OPEN_SECONDS`（默认 8s）进半开 → b 重新加入 LB
- ── Decorator 第 2 轮 ──
- 1.7 再次触发：b 重新置 500，发 `INSTANCE_R2_TRIGGER_COUNT=30` 次
- 1.8 再次验证：流量再次全部走 a
- 1.9 再次恢复：b 翻回 200
- ── InvokeHandler 子阶段（复用 Decorator 的规则）──
- 1.10 启动 invokeHandler consumer（selfService 同 Decorator）
- 1.11 触发：b=500，发 `INSTANCE_R1_TRIGGER_COUNT=30` 次
- 1.12 验证：再发 10 次，流量全部走 a
- 1.13 恢复：b 翻回 200，等 8s

### 通过条件（共 9 项指标）
- Decorator 两轮 `trigger_fail >= 1`
- Decorator 两轮 `verify_ok == RECOVERY_REQUEST_COUNT`
- Decorator 两轮 `recover_ok == RECOVERY_REQUEST_COUNT`
- InvokeHandler `trigger_fail >= 1`，`verify_ok == RECOVERY_REQUEST_COUNT`，`recover_ok == RECOVERY_REQUEST_COUNT`

### 验证原理
- **为什么实例级熔断"不 abort 而是流量转移"**：`commonCheck` 只对 `ServiceResource` / `MethodResource` 调 `Check`（`circuitbreaker_flow.go:244`），**INSTANCE 级资源不参与请求前检查**，因此实例 b 被熔断 OPEN 后请求并不会被拒绝（无 `call aborted`），而是由路由/负载均衡层把 b 从可选实例集中摘除，流量自然全部落到健康实例 a。这正是 1.5 期望 `verify_ok == RECOVERY_REQUEST_COUNT`（全 200）而非 abort 的根因。
- **上报路径**：装饰器内 `SetInstance` 回填具体实例后，`commonReport` 追加一次 `InstanceResource` 上报（`circuitbreaker_flow.go:326`），由 b 的连续 5 次 5xx 推动该实例计数器 `close → open`。
- **半开恢复**：`open` 后经 `sleepWindow`(6s) 延迟转 `half-open`（`counter.go:156`），`half-open` 态发放 `consecutiveSuccess=1` 个探测配额；b 翻回 200 后探测成功即 `half-open → close`（`counter.go:189`），b 重新进入可选实例集。
- **4xx 不计入**：`error_conditions` 配 `RET_CODE RANGE 500~599`，`blockCounter.parseRetStatus`（`block_counter.go:106`）只把 5xx 判为 `RetFail`，4xx 透传为成功，故不会因客户端错误误触发熔断。

---

## 用例 2：服务级（SERVICE）熔断

### Caller 写法
- **Decorator 子阶段**：同 `newCircuitBreakerCaller/consumer`
- **InvokeHandler 子阶段**：`invokeHandlerCaller/consumer`
- 两个子阶段共用 selfService=`CircuitBreakerServiceCaller`，Decorator 端口 18082，InvokeHandler 端口 18092
- 装饰器内 `commonCheck/commonReport` 会按 `ServiceResource` 上报 → 触发 SERVICE 级熔断

### 规则
- `level=SERVICE`，`name=cb-service-CircuitBreakerServiceCaller`
- 其他字段同用例 1

### 验证步骤
- 2.1 复位 provider：a=500，b=500（模拟整服务不可用）
- 2.2 启动 service consumer
- 2.3 创建/更新规则
- ── Decorator 第 1 轮 ──
- 2.4 触发：连发 `TRIGGER_REQUEST_COUNT` 次累计错误率达阈值
- 2.5 验证：再发 `RECOVERY_REQUEST_COUNT` 次，期望出现 `call aborted`
- 2.6 恢复：a/b 都翻回 200，等 `WAIT_HALF_OPEN_SECONDS` → 半开探测一次成功 → 关闭
- ── Decorator 第 2 轮 ──
- 2.7 再置 a/b=500 → 再次熔断
- 2.8 再次出现 abort
- 2.9 再次翻 200 → 再次恢复
- ── InvokeHandler 子阶段（复用 Decorator 的规则）──
- 2.10 启动 invokeHandler consumer
- 2.11 触发：a/b=500，发 `TRIGGER_REQUEST_COUNT` 次
- 2.12 验证：再发 `RECOVERY_REQUEST_COUNT` 次，期望出现 abort
- 2.13 恢复：a/b 翻回 200，等 8s
- ── fallback 子阶段（复用 Decorator 的规则 cb-service-CircuitBreakerServiceCaller，PUT 更新规则切换 fallbackConfig.enable）──
- 2.14 PUT 更新规则打开 `fallbackConfig.enable=true`（code=599 / body="degraded by circuitbreaker" / header X-Fallback=true）
- 2.15 重启 consumer（规则 revision 变更触发 SDK counter 重建，需重新加载）
- 2.16 正向触发：a/b=500，发 `TRIGGER_REQUEST_COUNT` 次 `/echo`
- 2.17 正向验证：再发 `RECOVERY_REQUEST_COUNT` 次，期望 `CASE_FALLBACK >= 1`（HTTP 599 + body "degraded"）
- 2.18 PUT 更新规则关闭 `fallbackConfig.enable=false`（验证 enable 短路）
- 2.19 重启 consumer
- 2.20 反向触发：a/b=500，发 `TRIGGER_REQUEST_COUNT` 次
- 2.21 反向验证：再发 `RECOVERY_REQUEST_COUNT` 次，期望 `CASE_ABORT >= 1`（503，enable=false 不下发 599）

### 通过条件（共 11 项指标）
- Decorator 两轮 `trigger_fail >= 1`
- Decorator 两轮 `verify_abort >= 1`
- Decorator 两轮 `recover_ok >= 1`
- InvokeHandler `trigger_fail >= 1`，`verify_abort >= 1`，`recover_ok >= 1`
- fallback 正向 `svc_fallback >= 1`（enable=true → HTTP 599）
- fallback 反向 `svc_fb_off_abort >= 1`（enable=false → 503 兜底）

### 验证原理
- **为什么服务级熔断"会 abort"**：`commonCheck` 第一步就对 `ServiceResource` 调 `Check`（`circuitbreaker_flow.go:249`），服务级计数器 OPEN 后 `CheckResult.Pass=false`，请求在调用业务前即被拦截返回 `call aborted`。这与用例 1（实例级不进 `Check`）形成对照——所以 2.1 复位为 **a/b 都 500**（整服务不可用），让服务级错误率达阈值。
- **半开放行业务请求探活**：服务级 `half-open` 态由 `AcquirePermission` 发放有限配额（`half_open_status.go`，CAS 原子计数确保并发不超额），获得配额的业务请求落到已翻回 200 的 provider 即上报成功，`Release` 归集判定连续成功达 `consecutiveSuccess=1` 触发 `half-open → close`（`counter.go:217` `handleHalfOpenReport`）。因此 2.6 恢复阶段必须 **a/b 同步翻回 200**，否则半开探测可能打到仍 500 的实例而回 OPEN。
- **fallback 降级**：规则顶层 `fallbackConfig.enable=true` 时，`buildFallbackInfo` 把规则配置的 code/body/headers 封装进 `CallAborted`，demo 端 `HasFallback()` 为真则透传降级响应（599 + "degraded"）而非默认 503；`enable=false` 时 `GetEnable()` 短路，`CallAborted` 不带 fallback，demo 回默认 503 兜底。

---

## 用例 3：接口级（METHOD）熔断 + 多接口隔离 + DELAY + 多 MatchString

### Caller 写法
- **Decorator 子阶段**：`newCircuitBreakerCaller/consumer`，暴露 4 个端点 `/echo /order /info /slow`
- **InvokeHandler 子阶段**：`invokeHandlerCaller/consumer`，只验证 `/echo` 路径
- 两个子阶段共用 selfService=`CircuitBreakerInterfaceCaller`，Decorator 端口 18083，InvokeHandler 端口 18093

### 规则（1 条 METHOD 级规则，3 个 BlockConfig）
- `name=cb-interface-CircuitBreakerInterfaceCaller`
- `/echo` block：
  - `api.path=/echo` (EXACT)
  - `error_conditions`：5 条 RET_CODE 同时挂，覆盖 `RANGE / EXACT / REGEX / IN / NOT_IN`
  - `trigger`: `CONSECUTIVE_ERROR=5 / ERROR_RATE=50%@30s, minReq=10`
- `/order` block：
  - `api.path=/order` (EXACT)
  - 阈值故意调高：`CONSECUTIVE_ERROR=100 / ERROR_RATE=99%@30s, minReq=200`
- `/slow` block：
  - `api.path=/slow` (EXACT)
  - `error_conditions`：`input_type=DELAY, value=200`（毫秒）
- `/info` 无规则 + consumer 侧 `defaultRuleEnable=false`

### 验证步骤
- 3.1 复位 provider：a/b 的 `/echo /order` 都 500；`/info` 恒 500；`/slow` 默认 0ms
- 3.2 启动 interface consumer
- 3.3 创建规则
- ── /echo Decorator 第 1 轮 ──
- 3.4 触发：发 `TRIGGER_REQUEST_COUNT` 次 `/echo` 累计 5 次失败
- 3.5 验证：`RECOVERY_REQUEST_COUNT` 次 `/echo` 应出现 abort
- 3.6 恢复：a/b 的 `/echo` 翻 200，等 `WAIT_HALF_OPEN_SECONDS`
- ── /echo Decorator 第 2 轮 ──
- 3.7 再置 500 → 再次触发熔断
- 3.8 再次 abort
- 3.9 再次翻 200 → 再次恢复
- ── 多接口隔离 ──
- 3.10 `/order`：发 `TRIGGER_REQUEST_COUNT` 次，应全部失败但无 abort
- 3.11 `/info`：发 `TRIGGER_REQUEST_COUNT` 次，应全部失败但无 abort
- ── /slow DELAY 熔断 ──
- 3.12 触发：`/slow` 延迟设 500ms，发 `TRIGGER_REQUEST_COUNT` 次
- 3.13 验证：再发 `RECOVERY_REQUEST_COUNT` 次，应出现 abort
- 3.14 恢复：延迟清零，等 `WAIT_HALF_OPEN_SECONDS` → 全部 200
- ── InvokeHandler 子阶段（复用 Decorator 的规则，只验证 /echo）──
- 3.15 启动 invokeHandler consumer
- 3.16 触发：a/b=500，发 `TRIGGER_REQUEST_COUNT` 次到 `/echo`
- 3.17 验证：再发 `RECOVERY_REQUEST_COUNT` 次，期望出现 abort
- 3.18 恢复：a/b 翻回 200，等 8s
- ── fallback 子阶段（复用 Decorator 的 merged 规则 cb-interface-CircuitBreakerInterfaceCaller，PUT 更新规则切换 fallbackConfig.enable）──
- 3.19 PUT 更新规则打开 `fallbackConfig.enable=true`（code=599 / body="degraded by circuitbreaker"）
- 3.20 重启 consumer（规则 revision 变更触发 SDK counter 重建，需重新加载）
- 3.21 正向触发：a/b=500，发 `TRIGGER_REQUEST_COUNT` 次到 `/echo`
- 3.22 正向验证：再发 `RECOVERY_REQUEST_COUNT` 次，期望 `CASE_FALLBACK >= 1`（HTTP 599 + body "degraded"，fallbackConfig 为 Rule 级字段，三个 block 共享）
- 3.23 PUT 更新规则关闭 `fallbackConfig.enable=false`（验证 enable 短路）
- 3.24 重启 consumer
- 3.25 反向触发：a/b=500，发 `TRIGGER_REQUEST_COUNT` 次到 `/echo`
- 3.26 反向验证：再发 `RECOVERY_REQUEST_COUNT` 次，期望 `CASE_ABORT >= 1`（503，enable=false 不下发 599）

### 通过条件（共 16 项指标）
- `/echo` Decorator 两轮 trigger ≥1 fail / verify ≥1 abort / recover 全 200
- `/order` 整批失败但 `abort == 0`
- `/info` 整批失败但 `abort == 0`
- `/slow` trigger 阶段成功完成 ≥1 / verify ≥1 abort / recover 全 200
- InvokeHandler trigger ≥1 fail / verify ≥1 abort / recover ≥1 ok
- fallback 正向 `iface_fallback >= 1`（enable=true → HTTP 599）
- fallback 反向 `iface_fb_off_abort >= 1`（enable=false → 503 兜底）

### 验证原理
- **多 BlockConfig 按 path 选中**：1 条 METHOD 级规则带 3 个 BlockConfig，请求进来时 `selectCircuitBreakerRule`（`rule.go:201`）先按 Level/source/destination 选中规则，再由 `matchRuleAPI`（`rule.go:268`）遍历 BlockConfig 用 `api.protocol/method/path` 匹配选中对应块；上报时 `dispatchToBlocks`（`counter.go:250`）只把统计派发给 `matchAPI` 命中的块。这保证 `/echo`、`/order`、`/slow` 三个接口的熔断计数器**彼此独立、互不串扰**——这就是"多接口隔离"的实现基础。
- **未配置规则的接口不被熔断**：`/info` 没有对应 BlockConfig，且 consumer 侧 `defaultRuleEnable=false`，故即使 `/info` 恒 500 也不会触发任何计数器（3.11 期望 `abort == 0`）。
- **阈值隔离**：`/order` 故意配 `CONSECUTIVE_ERROR=100 + minRequest=200`，普通 burst 量级永远达不到，验证"高阈值接口在同一规则下不会被低阈值接口的失败带崩"。
- **DELAY 时延熔断**：`/slow` 块的 `error_conditions` 用 `INPUT_TYPE=DELAY, value=200`，`parseRetStatus`（`block_counter.go:128`）把 `stat.Delay.Milliseconds() > 200` 判为 `RetTimeout`（失败），因此 3.12 把延迟设 500ms 即可触发——验证"错误判定不止看返回码，还支持时延维度"。
- **多 MatchString**：`/echo` 块同时挂 `RANGE/EXACT/REGEX/IN/NOT_IN` 5 种 RET_CODE 条件，验证 `match.MatchString` 5 种匹配语义在同一块内都能正确识别 5xx。

---

## 用例 4：存量散装写法（旧版 API）的实例级熔断

### Caller 写法
- 旧版散装路径，源码：`oldInstanceCircuitBreakerCaller/consumer/main.go`
- 直接调用 `CircuitBreakerAPI.Report(InstanceResource)` → 上报熔断结果
- 直接调用 `ConsumerAPI.UpdateServiceCallResult` → 上报调用结果指标
- **不**使用 `MakeFunctionDecorator / RequestContext / SetInstance`
- selfService=`CircuitBreakerOldInstanceCaller`，端口 18084

### 验证目的
- 保证存量客户在新版 SDK 中沿用旧版散装写法依旧能触发实例级熔断
- 即「向后兼容」语义不被破坏（与用例 1 行为对齐）

### 规则
- `level=INSTANCE`，`name=cb-instance-CircuitBreakerOldInstanceCaller`
- 触发/恢复条件与用例 1 完全一致：`CONSECUTIVE_ERROR=5`

### 验证步骤
- 4.1 复位 provider：a=200，b=500
- 4.2 启动 old-instance consumer
- 4.3 创建/更新规则
- ── 第 1 轮 ──
- 4.4 触发：连发 `OLD_INSTANCE_R1_TRIGGER_COUNT=30` 次 `/echo`
- 4.5 验证：再发 10 次，流量应全部走 a
- 4.6 恢复：b 翻回 200，等 15s
- ── 第 2 轮 ──
- 4.7 再次触发：b 重新置 500，发 `OLD_INSTANCE_R2_TRIGGER_COUNT=30` 次
- 4.8 再次验证：流量再次全部走 a
- 4.9 再次恢复：b 翻回 200

### 通过条件（共 6 项指标，与用例 1 对齐）
- 两轮 `trigger_fail >= 1`
- 两轮 `verify_ok == RECOVERY_REQUEST_COUNT`
- 两轮 `recover_ok == RECOVERY_REQUEST_COUNT`

### 验证原理
- **散装写法等价于装饰器**：装饰器（用例 1）本质是把 `Check → 业务调用 → Report` 三步封装。旧版直接调 `CircuitBreakerAPI.Report(InstanceResource)` 上报熔断结果 + `ConsumerAPI.UpdateServiceCallResult` 上报指标，绕过装饰器但触达的是**同一套 `InstanceResource` 计数器**（`counter.go` 的 `Report`/`dispatchToBlocks`）。
- **结果与用例 1 完全对齐**正说明：新版 SDK 重构装饰器/InvokeHandler 时没有破坏底层实例级熔断与半开恢复链路，存量客户零改造即可继续工作（向后兼容）。

---

## 用例 5：HTTP 状态码区分（4xx 不熔断 / 5xx 熔断 / 网络错熔断）

### Caller 写法
- 同 `newCircuitBreakerCaller/consumer`，selfService=`CircuitBreakerHttpStatusCaller`，端口 18085
- 装饰器内部按 HTTP 状态码分流：5xx → OnError（retCode="-1"），4xx → OnSuccess（透传真实状态码）

### 规则
- A/B 段：`level=INSTANCE`，`name=cb-instance-CircuitBreakerHttpStatusCaller`，`CONSECUTIVE_ERROR=3`
- C 段：`level=SERVICE`，`name=cb-service-CircuitBreakerHttpStatusCaller`，`CONSECUTIVE_ERROR=3`

### 验证步骤
- 5.1 复位 provider：a=200 / b=200
- 5.2 启动 http_status consumer
- 5.3 创建 INSTANCE 级规则
- ── A 段：4xx 不熔断 ──
- 5.4 连发 `TRIGGER_REQUEST_COUNT` 次 `/forbidden`（provider 永远返回 403）
- ── B 段：5xx 熔断 ──
- 5.5 provider-b=500，连发 `TRIGGER_REQUEST_COUNT` 次 `/echo` → b 累计 3 次 5xx 触发熔断
- 5.6 验证：再发 `RECOVERY_REQUEST_COUNT` 次 `/echo`，abort 或路由到 a，总计 = RECOVERY_REQUEST_COUNT
- 5.7 恢复：provider-b=200，等 `WAIT_HALF_OPEN_SECONDS`
- ── 规则切换（INSTANCE → SERVICE） ──
- 5.(7→8) 删除 INSTANCE 级规则，创建 SERVICE 级规则
- ── C 段：网络错熔断（-1 哨兵） ──
- 5.8 关停 provider-a / provider-b 制造网络错
- 5.9 连发 `TRIGGER_REQUEST_COUNT` 次 `/echo` → SDK retCode="-1" 触发 SERVICE 级熔断
- 5.10 provider 重启由主 shell 接管

### 通过条件（3 段独立判定）
- A 段：`a_fail ≥ 1` 且 `a_abort == 0`（4xx 全部 fail 但永不熔断）
- B 段：`b_trigger_fail ≥ 3` 且 `b_verify_ok + b_verify_abort == RECOVERY_REQUEST_COUNT` 且 `b_recover_ok == RECOVERY_REQUEST_COUNT`
- C 段：`c_fail ≥ 1` 且 `c_abort ≥ 1`（网络错触发熔断）

### 验证原理
- **retCode 双路径是关键**：熔断计数器看的是 `stat.RetCode`，而它由 demo 端按 HTTP 状态码分流决定——5xx 走 `OnError` 设 `retCode="-1"`（或 5xx 码），4xx 走 `OnSuccess` 透传真实码。`parseRetStatus`（`block_counter.go:106`）只对 `RET_CODE RANGE 500~599` 命中的码判 `RetFail`。
  - **A 段（4xx 不熔断）**：403 走 OnSuccess、码=403 不在 500~599 区间 → 判成功 → 计数器永不 OPEN。验证"客户端错误不应触发服务端熔断"。
  - **B 段（5xx 熔断）**：500 走 OnError → 累计 3 次连续失败触发 INSTANCE 级 OPEN。
  - **C 段（网络错熔断）**：provider 关停后请求根本发不出去（连接失败），SDK 内部用 `retCode="-1"` 哨兵标记；`parseRetStatus`（`block_counter.go:124`）对 `"-1"` **直接判 `RetFail` 而不经 MatchString**，因此网络错也能触发熔断。C 段用 SERVICE 级规则是因为实例全死时 INSTANCE 级会因实例剔除而 `GetOneInstance` 直接失败、难以稳定复现熔断态。

---

## 用例 6：默认实例级熔断兜底（服务端无规则）

### Caller 写法
- 同 `newCircuitBreakerCaller/consumer`，selfService=`CircuitBreakerDefaultRuleCaller`，端口 18086
- consumer 启动时通过 `polaris.yaml` 启用 `defaultRuleEnable: true`

### 默认规则（由 SDK 本地生成）
- `level=INSTANCE`，`name=default-polaris-instance-circuit-breaker`
- `CONSECUTIVE_ERROR=3` + `ERROR_RATE 50%@30s, minRequest=3`（脚本侧 yaml 收紧阈值）

### 与用例 1 的根本区别
- **不向服务端创建任何规则**；不调用 `create_circuitbreaker_rule`
- selfService 独立，避免被其它用例的规则误命中

### 验证步骤
- 6.1 复位 provider：a=200 / b=500
- 6.2 启动 default-rule consumer
- 6.3 跳过规则创建
- 6.4 触发：连发 `DEFAULT_RULE_TRIGGER_COUNT=30` 次 → b 累计 3 次 5xx 触发熔断
- 6.5 验证：再发 `RECOVERY_REQUEST_COUNT` 次，流量应全部走 a → 200
- 6.6 恢复：b 翻回 200，等 `WAIT_HALF_OPEN_SECONDS`

### 通过条件
- `trigger_fail ≥ 3`
- `verify_ok == RECOVERY_REQUEST_COUNT`
- `recover_ok == RECOVERY_REQUEST_COUNT`

### 验证原理
- **默认规则注入时机**：当 `getCircuitBreakerRule`（`default.go:51`）先查服务端规则 miss、且 `defaultRuleEnable=true`、且当前是 INSTANCE 级时，SDK 本地构造 `default-polaris-instance-circuit-breaker` 规则兜底。
- **`hasAnyEnabledRule` 拦截**（`default.go:139`）：注入前会全局检查——若该服务下存在任意 enabled 且匹配当前 caller 的服务端规则（SERVICE/METHOD/INSTANCE 任一级），则**不注入**默认规则（避免与服务端规则冲突）。本用例特意用独立 selfService（`CircuitBreakerDefaultRuleCaller`）就是为了不被其他用例的规则命中，确保走默认规则路径。
- **行为与用例 1 一致**：默认规则同样是 INSTANCE 级（`error_conditions=RET_CODE 500~599`，trigger=ERROR_RATE+CONSECUTIVE_ERROR），故 b 被熔断后流量转移到 a（不 abort），半开恢复机制与用例 1 相同。

---

## 用例 7：修改熔断参数生效（CONSECUTIVE_ERROR=3 → 7）

### Caller 写法
- 同 `newCircuitBreakerCaller/consumer`，selfService=`CircuitBreakerModifyRuleCaller`，端口 18087

### 规则
- `level=INSTANCE`，`name=cb-instance-CircuitBreakerModifyRuleCaller`
- `CONSECUTIVE_ERROR` 阈值在两轮间变化：轮 1=3，轮 2=7

### 验证步骤
- 7.1 复位 provider：a=200 / b=500
- 7.2 启动 modify_rule consumer
- 7.3 创建规则（`CONSECUTIVE_ERROR=3`）
- ── 第 1 轮（CONSECUTIVE_ERROR=3） ──
- 7.4-7.6 trigger → verify → recover
- ── 规则更新 ──
- 7.7 调用 `update_circuitbreaker_rule` API 将 `CONSECUTIVE_ERROR` 改为 7
- 7.8 等待 `WAIT_RULE_READY_SECONDS`
- ── 第 2 轮（CONSECUTIVE_ERROR=7） ──
- 7.9 触发：发 `MODIFY_R2_TRIGGER_COUNT=30` 次
- 7.10-7.11 verify → recover

### 通过条件（共 6 项指标）
- 两轮 `trigger_fail >= 1`
- 两轮 `verify_ok == RECOVERY_REQUEST_COUNT`
- 两轮 `recover_ok == RECOVERY_REQUEST_COUNT`

### 验证原理
- **规则热更新链路**：调 `update_circuitbreaker_rule` PUT 改 `CONSECUTIVE_ERROR` 后，服务端规则 `revision` 变更，SDK 通过规则变更事件感知并**重建对应资源计数器**（`counter.go`），新阈值立即生效，无需重启 consumer。
- **第 2 轮 burst 须加大**：轮 1 阈值 3、轮 2 阈值 7，但 50/50 LB 下 b 不一定连续被选中 7 次，故轮 2 用 case-local `MODIFY_R2_TRIGGER_COUNT=30` 保证连续命中 b 达 7 次（否则 trigger 不足导致假性 FAIL）。
- 验证目的：证明运行时通过控制台/OpenAPI 修改熔断阈值能动态生效，是熔断规则可运维性的核心能力。

---

## 用例 8：接口协议+HTTP方法维度合并（1 条规则 13 个 BlockConfig）

### Caller 写法
- 共享 `newCircuitBreakerCaller/consumer`
- consumer 端按 path 段推断 `RequestContext.Protocol` / `HTTPMethod`
- selfService=`CircuitBreakerPMCaller`，端口 18088

### 规则（1 条 METHOD 级规则，13 个 BlockConfig）
- `name=cb-proto-meth-CircuitBreakerPMCaller`
- 4 个协议 BC + 9 个 HTTP 方法 BC
- 每条 BC 共用 `CONSECUTIVE_ERROR=3`

### 验证步骤
对 13 个 BlockConfig 各跑 3 阶段：
- `trigger`：发 `PM_TRIGGER_COUNT=30` 次 → 触发对应 BC 熔断
- `verify`：再发 3 次 → 期望 abort 或 ok，总计 = 3
- `recover`：provider 翻回 200，等 8s → 再发 3 次 → 全 200

### 通过条件（共 13 × 3 = 39 项指标）
每个 BC：`trigger fail >= 1 || abort >= 1`，`verify ok + abort == 3`，`recover ok == 3`

### 验证原理
- **协议/方法维度独立匹配**：consumer 按请求路径段（如 `/api/protocol/grpc`、`/api/method/POST`）推断 `RequestContext.Protocol` / `HTTPMethod`，构造 `MethodResource` 时带上这两维。上报时 `matchAPI`（`block_counter.go:138`）按 `protocol/method/path` 组合匹配选中对应 BlockConfig，使 13 个维度（4 协议 + 9 HTTP 方法）的熔断计数器**彼此正交、互不干扰**。
- **验证目的**：一条规则即可对不同协议、不同 HTTP 方法分别配置独立熔断策略（如 gRPC 与 HTTP 分开熔断、GET 与 POST 分开熔断），无需为每个维度建独立规则。

---

## 用例 9：路径匹配方式维度（5 种 MatchString）

### Caller 写法
- 共享 `newCircuitBreakerCaller/consumer`
- selfService=`CircuitBreakerPathTypeCaller`，端口 18089

### 规则（1 条 METHOD 级规则，5 个 BlockConfig）
| path.type | path.value | 消费者触发 path |
|---|---|---|
| EXACT | `/api/pathtype/exact` | `/api/pathtype/exact` |
| REGEX | `^/api/pathtype/regex/.*` | `/api/pathtype/regex/abc` |
| NOT_EQUALS | `/api/pathtype/never_match_anything` | `/api/pathtype/something` |
| IN | `/api/pathtype/in1,/api/pathtype/in2` | `/api/pathtype/in1` |
| NOT_IN | `/api/pathtype/forbidden1,/api/pathtype/forbidden2` | `/api/pathtype/allowed` |

每条 BC 共用 `CONSECUTIVE_ERROR=3`。

### 验证步骤
对 5 种 MatchString 各跑正向 3 阶段：
- `trigger`：发 `PATHTYPE_TRIGGER_COUNT=30` 次 → 触发对应 BC 熔断
- `verify`：再发 3 次 → 期望 abort 或 ok，总计 = 3
- `recover`：provider 翻回 200，等 8s → 再发 3 次 → 全 200

### 为什么移除了反向验证？
5 种 path-type 已合并到 1 条规则的 5 个 BlockConfig。`NOT_EQUALS` 和 `NOT_IN` 否定匹配 BC 会吃掉几乎所有 path，不存在"对全部 5 个 BC 都不匹配"的反向 path。正向 3 阶段已充分证明 5 种 MatchString 各自的匹配语义。

### 验证原理
- **MatchString 5 种语义**：`api.path` 的 `type` 字段决定路径匹配方式，由 `match.MatchString` 统一实现——`EXACT`（全等）、`REGEX`（正则）、`NOT_EQUALS`（不等）、`IN`（在逗号分隔集合内）、`NOT_IN`（不在集合内）。consumer 对每种 path-type 发送一条**应命中**的请求路径（见上表"消费者触发 path"列），验证 `matchRuleAPI`（`rule.go:268`）能据此选中对应 BlockConfig 并触发熔断。
- 与用例 8 互补：用例 8 验证 protocol/method 维度匹配，用例 9 验证 path 这一维的 5 种字符串匹配算法都正确。

### 通过条件（共 5 × 3 = 15 项指标）
每种 path type：`trigger fail >= 1 || abort >= 1`，`verify ok + abort == 3`，`recover ok == 3`

---

## 用例 10：全死全活兜底策略验证（三种调用模式）

### 验证目的
验证当所有实例都被 INSTANCE 级熔断 OPEN 后，FilterOnlyRouter 的全死全活兜底策略：
- **全死全活开启**（`enableRecoverAll=true`，默认）：`GetOneInstance` 仍能选到 OPEN 实例，请求可透传至 provider
- **全死全活关闭**（`enableRecoverAll=false`）：`GetOneInstance` 直接失败

覆盖三种 SDK 调用模式，每种各跑一轮 enableRecoverAll 开启/关闭对比。

### Caller 写法
| 子阶段 | 源码 | 调用模式 | selfService | 端口 |
|--------|------|---------|-------------|------|
| A | `newCircuitBreakerCaller/consumer` | Decorator | CircuitBreakerAllDeadDecoratorCaller | 18090 |
| B | `invokeHandlerCaller/consumer` | InvokeHandler | CircuitBreakerAllDeadInvokeCaller | 18091 |
| C | `oldInstanceCircuitBreakerCaller/consumer` | Bare Report | CircuitBreakerAllDeadReportCaller | 18092 |

三个子阶段共用同一条规则 `cb-instance-CircuitBreakerHttpStatusCaller`（`source=*/*`，`CONSECUTIVE_ERROR=3`），与用例 5 的 INSTANCE 规则同名。

### 规则
- `level=INSTANCE`，`name=cb-instance-CircuitBreakerHttpStatusCaller`，`source=*/*`（通配所有 caller）
- `CONSECUTIVE_ERROR=3`（收紧阈值加快验证）
- `recover_condition.sleep_window=6s`，`consecutiveSuccess=1`

### 验证步骤（每个子阶段执行相同流程）
- ── 全死全活开启 ──
- .1 启动 consumer（`enableRecoverAll=true`）
- .2 创建规则
- .3 trigger：provider-a=500 / provider-b=500，发 60 次让两个实例都被熔断 OPEN
- .4 verify：provider 翻回 200，立即发 10 次 → 全死全活让请求透传到 provider → 返回 200
- .5 recover：等 sleepWindow 进入半开 → 探测成功恢复
- ── 全死全活关闭 ──
- .6 重启 consumer（`enableRecoverAll=false`）
- .7 trigger：重新触发熔断（a/b=500，发 60 次）
- .8 verify：provider 翻回 200，发 10 次 → 全死全活关闭 → GetOneInstance 失败 → 500
- .9 recover：重启 consumer（恢复 `enableRecoverAll=true`），等 sleepWindow

### 通过条件（每个子阶段 6 项指标，共 18 项）
- 全死全活开启：`trigger_fail >= 5`，`verify_ok >= 1`，`recover_ok >= 1`
- 全死全活关闭：`trigger_fail >= 5`，`verify_fail >= 1`，`recover_ok >= 1`

### 验证原理
- INSTANCE 级熔断不经过 `AcquirePermission`（只检查 SERVICE/METHOD 级），请求可透传至 provider
- 全死全活触发时 FilterOnlyRouter 设置 `HasLimitedInstances=true`，LB 从 `selectableInstancesWithoutUnhealthy` 中选择 OPEN 实例
- 全死全活关闭时 LB 从 `healthyInstances`（空集）中选择 → `GetOneInstance` 失败

---

## 熔断可观测性验证（嵌入用例 1/2/3/4，告知性）

可观测性验证**不是独立用例**，而是嵌入在用例 1/2/3/4 末尾的**告知性子步骤**（`stop_consumer` 前由 `_verify_observability` 触发）。不额外启动 consumer、不增规则、不影响核心熔断 PASS/FAIL。

### 设计思路
每个 consumer 启动时通过 yaml 同时配两种可观测能力：
- `statReporter.prometheus.type=pull` + `interval:5s`（SDK 启动独立 HTTP server 暴露 `/metrics`，端口=业务端口+`METRICS_PORT_OFFSET`=20000）
- `eventReporter.pushgateway.address=127.0.0.1:19091`（指向全局 1 个 `mock_event_server.py`）

一次熔断 trigger 同时产出 gauge + event，脚本在 `stop_consumer` 前 curl `/metrics` + 解析 `captured_cb_events.json` 完成验证。

### 嵌入点与对应关系

| 嵌入用例 | Level | consumer 名 | 业务端口 | metrics 端口 | rule_name |
|---------|-------|-------------|---------|------------|-----------|
| 用例 1 | INSTANCE | instance_consumer | 18081 | 38081 | `cb-instance-${INSTANCE_CALLER}` |
| 用例 2 | SERVICE | service_consumer | 18082 | 38082 | `cb-service-${SERVICE_CALLER}` |
| 用例 3 | METHOD | interface_consumer | 18083 | 38083 | `cb-interface-${INTERFACE_CALLER}` |
| 用例 4 | INSTANCE | old_instance_consumer | 18084 | 38084 | `cb-instance-${OLD_INSTANCE_CALLER}` |

### 全局基础设施
- 步骤 B 后启动 **1 个** `mock_event_server.py`（监听 `MOCK_CB_EVENT_PORT=19091`），按 `captured_cb_events.json` JSONL 捕获 pushgateway POST；cleanup 时停止。
- `OBSERV_ENABLE=true`（默认）控制开关，设为 false 时跳过全部可观测性子步骤。

### 验证步骤（每次嵌入执行相同流程，由 `_verify_observability` 统一驱动）
| 顺序 | 操作 | 函数 | 判定 |
|------|------|------|------|
| 1 | 确保 consumer 存活且 /metrics 可达（在 stop_consumer 前执行） | 调用方控制 | - |
| 2 | curl `/metrics` 轮询 `circuitbreaker_open`（5 轮 × 2s 间隔） | `_probe_cb_metric` | gauge 行存在且 `callee_service`/`rule_name` label 匹配 → OK |
| 3 | 解析 `captured_cb_events.json` JSONL | `_verify_cb_event` | 按 `rule_name`+`namespace`+`service` 统计 Open/Close 次数，Open≥1 → OK |
| 4 | 汇总输出 | `_verify_observability` | OK → log_info；失败 → log_warn（**告知性，不改 PASS/FAIL**） |

### 验证原理
- **一次 trigger 同时产出 gauge + event**：`counter.go` 的 `toOpen` 依次调 `reportCircuitBreakMetric → SyncReportStat` 和 `reportCircuitBreakerEvent → sendEvent`，两条链路独立异步、不互等。
- **指标链路**：`SyncReportStat` 写入 `circuitBreakerCollector` → `doAggregation`（`interval:5s`）写入 GaugeVec → `/metrics` 端点暴露 `circuitbreaker_open{...}` gauge。`circuitbreaker_open` 是 gauge（非 counter），Open 时为 1、HalfOpen 时 Dec() 归零，故判定标准为**指标行存在**而非 value>0。
- **事件链路**：`reportCircuitBreakerEvent → BuildCircuitBreakerEvent → sendEvent` 投递 EventReporter 链 → `PushgatewayReporter` 入队 → batch flush（默认 4s 间隔）POST 到 `mock_event_server.py`。
- **revision 永不过期**（`collector.go` `currentRevision>0` 守卫）：`circuitBreakerCollector` 传 `0` 跳过 revision 过期清零，gauge 值不被周期聚合清零。
- **metrics 端口偏移选择**：业务端口+10000 会撞 provider 端口（28081/28082），故用 `METRICS_PORT_OFFSET=20000`（38081-38084），避开所有已用端口。

---

## 主动探测用例（独立脚本 `verify_faultdetect.sh`）

> 本组用例不在 `verify_circuitbreaker.sh` 中，由独立脚本 `verify_faultdetect.sh` 执行，
> 验证 SDK 在熔断打开后**周期性主动探活下游实例并据此恢复**的能力。
> 完整说明见 [fault-detect.md](fault-detect.md)。

### 验证目的
验证熔断主动探测（Fault Detect / Active Health Check）在 **SERVICE / METHOD / INSTANCE 三个 HTTP 探测
级别 + TCP 探测 + UDP 探测共 5 个用例**的完整闭环。各用独立 caller + 独立 consumer 端口
（18095/18096/18097/18098/18099）+ 独立规则名（`cb-fd-<LEVEL>-<caller>` / `fd-fd-<LEVEL>-<caller>`），
由 `RUN_FD_CASES` 控制选跑（默认全跑：`service,method,instance,tcp,udp`）。

### 五个用例（共 10 条规则：各 1 熔断 + 1 探测）
| 用例 | 主调 caller | 被调服务 | consumer 端口 | 探测协议 | 熔断规则 level / method | 探测端口 / method |
| --- | --- | --- | --- | --- | --- | --- |
| 服务级(SERVICE)  | `CircuitBreakerFaultDetectCaller` | `CircuitBreakerFDSvcCallee` | 18095 | HTTP(1) | SERVICE / `*` | 实例端口 `port=0` / `/echo`（`*`） |
| 接口级(METHOD)   | `CircuitBreakerFDMethodCaller` | `CircuitBreakerFDMethodCallee` | 18096 | HTTP(1) | METHOD / `/echo`（BlockConfig.api.path=/echo） | 实例端口 `port=0` / `/echo` |
| 实例级(INSTANCE) | `CircuitBreakerFDInstanceCaller` | `CircuitBreakerFDInstanceCallee` | 18097 | HTTP(1) | INSTANCE / `*` | 实例端口 `port=0` / `/echo`（`*`） |
| TCP 探测(SERVICE) | `CircuitBreakerFDTcpCaller` | `CircuitBreakerFDTcpCallee` | 18098 | TCP(2) | SERVICE / `*` | provider-a `28091` / `send=ping,receive=tcp-ok` |
| UDP 探测(SERVICE) | `CircuitBreakerFDUdpCaller` | `CircuitBreakerFDUdpCallee` | 18099 | UDP(3) | SERVICE / `*` | provider-a `28101` / `send=ping,receive=udp-ok` |

熔断规则公共：`CONSECUTIVE_ERROR=5`，`sleepWindow=6s`，`consecutiveSuccess=1`，
**`faultDetectConfig.enable=true`**（探测门控），**仅 CONSECUTIVE_ERROR、不叠加 ERROR_RATE**（避免恢复抖动）。
HTTP 探测规则公共：`protocol=HTTP`(1)，`GET /echo`，`interval=2s`，`timeout=1000ms`，`port=0`（用实例端口）。
TCP/UDP 探测规则：`protocol=TCP`(2)/`UDP`(3)，用 `tcp_config`/`udp_config`（`{send,receive[]}`），
`interval=2s`，`timeout=1000ms`，`port` 固定指向 provider-a 的探测端口（TCP=28091 / UDP=28101）。
**五个用例各用独立被调服务**（provider-a 28081 / provider-b 28082 两进程通过 `--service` 逗号分隔同时注册
到这五个服务）。独立被调服务可避免被 verify_circuitbreaker.sh 残留的 `source=*/*` catch-all 规则按
id 字典序抢占导致探测门控失效（详见 fault-detect.md）。

> **TCP/UDP 只用单实例 provider-a**：`FaultDetectRule.port` 单值、`port>0` 时所有被探测实例共用同一端口号，
> 而同机两进程不能绑定同一 TCP/UDP 端口，故 TCP/UDP 探测端口仅 provider-a 启动（脚本只给 a 传
> `--tcp-probe-port`/`--udp-probe-port`）。SERVICE 级闭环不依赖多实例，单实例足够。

> 探测端点选 `/echo` 而非 `/health`：`/health` 固定 200 无法反映健康变化，会让半开态被立即拉回
> CLOSE；`/echo` 受 provider `needErr` 开关控制，挂时探测失败、恢复时探测成功，闭环可验证。

### 用例一/二：SERVICE / METHOD 级（abort 型，共用 `_run_probe_abort_case`）
都经 AcquirePermission，OPEN 时业务请求 abort。步骤：
1. 环境复位：provider-a/b 均 200
2. 启动 consumer + 下发熔断规则（带 `faultDetectConfig.enable=true`）+ 探测规则，等待就绪
3. 触发 OPEN：provider 全部置 500，burst `FD_TRIGGER_COUNT`(30) → 熔断打开（abort）
4. 维持 OPEN：保持 500，等 `WAIT_KEEP_OPEN_SECONDS`(12s) > sleepWindow，burst 验证仍 abort（探测 `/echo` 失败，半开回 OPEN）
5. 探活恢复：provider 全部置 200，等 `WAIT_RECOVER_SECONDS`(16s)，主动探测探活推动 CLOSE，burst 验证全 ok
6. 日志佐证：SDK 日志出现探测调度 + 状态切换记录

**通过条件**：`trigger_abort>=1` 且 `keep_abort>=1` 且 `recover_ok==FD_VERIFY_COUNT` 且 探测调度=yes 且 状态切换=yes。

### 用例三：INSTANCE 级（ok 型，单实例故障）
INSTANCE 级不经 AcquirePermission，OPEN 实例被路由层摘除、业务落其余健康实例（不 abort）。步骤：
1. 环境复位：a/b 均 200
2. 启动 consumer + 下发 INSTANCE 级熔断规则 + 探测规则，等待就绪
3. 触发 b OPEN：**仅 provider-b 置 500**（a 保持 200），burst 30 → 触发 b 实例熔断
4. 维持 OPEN：b 保持 500，等 12s，burst 验证业务落 a（ok）
5. 探活恢复：provider-b 置 200，等 16s，b 实例 half-open→close 重新可选，burst 验证全 ok
6. 日志佐证：探测调度 + **INSTANCE 级** half-open→close

**通过条件**：`trigger_fail>=1` 且 `keep_ok>=FD_VERIFY_COUNT-1` 且 `recover_ok>=FD_VERIFY_COUNT-1`
且 探测调度=yes 且 INSTANCE状态切换=yes（ok 容忍 1 次 LB 抖动）。

> INSTANCE 级恢复实测由 half-open 态业务请求探活推动（探测在 OPEN 期间也持续调度）；不用
> "provider-b 探测请求增量"作铁证（provider 三用例共享、计数串扰），以 INSTANCE 级 half-open→close 为铁证。

### 用例四/五：TCP / UDP 探测（abort 型，单实例 provider-a，共用 `_run_probe_tcpudp_case`）
均为 SERVICE 级熔断规则（abort 型），闭环结构与 HTTP SERVICE 用例一致，区别在探测协议与探测端口。步骤：
1. 环境复位：provider-a/b 的 `/echo` 置 200；TCP/UDP 探测端口正常回包
2. 启动 consumer（TCP=18098 / UDP=18099）+ 下发 SERVICE 级熔断规则（带 `faultDetectConfig.enable=true`）+
   TCP/UDP 探测规则（`protocol=2/3`，`tcp_config`/`udp_config`，`port` 指向 provider-a 的 28091/28101），等待就绪
3. 触发 OPEN：provider-a/b 的 `/echo` 置 500，burst `FD_TRIGGER_COUNT`(30) → 熔断打开（abort）
4. 维持 OPEN：`/echo` 保持 500 **且** 探测端口故障（`openTcpError`/`openUdpError` 让探测端口不回包 → 探测失败），
   等 `WAIT_KEEP_OPEN_SECONDS`(12s) > sleepWindow，burst 验证仍 abort
5. 探活恢复：`/echo` 与探测端口都恢复正常，等 `WAIT_RECOVER_SECONDS`(16s)，主动探测探活推动 CLOSE，burst 验证全 ok
6. 日志佐证：SDK 日志出现探测调度 + 状态切换记录

**通过条件**：`trigger_abort>=1` 且 `keep_abort>=1` 且 `recover_ok==FD_VERIFY_COUNT` 且 探测调度=yes 且 状态切换=yes。

> **维持 OPEN 需业务 500 + 探测端口故障双重保证**：SERVICE 级 half-open 会放行业务请求探活，若维持阶段
> 业务 `/echo` 恢复 200 会抢先 close。故维持阶段让业务 `/echo` 仍 500、同时让 TCP/UDP 探测端口故障，
> 恢复阶段则业务与探测端口同时转绿。TCP/UDP 探测器各暴露并修复了 3 个 SDK bug（`Name()` 撞名、
> `ReadAll` 无 read deadline 阻塞、`DetectResultImp` 缺 `Code` 哨兵），详见 fault-detect.md「与代码改造对应关系」。

### 共同验证原理
- 熔断规则 `faultDetectConfig.enable=true` 是探测启动门控；门控关闭则 SDK 不创建 `ResourceHealthChecker`
- 探测器按 `interval` 周期 GET `/echo`，结果经 `doReport(stat, record=false)` 上报，不触发实例重新注册
- HALF_OPEN 态下探测结果与业务请求共用恢复判定：连续成功达 `consecutiveSuccess` 即 `HALF_OPEN → CLOSE`，任一失败回 `OPEN`
- 日志佐证 grep 各 consumer 独立 SDK 日志 `.build/<name>_run/polaris/log/circuitbreaker/polaris-circuitbreaker.log`（非 stdout）：探测调度铁证用 `[CircuitBreaker] schedule task`（不用宽松 `[FaultDetect]`/`health check`，会被"is disabled"关停日志误判）；状态切换关键字小写 `status change: half-open -> close`
- 退出时熔断规则删除、**探测规则保留复用**（FaultDetectRule 无 enable 字段）

---

## 失败诊断

各用例对应的 SDK 日志目录见上方「SDK 日志目录映射」章节。每个用例失败时脚本会输出统计变量值，常见原因：

| 现象 | 可能原因 | 排查 |
|------|---------|------|
| `trigger_fail=0` | provider-b/`/switch` 未生效；或 5 次 `CONSECUTIVE_ERROR` 之间穿插了来自 provider-a 的 200 | 查 `provider_b.log`；增大 `--trigger-count` |
| `verify_abort=0`（仅服务级 / 接口级） | SDK 未应用 BlockConfig；或规则未启用；或被陌生规则干扰 | 查 consumer 日志；看"规则巡检"输出；增大 `WAIT_RULE_READY_SECONDS` |
| `verify_ok < RECOVERY_REQUEST_COUNT`（仅实例级） | 流量未完全转移；或 GetOneInstance 把 cb=Open 实例仍然返回 | 查 `instance_consumer.log` 中 `printAllInstances` 输出 |
| `recover_ok=0`（服务级/接口级/实例级） | sleepWindow 不够；或半开探测打到仍 500 的实例 | 增大 `--wait-half-open`；服务级用例必须把所有实例同步翻回 200 |
| `inspect_caller_rules` 警告"陌生规则" | 上一次脚本运行未清理干净 | 登录控制台清理；或设 `STRICT_RULE_CHECK=true` |
| 用例 6/7 verify 阶段全 EOF | case 5 C 段关 provider 后未正确重启 | 确认脚本已用主 shell `start_provider` 接管 |
| 用例 7 轮 2 verify=9 (期望 10) | CONSECUTIVE_ERROR=7 阈值下 burst=15 不够 | 确认轮 2 用了 case-local `MODIFY_R2_TRIGGER_COUNT=30` |
| 用例 8/10 trigger 阶段未命中 | `RequestContext.Protocol/HTTPMethod` 未正确推断 | 查 `pm_consumer.log` / `pathtype_consumer.log` |
| 用例 10 verify 阶段全 500（全死全活未生效） | trigger burst 不够，只有一个实例被熔断 | 确认 burst=60 |
| InvokeHandler 子阶段 trigger 不够 | 50/50 LB 下连续命中概率不足 | 确认 burst 值与 Decorator 阶段一致（30） |

## 与 polaris-go 代码改造的对应关系

| 改造点 | 由哪个用例验证 |
|--------|----------------|
| `MethodResource` 三元组（Protocol/Method/Path） | 用例 3 / 用例 8 |
| `selectCircuitBreakerRule` 遍历 `BlockConfigs.Api` | 用例 3 / 用例 8 |
| `ResourceCounters.init` 走 `BlockConfigs × TriggerConditions` | 用例 1/2/3 共同依赖 |
| `blockCounter.parseRetStatus` 块级独立判错 + 多种 MatchString | 用例 3 |
| `blockCounter.parseRetStatus` 处理 `INPUT_TYPE=DELAY` | 用例 3 `/slow` |
| `commonCheck/commonReport` 多级分发（SERVICE / METHOD） | 用例 2 + 用例 3 |
| `InvokeContext.SetInstance` + `commonReport` 实例级分支 | 用例 1 |
| 旧 `CircuitBreakerAPI.Report(InstanceResource)` 散装路径不破坏 | 用例 4 |
| `HalfOpenStatus.AcquirePermission` 精确发放配额 | 用例 1/2/3 恢复阶段 |
| `HalfOpenStatus.Release` 归集判定 | 用例 1/2/3 恢复阶段 |
| `CircuitBreakerRuleDictionary` 三级索引 | 用例 3 多接口隔离 |
| `blockCounter.parseRetStatus` 的 `-1` 哨兵识别 | 用例 5 C 段 |
| demo 端 4xx → OnSuccess、5xx → OnError 的分流 | 用例 5 A/B 段 |
| `default.go` dictionary lookup miss + `defaultRuleEnable=true` 兜底 | 用例 6 |
| `update_circuitbreaker_rule` API 热改 trigger 阈值 | 用例 7 |
| `matchProtocolOrMethod` 多维度独立匹配 | 用例 8 |
| `MatchString` 5 种类型正向验证 | 用例 9 |
| `FilterOnlyRouter` 全死全活兜底（`enableRecoverAll`） | 用例 10 |
| `MakeInvokeHandler` + `AcquirePermission` 手动编排模式 | 用例 1/2/3 InvokeHandler 子阶段 + 用例 10(B) |
| 三种调用模式行为一致性 | 用例 10（Decorator / InvokeHandler / Bare Report） |
| `buildFallbackInfo` 解析规则降级响应（SERVICE/METHOD） | 用例 2/3 fallback 正向子阶段 |
| `buildFallbackInfo` 的 `GetEnable()` 短路（enable=false 回 503） | 用例 2/3 fallback 反向子阶段 |
| `CallAborted` 携带 fallback + demo `HasFallback()` 透传/回退 | 用例 2/3 fallback 子阶段 |
| PUT 更新熔断规则 fallbackConfig.enable 触发 SDK counter 重建 | 用例 2/3 fallback 子阶段 |
| `reportCircuitBreakMetric` + `buildCircuitBreakGauge` 构造熔断 gauge 并通过 `SyncReportStat` 上报 | 用例 1/2/3/4（嵌入可观测性子步骤） |
| `PrometheusReporter.ReportStat` → `circuitBreakerCollector.CollectStatInfo` → `doAggregation` 聚合到 GaugeVec | 用例 1/2/3/4（嵌入可观测性子步骤） |
| `PullAction.doAggregation` 读 `cfg.Interval`（默认 15s）替代硬编码 30s | 用例 1/2/3/4（嵌入可观测性子步骤） |
| `PutDataFromContainerInOrder` `currentRevision>0` 守卫（熔断 gauge 永不过期，对齐 polaris-java） | 用例 1/2/3/4（嵌入可观测性子步骤） |
| `reportCircuitBreakerEvent` → `BuildCircuitBreakerEvent` → `sendEvent` 投递事件链 | 用例 1/2/3/4（嵌入可观测性子步骤） |
| `PushgatewayReporter.ReportEvent` 入队 + batch flush POST `/polaris/client/events` | 用例 1/2/3/4（嵌入可观测性子步骤） |
| `toOpen` reason 形参透传（trigger 层 `CloseToOpen` 传入 CONSECUTIVE_ERROR/ERROR_RATE 描述） | 用例 1/2/3/4（嵌入可观测性子步骤） |
| `mock_event_server.py` 全局启动 1 个捕获 pushgateway POST + cleanup 停止 | 用例 1/2/3/4（嵌入可观测性子步骤） |
| 一次 trigger 同时产出 gauge + event（prometheus + pushgateway 同配同 consumer） | 用例 1/2/3/4（嵌入可观测性子步骤） |
