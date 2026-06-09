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
                ┌────── instance-consumer     (18081)  selfService=CircuitBreakerInstanceCaller
                │
                ├────── service-consumer      (18082)  selfService=CircuitBreakerServiceCaller
   用户 curl ───┤
                ├────── interface-consumer    (18083)  selfService=CircuitBreakerInterfaceCaller
                │
                └────── old-instance-consumer (18084)  selfService=CircuitBreakerOldInstanceCaller
                                                       （旧版散装写法，向后兼容验证）
```

前三个 consumer 共享同一份源代码：`examples/circuitbreaker/newCircuitBreakerCaller/consumer/main.go`，
仅通过 CLI 参数 `--selfService / --port` 区分角色。统一装饰器写法：

- 通过 `CircuitBreakerAPI.MakeFunctionDecorator` 接入 SDK；
- `RequestContext.Method` 决定是否启用接口级熔断（demo 中 4 个端点都填了对应 path）；
- customer func 内 `model.GetInvokeContext(ctx).SetInstance(instance)` 让装饰器在结束阶段
  自动按 `InstanceResource` 上报，从而触发实例级熔断统计；
- 不调 `SetInstance` 时跳过实例级上报，行为与历史完全一致（向后兼容）。

第四个 consumer 使用旧版散装写法，对应源代码：
`examples/circuitbreaker/oldInstanceCircuitBreakerCaller/consumer/main.go`，

- 直接调用 `CircuitBreakerAPI.Report(InstanceResource)` 上报熔断结果；
- 直接调用 `ConsumerAPI.UpdateServiceCallResult` 上报调用统计；
- 不使用 `MakeFunctionDecorator / RequestContext / SetInstance`；
- 验证目的：保证存量客户在新版 SDK 中沿用旧版散装写法依旧能触发实例级熔断（向后兼容性验证）。

## Provider 暴露的接口

| 路径    | 默认行为                | 控制开关                                      | 用途                                        |
|---------|-------------------------|-----------------------------------------------|---------------------------------------------|
| `/echo` | a=200, b=500            | `/switch?openError=true\|false`               | 用例 1/2/3 主链路                           |
| `/order`| a=200, b=200            | `/switch?openErrorOrder=true\|false`          | 用例 3 多接口隔离对比                       |
| `/info` | 恒 500                  | 无                                            | 用例 3 验证"未配置规则的接口不会被熔断"     |
| `/slow` | 200，sleep `slowDelayMs`| `/switch?slowDelayMs=<int>`                   | 用例 3 验证"错误判断条件支持时延"           |
| `/switch`| 200                    | `openError` / `openErrorOrder` / `slowDelayMs`| 运行时切换上述行为                          |
| `/health`| 恒 200                 | 无                                            | 启动探针                                    |

## 公共流程

每次运行 `verify_circuitbreaker.sh`：

1. 步骤 A：环境准备（检查 Go/python3/curl，创建 `.build/`、`.logs/`，生成规则模板 `_gen_rule.py`）
2. 步骤 B：启动 provider-a + provider-b
3. 步骤 C：依次执行用例 1（instance）、用例 2（service）、用例 3（interface）、用例 4（old_instance）
   - 每个用例开始时打印结构化 `print_block` 概览（Caller 写法 / 规则配置 / 验证步骤 / 预期结果 / 判定标准）
   - 创建规则前会通过 `inspect_caller_rules` + `inspect_callee_rules` 巡检主调与被调维度的现有熔断规则
     - 默认仅 WARN，列出陌生规则
     - `STRICT_RULE_CHECK=true` 时遇到陌生规则直接 FAIL（避免规则污染）
4. 步骤 D：结果汇总，trap EXIT 时自动删除创建的熔断规则并停止进程

## 用例 1：实例级（INSTANCE）熔断

### Caller 写法
- 共享 `newCircuitBreakerCaller/consumer`
- `MakeFunctionDecorator` + `RequestContext.Method=/echo`
- customer func 内调 `SetInstance(instance)` → 装饰器自动按 InstanceResource 上报
- selfService=`CircuitBreakerInstanceCaller`，端口 18081

### 规则
- `level=INSTANCE`，`name=cb-instance-CircuitBreakerInstanceCaller`
- `rule_matcher.source.service=CircuitBreakerInstanceCaller`
- `rule_matcher.destination.service=CircuitBreakerCallee`
- `block_configs[0]`：
  - `error_conditions`：`RET_CODE RANGE 500~599`（4xx 不计入熔断）
  - `trigger_conditions`：`CONSECUTIVE_ERROR=5` + `ERROR_RATE 50%@30s, minRequest=10`
- `recover_condition.sleep_window=12s`，`consecutiveSuccess=1`

### 验证步骤
- 1.1 复位 provider：a=200，b=500
- 1.2 启动 instance consumer
- 1.3 创建/更新规则
- ── 第 1 轮 ──
- 1.4 触发：连发 `TRIGGER_REQUEST_COUNT`（默认 15）次 `/echo`，让 b 累计 5 次失败 → 实例 b 被摘除
- 1.5 验证：再发 `RECOVERY_REQUEST_COUNT`（默认 10）次，流量应全部走 a
- 1.6 恢复：b 翻回 200，等 `WAIT_HALF_OPEN_SECONDS`（默认 15s）进半开 → b 重新加入 LB
- ── 第 2 轮 ──
- 1.7 再次触发：b 重新置 500
- 1.8 再次验证：流量再次全部走 a
- 1.9 再次恢复：b 翻回 200

### 通过条件（共 6 项指标）
- 两轮 `trigger_fail >= 1`
- 两轮 `verify_ok == RECOVERY_REQUEST_COUNT`
- 两轮 `recover_ok == RECOVERY_REQUEST_COUNT`

---

## 用例 2：服务级（SERVICE）熔断

### Caller 写法
- 同 `newCircuitBreakerCaller/consumer`，selfService=`CircuitBreakerServiceCaller`，端口 18082
- 装饰器内 `commonCheck/commonReport` 会按 `ServiceResource` 上报 → 触发 SERVICE 级熔断
- customer func 仍调 `SetInstance`（实例级统计同时进行，但本用例不依赖也不验证）

### 规则
- `level=SERVICE`，`name=cb-service-CircuitBreakerServiceCaller`
- 其他字段同用例 1

### 验证步骤
- 2.1 复位 provider：a=500，b=500（模拟整服务不可用）
- 2.2 启动 service consumer
- 2.3 创建/更新规则
- ── 第 1 轮 ──
- 2.4 触发：连发 `TRIGGER_REQUEST_COUNT` 次累计错误率达阈值
- 2.5 验证：再发 `RECOVERY_REQUEST_COUNT` 次，期望出现 `call aborted`
- 2.6 恢复：a/b 都翻回 200（避免半开探测打到仍 500 的实例），等 `WAIT_HALF_OPEN_SECONDS` → 半开探测一次成功 → 关闭
- ── 第 2 轮 ──
- 2.7 再置 a/b=500 → 再次熔断
- 2.8 再次出现 abort
- 2.9 再次翻 200 → 再次恢复

### 通过条件（共 6 项指标）
- 两轮 `trigger_fail >= 1`
- 两轮 `verify_abort >= 1`
- 两轮 `recover_ok >= 1`

---

## 用例 3：接口级（METHOD）熔断 + 多接口隔离 + DELAY + 多 MatchString

### Caller 写法
- 同 `newCircuitBreakerCaller/consumer`，selfService=`CircuitBreakerInterfaceCaller`，端口 18083
- 一份 binary 同时暴露 4 个端点 `/echo /order /info /slow`
  - 每个端点对应一个独立装饰器，`RequestContext.Method=<path>` 区分接口级 BlockConfig

### 规则（3 条 METHOD 级规则，挂在同一个 service）
- `/echo` — `cb-interface-CircuitBreakerInterfaceCaller`
  - `BlockConfig.api.path=/echo` (EXACT)
  - `error_conditions`：5 条 RET_CODE 同时挂，覆盖 `RANGE / EXACT / REGEX / IN / NOT_IN` —— 全部只命中 5xx，4xx 不计入熔断
  - `trigger`: `CONSECUTIVE_ERROR=5 / ERROR_RATE=50%@30s, minReq=10`
  - `recover`: `sleepWindow=12s, consecutiveSuccess=1`
- `/order` — `cb-interface-CircuitBreakerInterfaceCaller-order`
  - `BlockConfig.api.path=/order` (EXACT)
  - 阈值故意调高：`CONSECUTIVE_ERROR=100 / ERROR_RATE=99%@30s, minReq=200`
  - 演示"两个接口配置不同的规则会按各自规则生效"
- `/slow` — `cb-interface-CircuitBreakerInterfaceCaller-slow`
  - `BlockConfig.api.path=/slow` (EXACT)
  - `error_conditions`：`input_type=DELAY, value=200`（毫秒）
  - 演示"错误判断条件支持时延"
- `/info` 无规则 + consumer 侧 `defaultRuleEnable=false` —— 演示"未配置规则的接口不会被熔断"

### 验证步骤
- 3.1 复位 provider：a/b 的 `/echo /order` 都 500；`/info` 恒 500；`/slow` 默认 0ms
- 3.2 启动 interface consumer
- 3.3 创建/更新 3 条规则
- ── /echo 第 1 轮 ──
- 3.4 触发：发 `TRIGGER_REQUEST_COUNT` 次 `/echo` 累计 5 次失败
- 3.5 验证：`RECOVERY_REQUEST_COUNT` 次 `/echo` 应出现 abort
- 3.6 恢复：a/b 的 `/echo` 翻 200，等 `WAIT_HALF_OPEN_SECONDS`，半开探测一次成功 → 关闭
- ── /echo 第 2 轮 ──
- 3.7 再置 500 → 再次触发熔断
- 3.8 再次 abort
- 3.9 再次翻 200 → 再次恢复
- ── 多接口隔离 ──
- 3.10 `/order`：发 `TRIGGER_REQUEST_COUNT` 次，应全部失败但无 abort（阈值远未达到）
- 3.11 `/info`：发 `TRIGGER_REQUEST_COUNT` 次，应全部失败但无 abort（无规则覆盖 + 默认规则关闭）
- ── /slow DELAY 熔断 ──
- 3.12 触发：把 `/slow` 延迟设 500ms（>200ms 阈值），发 `TRIGGER_REQUEST_COUNT` 次
- 3.13 验证：再发 `RECOVERY_REQUEST_COUNT` 次，应出现 abort（fast fail）
- 3.14 恢复：延迟清零，等 `WAIT_HALF_OPEN_SECONDS` → 全部 200

### 通过条件（共 11 项指标）
- `/echo`  两轮 trigger ≥1 fail / verify ≥1 abort / recover 全 200
- `/order` 整批失败但 `abort == 0`
- `/info`  整批失败但 `abort == 0`（依赖 `defaultRuleEnable=false`）
- `/slow`  trigger 阶段成功完成 ≥1 / verify ≥1 abort / recover 全 200

---

## 用例 4：存量散装写法（旧版 API）的实例级熔断

### Caller 写法
- 旧版散装路径，源码：`oldInstanceCircuitBreakerCaller/consumer/main.go`
- 直接调用 `CircuitBreakerAPI.Report(InstanceResource)` → 上报熔断结果
- 直接调用 `ConsumerAPI.UpdateServiceCallResult` → 上报调用统计
- **不**使用 `MakeFunctionDecorator / RequestContext / SetInstance`
- selfService=`CircuitBreakerOldInstanceCaller`，端口 18084

### 验证目的
- 保证存量客户在新版 SDK 中沿用旧版散装写法依旧能触发实例级熔断
- 即「向后兼容」语义不被破坏（与用例1 行为对齐）

### 规则
- `level=INSTANCE`，`name=cb-instance-CircuitBreakerOldInstanceCaller`
- `rule_matcher.source.service=CircuitBreakerOldInstanceCaller`
- `rule_matcher.destination.service=CircuitBreakerCallee`
- 触发/恢复条件与用例1 完全一致：
  - `error_conditions`：`RET_CODE RANGE 500~599`（4xx 不计入熔断）
  - `trigger_conditions`：`CONSECUTIVE_ERROR=5` + `ERROR_RATE 50%@30s, minRequest=10`
  - `recover_condition.sleep_window=12s`，`consecutiveSuccess=1`
- source.service 与用例1 不同，避免规则相互覆盖

### 验证步骤
- 4.1 复位 provider：a=200，b=500
- 4.2 启动 old-instance consumer
- 4.3 创建/更新规则
- ── 第 1 轮 ──
- 4.4 触发：连发 `TRIGGER_REQUEST_COUNT`（默认 15）次 `/echo`，让 b 累计 5 次失败 → 实例 b 被摘除
- 4.5 验证：再发 `RECOVERY_REQUEST_COUNT`（默认 10）次，流量应全部走 a
- 4.6 恢复：b 翻回 200，等 `WAIT_HALF_OPEN_SECONDS`（默认 15s）进半开 → b 重新加入 LB
- ── 第 2 轮 ──
- 4.7 再次触发：b 重新置 500
- 4.8 再次验证：流量再次全部走 a
- 4.9 再次恢复：b 翻回 200

### 通过条件（共 6 项指标，与用例1 对齐）
- 两轮 `trigger_fail >= 1`
- 两轮 `verify_ok == RECOVERY_REQUEST_COUNT`
- 两轮 `recover_ok == RECOVERY_REQUEST_COUNT`

---

## 用例 5：HTTP 状态码区分（4xx 不熔断 / 5xx 熔断 / 网络错熔断）

### Caller 写法
- 同 `newCircuitBreakerCaller/consumer`，selfService=`CircuitBreakerHttpStatusCaller`，端口 18085
- 装饰器内部按 HTTP 状态码分流：
  - 5xx：customer func `return error` → 装饰器 OnError → SDK 内部 `retCode="-1"`
  - 4xx：customer func `return body, nil` → 装饰器 OnSuccess → 真实状态码透传
  - 网络错：customer func `return error` → 装饰器 OnError → SDK 内部 `retCode="-1"`

### 规则
- `level=INSTANCE`，`name=cb-instance-CircuitBreakerHttpStatusCaller`
- `rule_matcher.source.service=CircuitBreakerHttpStatusCaller`（与用例 1/4 隔离）
- `block_configs[0]`：
  - `error_conditions`：`RET_CODE RANGE 500~599`
  - `trigger_conditions`：`CONSECUTIVE_ERROR=3`（收紧阈值便于快速验证）
- `recover_condition.sleep_window=12s`，`consecutiveSuccess=1`

### 验证步骤
- 5.1 复位 provider：a=200 / b=200
- 5.2 启动 http_status consumer
- 5.3 创建/更新规则
- ── A 段：4xx 不熔断 ──
- 5.4 连发 `TRIGGER_REQUEST_COUNT` 次 `/forbidden`（provider 永远返回 403）
- ── B 段：5xx 熔断 ──
- 5.5 provider-b=500，连发 `TRIGGER_REQUEST_COUNT` 次 `/echo` → b 累计 3 次 5xx 触发熔断
- 5.6 验证：再发 `RECOVERY_REQUEST_COUNT` 次 `/echo` 应全部走 a → 200
- 5.7 恢复：provider-b=200，等 `WAIT_HALF_OPEN_SECONDS` → 半开探测 → 关闭
- ── C 段：网络错熔断（-1 哨兵） ──
- 5.8 关停 provider-a / provider-b 制造网络错
- 5.9 连发 `TRIGGER_REQUEST_COUNT` 次 `/echo` → SDK 内部 retCode="-1" 命中 RANGE 类条件
- 5.10 重启 provider 恢复，等 `WAIT_HALF_OPEN_SECONDS` 让 sleepWindow 过期

### 通过条件（3 段独立判定）
- A 段：`a_fail ≥ 1` 且 `a_abort == 0`（4xx 全部 fail 但永不熔断 —— 关键约束）
- B 段：`b_trigger_fail ≥ 3` 且 `b_verify_ok == RECOVERY_REQUEST_COUNT` 且 `b_recover_ok == RECOVERY_REQUEST_COUNT`
- C 段：`c_fail ≥ 1` 且 `c_abort ≥ 1`（网络错触发熔断 —— 验证 -1 哨兵生效）

### 验证目的
1. 验证 4xx 在默认 `RANGE 500~599` 规则下不被熔断（demo 端 4xx 走 OnSuccess）
2. 验证 5xx 通过 OnError 路径累计 3 次后正常触发熔断
3. 验证 SDK 内部 `-1` 哨兵能让网络错被熔断器拦截，无需依赖具体规则配置

---

## 用例 6：默认实例级熔断兜底（服务端无规则）

### Caller 写法
- 同 `newCircuitBreakerCaller/consumer`，selfService=`CircuitBreakerDefaultRuleCaller`，端口 18086
- consumer 启动时通过 `polaris.yaml` 启用 `defaultRuleEnable: true`
- 5xx 走 OnError → SDK 内部 `retCode="-1"` → 命中默认规则的 RANGE 500~599

### 默认规则（由 SDK 本地生成）
来自 `plugin/circuitbreaker/composite/default.go::getCircuitBreakerRule`：

- `level=INSTANCE`，`name=default-polaris-instance-circuit-breaker`
- `block_configs[0]`：
  - `error_conditions`：`RET_CODE RANGE 500~599`
  - `trigger_conditions`：`CONSECUTIVE_ERROR=3` + `ERROR_RATE 50%@30s, minRequest=3`（脚本侧 yaml 收紧阈值便于快速验证）
- `recover_condition.sleep_window=12s`，`consecutiveSuccess=1`

### 与用例 1 的根本区别
- **不向服务端创建任何规则**；不调用 `create_circuitbreaker_rule`
- selfService 独立（`CircuitBreakerDefaultRuleCaller`），避免被其它用例的规则误命中
- 验证 SDK `default.go` 在 `dictionary.Lookup` 未命中且 `level=INSTANCE` 时的回退路径

### 验证步骤
- 6.1 复位 provider：a=200 / b=500
- 6.2 启动 default-rule consumer（启用 `defaultRuleEnable=true`）
- 6.3 跳过规则创建（关键：不向服务端创建任何熔断规则）
- 6.4 触发：连发 `TRIGGER_REQUEST_COUNT` 次 → b 累计 3 次 5xx 触发熔断
- 6.5 验证：再发 `RECOVERY_REQUEST_COUNT` 次，流量应全部走 a → 200
- 6.6 恢复：b 翻回 200，等 `WAIT_HALF_OPEN_SECONDS` 进半开 → b 重新加入 LB

### 通过条件
- `trigger_fail ≥ 3`（默认规则按 5xx 触发熔断）
- `verify_ok == RECOVERY_REQUEST_COUNT`（流量全部走 a）
- `recover_ok == RECOVERY_REQUEST_COUNT`（半开探测一次成功 → 关闭）

### 验证目的
1. 服务端 0 规则时，本地默认实例级熔断仍能兜底生效
2. 验证 `default.go` 在 dictionary lookup miss + level=INSTANCE 路径下被正确触发
3. 默认规则的 `RANGE 500~599` 与 demo 的"5xx 走 OnError" 端到端联动

---

## 失败诊断

每个用例失败时脚本会输出统计变量值，常见原因：

| 现象 | 可能原因 | 排查 |
|------|---------|------|
| `trigger_fail=0` | provider-b/`/switch` 未生效；或 5 次 `CONSECUTIVE_ERROR` 之间穿插了来自 provider-a 的 200，没累计够 | 查 `provider_b.log`；增大 `--trigger-count` |
| `verify_abort=0`（仅服务级 / 接口级） | SDK 未应用 BlockConfig；或规则未启用；或 SDK 还没拉到规则；或被陌生规则干扰 | 查 `*_consumer.log` 中是否有 `circuit breaker is created` 日志；看脚本启动时的"规则巡检"输出；增大 `WAIT_RULE_READY_SECONDS` |
| `verify_ok < RECOVERY_REQUEST_COUNT`（仅实例级） | 流量未完全转移；或 provider-a 同时也异常；或 GetOneInstance 把 cb=Open 实例仍然返回 | 查 `instance_consumer.log` 中 `printAllInstances` 输出 |
| `recover_ok=0`（服务级/接口级/实例级） | sleepWindow 不够；或半开探测打到仍 500 的实例；或 HalfOpen → Close 没生效 | 增大 `--wait-half-open`；服务级用例必须把所有实例同步翻回 200 |
| `inspect_caller_rules` 警告"陌生规则" | 上一次脚本运行未清理干净；或控制台手工建过同主调的规则 | 登录控制台清理；或设 `STRICT_RULE_CHECK=true` 让脚本直接 FAIL 防误判 |

## 与 polaris-go 代码改造的对应关系

| 改造点 | 由哪个用例验证 |
|--------|----------------|
| `MethodResource` 三元组（Protocol/Method/Path） | 用例 3 |
| `selectCircuitBreakerRule` 遍历 `BlockConfigs.Api` | 用例 3 |
| `ResourceCounters.init` 走 `BlockConfigs × TriggerConditions` | 用例 1/2/3 共同依赖 |
| `blockCounter.parseRetStatus` 块级独立判错 + 多种 MatchString | 用例 3 |
| `blockCounter.parseRetStatus` 处理 `INPUT_TYPE=DELAY` | 用例 3 `/slow` |
| `commonCheck/commonReport` 多级分发（SERVICE / METHOD） | 用例 2 + 用例 3 |
| `InvokeContext.SetInstance` + `commonReport` 实例级分支（统一装饰器写法支持实例级熔断） | 用例 1（也覆盖用例 2/3 的 SetInstance 调用） |
| 旧 `ConsumerAPI.UpdateServiceCallResult` + `CircuitBreakerAPI.Report(InstanceResource)` 散装路径不破坏 | 用例 4（端到端验证存量客户零改动） |
| `HalfOpenStatus.AcquirePermission` 精确发放配额（`recover.consecutiveSuccess`） | 用例 1/2/3 的恢复阶段：半开态下并发请求只有 `consecutiveSuccess` 个被放行 |
| `HalfOpenStatus.Release` 归集判定（任一失败 → Open / 全成功 → Close） | 用例 1/2/3 的恢复阶段：放行结果决定下一状态 |
| `CircuitBreakerRuleDictionary` 三级索引（Level→ServiceKey→Rules） | 用例 3 多接口隔离（同 service 下三条 METHOD 规则按各自 path 独立命中） |
| 周期性规则复检 `checkRules`（默认 60s） | 用例 1/2/3：长时间运行时若 push 通道丢失，规则仍能在下一周期被字典更新 |
| `blockCounter.parseRetStatus` 的 `-1` 哨兵识别（与 polaris-java 对齐） | 用例 5 C 段：网络错路径下 SDK 默认 `code="-1"`，哨兵直接命中 RANGE 类条件触发熔断 |
| demo 端 4xx → OnSuccess、5xx → OnError 的分流（与 polaris-java 同款） | 用例 5 A 段：4xx 透传真实状态码后不命中 RANGE 500~599，永不熔断；用例 5 B 段：5xx 走 OnError 触发熔断 |
| `default.go` dictionary lookup miss + `defaultRuleEnable=true` 兜底默认规则 | 用例 6：服务端 0 规则时，本地默认实例级熔断按 RANGE 500~599 + CONSECUTIVE_ERROR=3 触发 |
