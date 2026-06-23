# examples/circuitbreaker — 主动探测（Fault Detect）验证说明

本文档描述 `verify_faultdetect.sh` 执行的主动探测验证用例及其判定准则。脚本中所有步骤编号必须与本文一致。

## 测试拓扑

```
                  ┌──── provider-a (端口 28081，默认 200) ────┐
                  │   /echo /health + /switch               │  一个进程同时注册到五个被调服务：
                  │   TCP 探测端口 28091 / UDP 探测端口 28101  │      CircuitBreakerFDSvcCallee
   Polaris ◀──────┤   （TCP/UDP 探测端口仅 provider-a 启动）   ├──── CircuitBreakerFDMethodCallee
                  │   /echo /health + /switch               │      CircuitBreakerFDInstanceCallee
                  └──── provider-b (端口 28082，默认 500) ────┘      CircuitBreakerFDTcpCallee
                                       ▲                              CircuitBreakerFDUdpCallee
                                       │
   ┌── fd 五个用例各自独立 caller + consumer + 被调服务：
   │     服务级(SERVICE)  : consumer 18095 selfService=CircuitBreakerFaultDetectCaller  callee=CircuitBreakerFDSvcCallee
   │     接口级(METHOD)   : consumer 18096 selfService=CircuitBreakerFDMethodCaller     callee=CircuitBreakerFDMethodCallee
   │     实例级(INSTANCE) : consumer 18097 selfService=CircuitBreakerFDInstanceCaller   callee=CircuitBreakerFDInstanceCallee
   │     TCP 探测(SERVICE): consumer 18098 selfService=CircuitBreakerFDTcpCaller        callee=CircuitBreakerFDTcpCallee
   │     UDP 探测(SERVICE): consumer 18099 selfService=CircuitBreakerFDUdpCaller        callee=CircuitBreakerFDUdpCallee
```

> 与 `verify_circuitbreaker.sh` 共用 provider-a / provider-b 两个进程，但**五个用例各用独立被调服务**
> （provider 通过 `--service` 逗号分隔同时注册到五个服务）。每个用例再配独立 caller + 独立 consumer 端口 +
> 独立规则名（`cb-fd-<LEVEL>-<caller>` / `fd-fd-<LEVEL>-<caller>`）。
>
> **TCP/UDP 探测端口仅 provider-a 启动**：`FaultDetectRule.port` 是单值，`port>0` 时所有被探测实例共用
> 同一端口号，而同机两进程（provider-a/b）不能绑定同一 TCP/UDP 端口。因此脚本只给 provider-a 传
> `--tcp-probe-port`(28091) / `--udp-probe-port`(28101)，探测规则 `port` 固定指向 provider-a 的探测端口。
> SERVICE 级闭环不依赖多实例，单实例 provider-a 足够。

> **为什么三级用独立被调服务**：若三级共用 `CircuitBreakerCallee`，会被 `verify_circuitbreaker.sh`
> 用例10 残留的 `cb-instance-CircuitBreakerHttpStatusCaller`（`source=*/*` catch-all、无 faultDetectConfig、
> DELETE 403 删不掉）按规则排序（priority→destination→id，**不比 source**；两条 prio/dst 相同时按 id
> 字典序）抢占 —— 残留规则 id 字典序靠前会胜出，导致 INSTANCE 资源命中无探测门控的残留规则、
> 主动探测门控判 disabled 而从未启动（业务闭环靠残留规则也能跑通，但纯业务请求恢复、无探测）。
> Go 与 polaris-java 的排序逻辑一致（都不比 source），这不是 SDK bug 而是共享环境残留污染；用独立
> 被调服务从根本上隔离，三级互不干扰。

> **三个级别的语义差异**（决定各自的判定指标不同）：
> - **SERVICE / METHOD 级**：经 `AcquirePermission`，熔断 OPEN 时业务请求直接 `abort`（503）。
> - **INSTANCE 级**：不经 `AcquirePermission`，某实例 OPEN 时由服务路由层（FilterOnlyRouter）将其
>   从可选列表摘除、LB 不再选它，业务请求落到其余健康实例返回 200，**不是 abort**。

## 验证目标

验证熔断主动探测（Fault Detect / Active Health Check）的完整闭环：

```
业务请求触发服务级熔断 OPEN
  → SDK 周期性主动探测 provider /echo（provider 仍故障，探测失败 → 维持 OPEN）
  → 恢复 provider（业务与探测同时转绿）
  → 主动探测探活成功，推动 HALF_OPEN → CLOSE
```

## 闭环依赖的 SDK 能力

| 能力 | 说明 |
|------|------|
| 熔断规则 `faultDetectConfig.enable=true` | 探测启动门控，门控关闭则 SDK 不创建 `ResourceHealthChecker` |
| `ResourceHealthChecker` 周期调度 | 由 `TaskExecutor` 注入，按 `FaultDetectRule.interval` 周期执行探测 |
| 探测结果 `record=false` 上报 | 不触发实例重新注册，仅参与 HALF_OPEN 恢复判定 |
| HALF_OPEN 恢复判定 | 探测结果与业务请求共用：连续成功达 `consecutiveSuccess` → CLOSE，任一失败 → OPEN |

## Consumer 源码

复用 `newCircuitBreakerCaller/consumer`（Decorator 写法），端口 18095。

```bash
.build/fault_detect_consumer_run/polaris/log/circuitbreaker/polaris-circuitbreaker.log
```

> 排查主动探测问题时，优先看上述日志文件。该日志包含规则加载、计数器初始化、
> `[FaultDetect]` 探测调度、状态切换（`Open → HalfOpen → Close`）等完整链路信息。

## Provider 暴露的接口

| 路径      | 默认行为 | 控制开关                        | 用途                           |
|-----------|----------|--------------------------------|--------------------------------|
| `/echo`   | 200      | `/switch?openError=true\|false` | 探测端点（随故障开关变化）      |
| `/health` | 恒 200   | 无                             | 启动探针（**不可用作探测端点**） |
| `/switch` | 200      | `openError`                    | 运行时切换 `/echo` 返回码       |

> **探测端点为何选 `/echo`**：`/health` 固定返回 200，无法反映实例健康变化；
> 一旦熔断进入半开就会被它立即拉回 CLOSE，验证不出探测与恢复的因果。
> `/echo` 受 provider `needErr` 开关控制，挂时探测失败、恢复时探测成功，闭环可被稳定观察。

## 规则清单

五个用例各 1 条熔断规则 + 1 条探测规则，共 **10 条规则**。规则名按级别/协议编码隔离：

| 用例 | 熔断规则 | 探测规则 |
|------|----------|----------|
| 服务级(SERVICE)  | `cb-fd-SERVICE-CircuitBreakerFaultDetectCaller` | `fd-fd-SERVICE-CircuitBreakerFaultDetectCaller` |
| 接口级(METHOD)   | `cb-fd-METHOD-CircuitBreakerFDMethodCaller` | `fd-fd-METHOD-CircuitBreakerFDMethodCaller` |
| 实例级(INSTANCE) | `cb-fd-INSTANCE-CircuitBreakerFDInstanceCaller` | `fd-fd-INSTANCE-CircuitBreakerFDInstanceCaller` |
| TCP 探测(SERVICE) | `cb-fd-TCP-CircuitBreakerFDTcpCaller` | `fd-fd-TCP-CircuitBreakerFDTcpCaller` |
| UDP 探测(SERVICE) | `cb-fd-UDP-CircuitBreakerFDUdpCaller` | `fd-fd-UDP-CircuitBreakerFDUdpCaller` |

熔断规则公共配置：`CONSECUTIVE_ERROR=5`，`sleepWindow=6s`，`consecutiveSuccess=1`，
**`faultDetectConfig.enable=true`**（探测门控），`error_conditions=RET_CODE RANGE 500~599`。
HTTP 探测规则公共配置（SERVICE/METHOD/INSTANCE 三级）：`protocol=HTTP`(1)，`GET /echo`，`interval=2s`，
`timeout=1000ms`，`port=0`（使用被探测实例自身注册的端口 28081 / 28082）。
TCP/UDP 探测规则配置：`protocol=TCP`(2) / `UDP`(3)，用 `tcp_config` / `udp_config`（均为 `{send, receive[]}`，
`send="ping"`、`receive="tcp-ok"`/`"udp-ok"` 做内容匹配判健康），`interval=2s`，`timeout=1000ms`，
`port` 固定指向 provider-a 的独立探测端口（TCP=28091 / UDP=28101）。

> **探测规则 protocol 字段枚举**：1=HTTP / 2=TCP / 3=UDP。HTTP 用 `http_config`，TCP 用 `tcp_config`，
> UDP 用 `udp_config`。TCP/UDP 的 config 都是 `{send, receive[]}`：探测器发送 `send`、收到对端响应后与
> `receive` 列表做内容匹配，匹配则判探测成功。

> **熔断规则只配 `CONSECUTIVE_ERROR`，不叠加 `ERROR_RATE`**：ERROR_RATE 用 30s 滑动窗口统计 failRatio，
> 探测推动 `half-open → close` 后窗口内仍残留 OPEN 期间的失败记录，高频(2s)探测成功来不及把 failRatio
> 稀释到 50% 阈值以下，会导致 close 后立刻 re-open 抖动、恢复阶段假性失败。CONSECUTIVE_ERROR 无此问题
> （连续成功即恢复，不看历史窗口）。

### 各级别的匹配维度差异

| 级别 | 熔断规则 `level` | `destination.method` | `BlockConfig.api.path` | 探测规则 `target_service.method` |
|------|------|------|------|------|
| 服务级(SERVICE)  | `SERVICE`  | `*` | 空 | `*` |
| 接口级(METHOD)   | `METHOD`   | `/echo` | `/echo` | `/echo`（与熔断、业务三者方法维度同源） |
| 实例级(INSTANCE) | `INSTANCE` | `*` | 空 | `*` |

各熔断规则 `rule_matcher.source.service` 为对应级别的独立 caller，`destination.service` 为对应级别的
独立被调服务（SERVICE→CircuitBreakerFDSvcCallee、METHOD→CircuitBreakerFDMethodCallee、
INSTANCE→CircuitBreakerFDInstanceCallee）。探测规则 `target_service.service` 同此被调服务。

## 公共流程

每次运行 `verify_faultdetect.sh`：

1. 步骤 A：环境准备（检查 Go/python3/curl/lsof，创建 `.build/`、`.logs/`）
2. 步骤 B：启动 provider-a + provider-b（provider-a 额外启动 TCP/UDP 探测端口）
3. 步骤 C：依次执行五个用例（受 `RUN_FD_CASES` 控制，默认 `service,method,instance,tcp,udp` 全跑；
   未选中的用例标记 SKIP，不计入失败）。每个用例：启动独立 consumer + 下发熔断规则 + 探测规则 →
   触发 OPEN → 维持 OPEN → 探活恢复 → 日志佐证。
4. 步骤 D：结果汇总（五个用例各打印 PASS/FAIL/SKIP，任一被选中执行的用例 FAIL 即整体失败）。
   trap EXIT 时：**熔断规则删除**；**探测规则保留不删**（跨运行幂等复用，下次存在则 PUT 更新复用），
   停止 consumer 与 provider 进程。

> **探测规则跨运行复用**：FaultDetectRule **无 `enable` 字段**，无法 PUT enable=false 关闭；探测的真正
> 启停由其依附熔断规则的 `faultDetectConfig.enable` 门控。脚本对探测规则采用"先查后建、存在则 PUT
> 更新复用、cleanup 不删"，避免反复删建。

---

## 用例一 / 用例二：服务级(SERVICE) / 接口级(METHOD) 闭环验证（abort 型）

SERVICE 与 METHOD 级都经 `AcquirePermission`，熔断 OPEN 时业务请求 `abort`，闭环结构一致，脚本中
共用辅助函数 `_run_probe_abort_case`。两者唯一区别是规则匹配维度（见上「各级别的匹配维度差异」）：
METHOD 级把 `destination.method`、`BlockConfig.api.path`、探测 `target_service.method` 都限定到 `/echo`。

### 验证步骤（SERVICE / METHOD 通用）

| 步骤 | 动作 | 判定指标 |
|------|------|---------|
| [1] 环境复位 | provider-a/b 均置 200 | — |
| [2] 启动 consumer + 下发规则 | 启动独立 consumer；创建熔断规则（含 `faultDetectConfig.enable=true`）+ 探测规则；等待 `WAIT_RULE_READY_SECONDS`(6s) | — |
| [3] 触发熔断 OPEN | provider-a/b 均置 500，burst `FD_TRIGGER_COUNT`(30) 次 `/echo` | `trigger_abort >= 1` |
| [4] 维持 OPEN | provider 保持 500，等 `WAIT_KEEP_OPEN_SECONDS`(12s, > sleepWindow)，burst `FD_VERIFY_COUNT`(10) | `keep_abort >= 1`（探测失败不恢复） |
| [5] 探活恢复 | provider-a/b 翻回 200，等 `WAIT_RECOVER_SECONDS`(16s)，**不主动制造业务压力推动**，burst 验证 | `recover_ok == FD_VERIFY_COUNT`（恢复 CLOSE） |
| [6] 日志佐证 | grep SDK 熔断日志（见下）确认探测调度 + 状态切换 | `探测调度=yes` 且 `状态切换=yes` |
| ──── | **D 段：SERVICE 用例 — enable toggle 验证** | ──── |
| [7] 关闭探测 | PUT 熔断规则 `faultDetectConfig.enable=false`，等待 SDK 感知规则变更 | `disabled_stop=yes`（日志含 `[FaultDetect] health check for resource=... is disabled, now stop`） |
| [8] 重新开启探测 | PUT 熔断规则 `faultDetectConfig.enable=true`，等待 SDK 感知规则变更 | `resume_schedule=yes`（`schedule task` 行数增加） |
| ──── | **D 段：METHOD 用例 — 规则参数热更新验证** | ──── |
| [7] 修改探测间隔 | PUT 探测规则 `interval=5s`（原 2s），等待 SDK 感知规则变更 | `new_interval=yes`（日志出现 `schedule task ... interval=5s`） |

> **D 段说明**：SERVICE 用例验证 enable toggle 动态启停（`faultDetectConfig.enable` true→false→true），
> METHOD 用例验证探测规则参数热更新（interval 2s→5s）。D 段复用 A-C 段已启动的 consumer，不增加进程启动。
> D 段失败不影响 A-C 段判定结果（但整体仍报 FAIL，指标表中以 D 段列单独标注）。

### 通过条件（SERVICE / METHOD）

**A-C 段**：`trigger_abort >= 1` 且 `keep_abort >= 1` 且 `recover_ok == FD_VERIFY_COUNT` 且 `探测调度=yes` 且 `状态切换=yes`。

**D 段（SERVICE — toggle）**：`disabled_stop=yes` 且 `resume_schedule=yes`。

**D 段（METHOD — interval）**：`new_interval=yes`。

A-C 段与 D 段**全部满足**方为 PASS，任一失败即为 FAIL。

---

## 用例三：实例级(INSTANCE) 闭环验证（ok 型，单实例故障方案）

INSTANCE 级**不经过 AcquirePermission**：某实例 OPEN 时被服务路由层摘除、LB 不选它，业务请求落到
其余健康实例返回 200，**不是 abort**。因此判定指标与 abort 型完全不同，采用「单实例故障」方案：
只挂 provider-b，观察"b 被摘除 → b 恢复可选"的完整闭环。

### 验证步骤

| 步骤 | 动作 | 判定指标 |
|------|------|---------|
| [1] 环境复位 | provider-a/b 均置 200 | — |
| [2] 启动 consumer + 下发规则 | 启动 fd_instance_consumer(18097)；下发 INSTANCE 级熔断规则 + 探测规则；等待规则就绪 | — |
| [3] 触发 b 实例 OPEN | **仅 provider-b 置 500**（a 保持 200），burst `FD_TRIGGER_COUNT`(30) 次 | `trigger_fail >= 1`（b 被选中时 500，**不要求 abort**） |
| [4] 维持 OPEN(b 被摘除) | b 保持 500，等 `WAIT_KEEP_OPEN_SECONDS`(12s)，burst 验证 | `keep_ok >= FD_VERIFY_COUNT-1`（业务落 a，容忍 1 次抖动） |
| [5] 探活恢复(b 重新可选) | provider-b 翻回 200，等 `WAIT_RECOVER_SECONDS`(16s)，burst 验证 | `recover_ok >= FD_VERIFY_COUNT-1`（b 恢复可选） |
| [6] 日志佐证 | grep SDK 熔断日志确认探测调度 + **INSTANCE 级** half-open→close | `探测调度=yes` 且 `INSTANCE状态切换=yes` |

### 通过条件（INSTANCE，5 项全满足）

`trigger_fail >= 1` 且 `keep_ok >= FD_VERIFY_COUNT-1` 且 `recover_ok >= FD_VERIFY_COUNT-1` 且
`探测调度=yes` 且 `INSTANCE状态切换=yes`。

> **维持/探活 ok 容忍 1 次抖动（`>= N-1`）**：50/50 LB 下 b 刚 OPEN 的瞬间可能有 1 个在途请求仍命中 b。

> **INSTANCE 级恢复的实测行为**：b 实例进入 half-open 态后，SDK 会放行业务请求去探活 b，因此 b 的
> 最终 close 通常由 half-open 态业务请求探活推动；主动探测在 b OPEN 期间也持续调度（探测调度=yes 佐证）。
> 因此 INSTANCE 级**不**用"provider-b 收到探测请求增量"作铁证（provider 为三用例共享、计数会串扰，
> 且探测探活请求不一定落在恢复窗口），而以 SDK 日志中 **INSTANCE 级 b 实例 half-open→close** 作为
> 状态机恢复的铁证。

---

## 用例四 / 用例五：TCP / UDP 探测闭环验证（abort 型，单实例 provider-a）

TCP / UDP 用例都是 **SERVICE 级熔断规则（abort 型）**，闭环结构与 HTTP SERVICE 用例一致，脚本中共用
辅助函数 `_run_probe_tcpudp_case`。与 HTTP SERVICE 用例的区别：

- **探测协议**：TCP(2) / UDP(3)，探测打 provider-a 的**独立探测端口**（TCP=28091 / UDP=28101），用
  `send="ping"`、`receive="tcp-ok"`/`"udp-ok"` 做内容匹配判健康。
- **业务故障与探测故障分两个开关**：业务故障（触发熔断 OPEN）仍用 HTTP `/echo` 的 `openError` 开关；
  探测故障（维持 OPEN）用 provider 的 `openTcpError` / `openUdpError` 开关（让 TCP/UDP 探测端口不回包 →
  探测失败）。
- **单实例 provider-a**：因 `FaultDetectRule.port` 单值、同机两进程不能绑同一 TCP/UDP 端口，TCP/UDP 探测
  端口只在 provider-a 启动，探测规则 `port` 固定指向 a 的探测端口。SERVICE 级闭环不依赖多实例，单实例足够。

### 验证步骤（TCP / UDP 通用）

| 步骤 | 动作 | 判定指标 |
|------|------|---------|
| [1] 环境复位 | provider-a/b 的 `/echo` 置 200；TCP/UDP 探测端口正常回包 | — |
| [2] 启动 consumer + 下发规则 | 启动独立 consumer（TCP=18098 / UDP=18099）；创建 SERVICE 级熔断规则（含 `faultDetectConfig.enable=true`）+ TCP/UDP 探测规则（指向 a 的探测端口）；等待 `WAIT_RULE_READY_SECONDS`(6s) | — |
| [3] 触发熔断 OPEN | provider-a/b 的 `/echo` 置 500，burst `FD_TRIGGER_COUNT`(30) 次 | `trigger_abort >= 1` |
| [4] 维持 OPEN | `/echo` 保持 500 **且** 探测端口故障（`openTcpError`/`openUdpError` 让探测端口不回包），等 `WAIT_KEEP_OPEN_SECONDS`(12s, > sleepWindow)，burst 验证 | `keep_abort >= 1`（探测失败不恢复） |
| [5] 探活恢复 | `/echo` 与探测端口都恢复正常，等 `WAIT_RECOVER_SECONDS`(16s)，主动探测探活推动 close，burst 验证 | `recover_ok == FD_VERIFY_COUNT`（恢复 CLOSE） |
| [6] 日志佐证 | grep SDK 熔断日志确认探测调度 + 状态切换 | `探测调度=yes` 且 `状态切换=yes` |

> **维持 OPEN 为何要同时保持业务 500 + 探测端口故障（双重保证）**：SERVICE 级 half-open 会放行业务请求
> 去探活，若维持阶段业务 `/echo` 恢复 200，业务请求会抢先推动 close。因此维持阶段必须让业务 `/echo`
> 仍 500、同时让 TCP/UDP 探测端口故障，双重保证熔断不被意外恢复。恢复阶段则业务与探测端口同时转绿。

### 通过条件（TCP / UDP，5 项全满足）

`trigger_abort >= 1` 且 `keep_abort >= 1` 且 `recover_ok == FD_VERIFY_COUNT` 且 `探测调度=yes` 且 `状态切换=yes`。

---

## 用例六：协议/方法维度 + HTTP Headers 验证

验证 `selectFaultDetectRules` 对 `target_service.method` 的过滤正确性，以及 `generateHttpRequest` 对 `http_config.headers` 的设置正确性。**复用已有 SERVICE consumer（18095）和 METHOD consumer（18096），不新增进程。**

### 验证步骤

#### 段 A：SERVICE 级 method 过滤

| 步骤 | 动作 | 判定指标 |
|------|------|---------|
| [A.1] | 创建 `method.value=/api/protocol/http` 的探测规则（非通配） | `schedule task` 行数不变（SERVICE 级拒绝非通配 method） |
| [A.2] | 创建 `method.value=*` 的探测规则（通配） | `schedule task` 行数增加（SERVICE 级接受通配 method） |

> **原理**：SERVICE 级资源在 `selectFaultDetectRules` 中只接受 `method.value` 为空或 `*` 的探测规则（`match.IsMatchAll`），非通配值会被过滤。段 A 不触发熔断，仅验证规则过滤。

#### 段 B：METHOD 级 method 过滤 + 完整闭环

| 步骤 | 动作 | 判定指标 |
|------|------|---------|
| [B.1] | 创建 `method.value=/echo` + `method.value=/api/protocol/http` 两条探测规则 | 只有 `/echo` 的 `schedule task` 出现 |
| [B.2] | 触发熔断 OPEN（复用 METHOD consumer `/echo` handler） | `trigger_abort >= 1` |
| [B.3] | 维持 OPEN | `keep_abort >= 1` |
| [B.4] | 探活恢复 | `recover_ok == FD_VERIFY_COUNT` |
| [B.5] | 日志佐证 | 探测调度=yes, 状态切换=yes |

> **原理**：METHOD 级资源在 `selectFaultDetectRules` 中调用 `matchMethod` 按 `resource.Path` 精确匹配 `targetService.method`。`/api/protocol/http` 不与 resource.Path（`/echo`）匹配，因此不被选中。

#### 段 C：HTTP Headers 验证

| 步骤 | 动作 | 判定指标 |
|------|------|---------|
| [C.1] | 创建 `http_config.headers=[{key:"X-Health-Probe", value:"true"}]` 的探测规则 | `schedule task` 出现 |
| [C.2] | 等待探测周期，grep provider-a 日志 | `X-Health-Probe=true` 出现在探测请求中 |

> **原理**：`generateHttpRequest` 从 `rule.GetHttpConfig().GetHeaders()` 读取 headers 并设置到 HTTP 请求。provider-a 的 `logIncomingRequest` 已打印完整 headers，grep 即可验证。

### 通过条件

- **段 A**：`has_a1=yes`（拒绝非通配）且 `has_a2=yes`（接受通配）
- **段 B**：`has_b_filter=yes` 且 `trigger_abort>=1` 且 `keep_abort>=1` 且 `recover_ok==FD_VERIFY_COUNT` 且 `探测调度=yes` 且 `状态切换=yes`
- **段 C**：`c_has_schedule=yes` 且 `c_has_header=yes`

三段**全部满足**方为 PASS。

---

## 步骤 6 日志佐证的实现要点

SDK 内部熔断日志**不写到 consumer 应用 stdout**，而是落在各 consumer 独立的 SDK 日志文件：

```
.build/<consumer_name>_run/polaris/log/circuitbreaker/polaris-circuitbreaker.log
```

步骤 6 必须 grep 该文件（而非 consumer 应用日志）：
- 探测调度关键字：**`[CircuitBreaker] schedule task`**（composite/checker.go，探测周期任务真正注册）。
  ⚠️ **不能用宽松的 `[FaultDetect]` / `health check`**：关停日志
  `[FaultDetect] health check for ... is disabled, now stop the previous checker` 同时含这两个词，
  会把"探测被关停"误判为"探测调度=yes"（曾导致 INSTANCE 级假性 yes）。
- 状态切换关键字（**小写连字符**，非驼峰）：`status change: half-open -> close` / `open -> half-open`；
  INSTANCE 级额外要求该行含 `INSTANCE`（区分是 b 实例而非其它资源恢复）。

> SDK 日志（zap+lumberjack）带写缓冲，consumer 运行中关键字可能尚未落盘，步骤 6 对关键字做带超时
> 重试（12 次 × 0.5s，最多约 6s）等待落盘，避免误报 no。

## 验证原理

1. **探测门控**：熔断规则 `faultDetectConfig.enable=true` 是探测启动的必要条件。门控关闭时 SDK 不创建 `ResourceHealthChecker`，不会发起任何探测。
2. **探测调度**：`ResourceHealthChecker` 由 `TaskExecutor` 注入，按 `FaultDetectRule.interval` 周期对匹配实例执行 HTTP GET /echo。
3. **探测上报**：结果经 `doReport(stat, record=false)` 上报，`record=false` 确保不触发实例重新注册（避免干扰 LB）。
4. **恢复判定**：HALF_OPEN 态下探测结果与业务请求共用恢复判定逻辑：连续成功达 `consecutiveSuccess`(1) → CLOSE，任一失败 → OPEN。
5. **SERVICE/METHOD 维持 OPEN**：provider 仍 500，探测 `/echo` 失败，半开态回 OPEN，熔断不会意外恢复。
6. **INSTANCE 单实例故障**：仅 b 故障，b 被路由摘除、业务无感落 a（维持 OPEN 阶段 ok=N）；恢复 b 后 b 重新可选。
7. **D 段 — enable toggle 实时生效**：PUT 熔断规则 `faultDetectConfig.enable=false` → 服务端 push → SDK `OnEvent` → `scheduleHealthCheck` → `realRefreshHealthCheck` 门控重判不通过 → stop checker → 日志 `[FaultDetect] health check ... is disabled, now stop`。再 PUT `enable=true` → 同上链路门控通过 → 重建 checker → 新 `schedule task`。全程不需等周期调度，push 通道延迟在数百毫秒内。
8. **D 段 — interval 热更新**：PUT 探测规则 `interval=5s` → 服务端 push → SDK `OnEvent(EventFaultDetect)` → `scheduleHealthCheck` → `realRefreshHealthCheck` 检测到 `faultDetector.Revision` 变化 → stop 旧 checker + 重建新 checker（新 interval）→ 新 `schedule task` 日志含 `interval=5s`。

---

## 失败诊断

| 现象 | 可能原因 | 排查 |
|------|---------|------|
| SERVICE/METHOD `trigger_abort=0` | provider 未正确置 500；或 burst 次数不够命中阈值 | 检查 provider 日志；增大 `FD_TRIGGER_COUNT` |
| SERVICE/METHOD `keep_abort=0` | sleepWindow 内探测意外成功（provider 被其他进程翻回 200） | 检查 `WAIT_KEEP_OPEN_SECONDS` 是否 > sleepWindow；确认 provider 状态 |
| SERVICE/METHOD `recover_ok < N` | 探测间隔太长或 `WAIT_RECOVER_SECONDS` 不够；或熔断规则误配 ERROR_RATE 导致 close 抖动 | 增大 `WAIT_RECOVER_SECONDS`；确认规则只含 CONSECUTIVE_ERROR |
| INSTANCE `keep_ok < N-1` | b 未被正确摘除（INSTANCE 规则未生效）；或 a 也被误置 500 | 确认只挂 b；检查 INSTANCE 级规则是否下发 |
| INSTANCE 状态切换=no | 日志里无 INSTANCE 级 half-open→close；b 实际未走完恢复 | 看 `polaris-circuitbreaker.log` 中 28082 实例的 status change 链路 |
| `探测调度=no` | grep 错文件（grep 了 consumer stdout 而非 SDK 日志）；或 `faultDetectConfig.enable` 未生效 | 确认 grep 的是 `.build/<name>_run/.../polaris-circuitbreaker.log` |
| 规则创建失败 | Polaris 鉴权未通过或服务不存在 | 检查 `POLARIS_TOKEN`；确认 `CircuitBreakerCallee` 已注册 |
| 端口占用 | 上次脚本未正常退出 | 执行 `./cleanup.sh -f` 后重试 |
| D 段 `disabled_stop=no` | PUT `faultDetectConfig.enable=false` 后 SDK 未收到 push；或规则 id 错导致更新无效 | 检查 `polaris-circuitbreaker.log` 是否有 `[FaultDetect] start to pull fault detect rule`；确认 `cb_rule_id` 正确 |
| D 段 `resume_schedule=no` | PUT `enable=true` 后 schedule task 行数未增加（可能旧 checker 的 stop 未执行或重建失败） | grep SDK 日志确认是否有新 `schedule task`；检查 `WAIT_RULE_READY_SECONDS` 是否够长 |
| D 段 `new_interval=no` | PUT 探测规则 interval 后 SDK 未重建 checker（revision 未变？）；或等待时间不够 | 检查 `polaris-circuitbreaker.log` 是否有 `fault detect rule revision changed`；确认 PUT 是否成功（返回 200） |

## 与 polaris-go 代码改造的对应关系

| 改造点 | 由哪个用例验证 |
|--------|-------------|
| `faultDetectConfig.enable=true` 作为探测门控 | 三级用例步骤 2 规则下发 → 步骤 6 日志佐证 |
| `ResourceHealthChecker` 周期调度（TaskExecutor 注入） | 三级用例步骤 4/6 探测调度=yes |
| `IntervalExecute` 周期任务不退化为一次性 | 步骤 4 维持 OPEN 期间探测持续运行（多周期） |
| `doCheck` 用规则 protocol 选取探测器（非实例 protocol） | 步骤 6 探测调度=yes 且无 `plugin not found` |
| 探测结果 `record=false` 上报 | 步骤 4 探测失败不触发实例重新注册 |
| HALF_OPEN 恢复判定（探测与业务共用） | SERVICE/METHOD 步骤 5 纯等待推动 CLOSE；INSTANCE 步骤 5 b 恢复可选 |
| 三级（SERVICE/METHOD/INSTANCE）规则匹配与状态机 | 三个用例各自的 level 维度 |
| `realRefreshHealthCheck` 门控重判 — enable toggle 实时关闭/开启探测 | **SERVICE 用例 D 段**：`faultDetectConfig.enable` true→false→true，验证 stop/rebuild checker |
| `realRefreshHealthCheck` revision 检测 — 探测规则参数热更新 | **METHOD 用例 D 段**：PUT interval 2s→5s，验证 checker 重建且新 interval 生效 |
| UDP 探测器 `Name()` 返回 `"udp"`（原误返回 `"tcp"` 与 TCP 探测器同名、插件 map 互相覆盖） | 用例五 UDP 探测调度=yes（探测器可被正确选取） |
| UDP 探测改用 `conn.Read` + `SetReadDeadline`（原 `ReadAll` 无 read deadline，对端不回包时永久阻塞占死探测 goroutine） | 用例五 维持 OPEN（探测端口不回包时探测失败而非卡死） |
| UDP `DetectResultImp` 失败置 `Code="-1"`、成功置 `"0"`（原缺 `Code`，探测失败 RetCode 为空、不命中规则 `RET_CODE RANGE 500~599` 被误判成功） | 用例五 维持 OPEN（探测失败正确命中失败哨兵，不误判恢复） |
| `plugin.cfg` 补 `udp : healthcheck/udp`（`plugins.go` 本就 import 了 udp） | 用例五 UDP 探测器注册可用 |
| 探测日志收敛：状态变化立即 INFO、连续失败 30s 定时 INFO、连续成功静默 | 三级用例步骤 4/6 日志佐证（`detect status change` / `detect still failing`） |
| 探测器异常日志收敛：http/tcp/udp 首次 err 打印 Errorf，内容不变静默 | 三级用例维持 OPEN 阶段（探测持续异常不刷屏） |
