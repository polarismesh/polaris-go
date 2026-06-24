# examples/circuitbreaker — 主动探测（Fault Detect）用例清单

本文档罗列 `verify_faultdetect.sh` 执行的全部主动探测验证用例及其判定准则。脚本中所有步骤编号必须与本文一致。

## 测试拓扑

```
                  ┌──── provider-a (端口 28081，默认 200) ────┐
                  │   /echo /health + /switch                   │  一个进程同时注册到五个被调服务：
                  │   TCP 探测端口 28091 / UDP 探测端口 28101    │      CircuitBreakerFDSvcCallee
   Polaris ◀──────┤   （TCP/UDP 探测端口仅 provider-a 启动）       ├──── CircuitBreakerFDMethodCallee
                  │   /echo /health + /switch                   │      CircuitBreakerFDInstanceCallee
                  └──── provider-b (端口 28082，默认 500) ────┘      CircuitBreakerFDTcpCallee
                                       ▲                              CircuitBreakerFDUdpCallee
                                       │
   ┌── fd-consumer (18095) selfService=CircuitBreakerFaultDetectCaller  callee=CircuitBreakerFDSvcCallee
   │     用例一 服务级(SERVICE) HTTP 探测 + D段 toggle 验证
   ├── fd-method-consumer (18096) selfService=CircuitBreakerFDMethodCaller  callee=CircuitBreakerFDMethodCallee
   │     用例二 接口级(METHOD) HTTP 探测 + D段 interval 验证
   ├── fd-instance-consumer (18097) selfService=CircuitBreakerFDInstanceCaller  callee=CircuitBreakerFDInstanceCallee
   │     用例三 实例级(INSTANCE) HTTP 探测（ok 型，单实例故障方案）
   ├── fd-tcp-consumer (18098) selfService=CircuitBreakerFDTcpCaller  callee=CircuitBreakerFDTcpCallee
   │     用例四 TCP 探测（abort 型，SERVICE 级，探测端口 28091）
   ├── fd-udp-consumer (18099) selfService=CircuitBreakerFDUdpCaller  callee=CircuitBreakerFDUdpCallee
   │     用例五 UDP 探测（abort 型，SERVICE 级，探测端口 28101）
   └── 用例六 协议/方法维度 + Headers 验证（复用 18095/18096 consumer，不新增进程）
```

> **五个用例各用独立被调服务**：provider-a / provider-b 两进程通过 `--service` 逗号分隔同时注册到五个服务。
> 独立被调服务可避免被 `verify_circuitbreaker.sh` 用例 10 残留的 `source=*/*` catch-all 规则（无
> `faultDetectConfig`、DELETE 403 删不掉）按 id 字典序抢占导致探测门控误判 disabled。

> **TCP/UDP 探测端口仅 provider-a 启动**：`FaultDetectRule.port` 是单值，`port>0` 时所有被探测实例共用
> 同一端口号，而同机两进程不能绑定同一 TCP/UDP 端口。SERVICE 级闭环不依赖多实例，单实例 provider-a 足够。

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

六个用例各 1 条熔断规则 + 1 条探测规则，共 **12 条规则**。规则名按级别/协议编码隔离：

| 用例 | 熔断规则 | 探测规则 |
|------|----------|----------|
| 服务级(SERVICE)  | `cb-fd-SERVICE-CircuitBreakerFaultDetectCaller` | `fd-fd-SERVICE-CircuitBreakerFaultDetectCaller` |
| 接口级(METHOD)   | `cb-fd-METHOD-CircuitBreakerFDMethodCaller` | `fd-fd-METHOD-CircuitBreakerFDMethodCaller` |
| 实例级(INSTANCE) | `cb-fd-INSTANCE-CircuitBreakerFDInstanceCaller` | `fd-fd-INSTANCE-CircuitBreakerFDInstanceCaller` |
| TCP 探测(SERVICE) | `cb-fd-TCP-CircuitBreakerFDTcpCaller` | `fd-fd-TCP-CircuitBreakerFDTcpCaller` |
| UDP 探测(SERVICE) | `cb-fd-UDP-CircuitBreakerFDUdpCaller` | `fd-fd-UDP-CircuitBreakerFDUdpCaller` |
| 协议/方法验证 | 复用 SERVICE/METHOD consumer 已有规则 | 按段创建/更新 method 过滤和 headers 探测规则 |

熔断规则公共配置：`CONSECUTIVE_ERROR=5`，`sleepWindow=6s`，`consecutiveSuccess=1`，
**`faultDetectConfig.enable=true`**（探测门控），`error_conditions=RET_CODE RANGE 500~599`，
**仅 CONSECUTIVE_ERROR、不叠加 ERROR_RATE**（避免恢复抖动）。

HTTP 探测规则公共配置（用例一/二/三）：`protocol=HTTP`(1)，`GET /echo`，`interval=2s`，
`timeout=1000ms`，`port=0`（使用被探测实例自身注册的端口）。

TCP/UDP 探测规则配置（用例四/五）：`protocol=TCP`(2) / `UDP`(3)，用 `tcp_config` / `udp_config`
（均为 `{send, receive[]}`，`send="ping"`、`receive="tcp-ok"`/`"udp-ok"` 做内容匹配判健康），
`interval=2s`，`timeout=1000ms`，`port` 固定指向 provider-a 的独立探测端口（TCP=28091 / UDP=28101）。

> **探测规则 protocol 字段是数字枚举**：1=HTTP / 2=TCP / 3=UDP。HTTP 用 `http_config`，TCP 用
> `tcp_config`，UDP 用 `udp_config`。TCP/UDP 的 config 都是 `{send, receive[]}`：探测器发送 `send`、
> 收到对端响应后与 `receive` 列表做内容匹配，匹配则判探测成功。

### 各级别的匹配维度差异

| 级别 | 熔断规则 `level` | `destination.method` | `BlockConfig.api.path` | 探测规则 `target_service.method` |
|------|------|------|------|------|
| 服务级(SERVICE)  | `SERVICE`  | `*` | 空 | `*` |
| 接口级(METHOD)   | `METHOD`   | `/echo` | `/echo` | `/echo`（与熔断、业务三者方法维度同源） |
| 实例级(INSTANCE) | `INSTANCE` | `*` | 空 | `*` |

### 三个级别的语义差异

| 级别 | 熔断 OPEN 时行为 | 业务请求 | 判定指标类型 |
|------|------|------|------|
| SERVICE | `commonCheck` 前置拦截 | `call aborted`（503） | abort 型 |
| METHOD | `commonCheck` 前置拦截 | `call aborted`（503） | abort 型 |
| INSTANCE | 路由层摘除实例、LB 不选 | 流量转移到健康实例（200） | ok 型 |

- **SERVICE / METHOD 级**：经 `AcquirePermission`，熔断 OPEN 时业务请求直接 `abort`。
- **INSTANCE 级**：**不**经 `AcquirePermission`，某实例 OPEN 时由 FilterOnlyRouter 将其从可选列表摘除，
  业务请求落到其余健康实例返回 200，**不是 abort**。

## 公共流程

每次运行 `verify_faultdetect.sh`：

1. 步骤 A：环境准备（检查 Go/python3/curl/lsof，创建 `.build/`、`.logs/`）
2. 步骤 B：启动 provider-a + provider-b（provider-a 额外启动 TCP/UDP 探测端口）
3. 步骤 C：依次执行六个用例（受 `RUN_FD_CASES` 控制，默认
   `service,method,instance,tcp,udp,proto_method` 全跑；未选中的用例标记 SKIP，不计入失败）
4. 步骤 D：结果汇总，trap EXIT 时：**熔断规则删除**；**探测规则保留不删**（FaultDetectRule 无
   `enable` 字段，改为跨运行幂等复用，下次存在则 PUT 更新复用），停止 consumer 与 provider 进程

---

## 用例一 / 用例二：服务级(SERVICE) / 接口级(METHOD) HTTP 探测闭环（abort 型）

SERVICE 与 METHOD 级都经 `AcquirePermission`，熔断 OPEN 时业务请求 `abort`，闭环结构一致，
脚本中共用辅助函数 `_run_probe_abort_case`。两者区别仅在规则匹配维度：METHOD 级把
`destination.method`、`BlockConfig.api.path`、探测 `target_service.method` 都限定到 `/echo`。

### Caller 写法
- 复用 `newCircuitBreakerCaller/consumer`（Decorator 写法）
- 两个用例各自独立 consumer 端口 18095 / 18096

### 规则
- 熔断规则：SERVICE 级 `level=SERVICE`，METHOD 级 `level=METHOD`
- METHOD 级额外限定 `destination.method.value=/echo`，`BlockConfig.api.path=/echo`，探测规则 `target_service.method=/echo`
- 公共：`CONSECUTIVE_ERROR=5`，`sleepWindow=6s`，`faultDetectConfig.enable=true`，**仅 CONSECUTIVE_ERROR 不叠加 ERROR_RATE**
- 探测规则：`protocol=HTTP`(1)，`GET /echo`，`interval=2s`，`timeout=1000ms`，`port=0`

### 验证步骤

| 步骤 | 动作 | 判定指标 |
|------|------|---------|
| [1] 环境复位 | provider-a/b 均置 200 | — |
| [2] 启动 consumer + 下发规则 | 启动独立 consumer；创建熔断规则（含 `faultDetectConfig.enable=true`）+ 探测规则；等待 `WAIT_RULE_READY_SECONDS`(6s) | — |
| [3] 触发熔断 OPEN | provider-a/b 均置 500，burst `FD_TRIGGER_COUNT`(30) 次 `/echo` | `trigger_abort >= 1` |
| [4] 维持 OPEN | provider 保持 500，等 `WAIT_KEEP_OPEN_SECONDS`(12s, > sleepWindow)，burst `FD_VERIFY_COUNT`(10) | `keep_abort >= 1`（探测失败不恢复） |
| [5] 探活恢复 | provider-a/b 翻回 200，等 `WAIT_RECOVER_SECONDS`(16s)，**不主动制造业务压力推动**，burst 验证 | `recover_ok == FD_VERIFY_COUNT`（恢复 CLOSE） |
| [6] 日志佐证 | grep SDK 熔断日志确认探测调度 + 状态切换 | `探测调度=yes` 且 `状态切换=yes` |
| **D 段** | **（仅 SERVICE 用例）enable toggle 验证** | |
| [7] 关闭探测 | PUT 熔断规则 `faultDetectConfig.enable=false`，等待 SDK 感知规则变更 | `disabled_stop=yes`（日志含 `[FaultDetect] health check for resource=... is disabled, now stop`） |
| [8] 重新开启探测 | PUT 熔断规则 `faultDetectConfig.enable=true`，等待 SDK 感知规则变更 | `resume_schedule=yes`（`schedule task` 行数增加） |
| **D 段** | **（仅 METHOD 用例）规则参数热更新验证** | |
| [7] 修改探测间隔 | PUT 探测规则 `interval=5s`（原 2s），等待 SDK 感知规则变更 | `new_interval=yes`（日志出现 `schedule task ... interval=5s`） |

### 通过条件

**A-C 段**：`trigger_abort >= 1` 且 `keep_abort >= 1` 且 `recover_ok == FD_VERIFY_COUNT` 且
`探测调度=yes` 且 `状态切换=yes`。

**D 段（SERVICE — toggle）**：`disabled_stop=yes` 且 `resume_schedule=yes`。

**D 段（METHOD — interval）**：`new_interval=yes`。

A-C 段与 D 段**全部满足**方为 PASS，任一失败即为 FAIL。D 段失败不影响 A-C 段判定结果（但整体仍报 FAIL）。

### 验证原理
- **探测门控**：熔断规则 `faultDetectConfig.enable=true` 是探测启动门控。门控关闭时 `realRefreshHealthCheck`（`rule.go:131`）判定不通过 → stop checker，不创建 `ResourceHealthChecker`。
- **探测调度**：`ResourceHealthChecker` 由 `TaskExecutor` 注入 `IntervalExecute` 周期调度，按 `FaultDetectRule.interval` 对匹配实例执行 HTTP GET `/echo`。
- **探测上报**：结果经 `doReport(stat, record=false)` 上报，`record=false` 确保不触发实例重新注册（避免干扰 LB）。
- **HALF_OPEN 恢复判定**：探测结果与业务请求共用恢复逻辑——连续成功达 `consecutiveSuccess`(1) → `HALF_OPEN → CLOSE`，任一失败 → OPEN。
- **步骤 5 纯探测推动 CLOSE**：provider 翻回 200 后脚本不主动发业务请求，等待 `WAIT_RECOVER_SECONDS`(16s) 让探测周期 `interval=2s` 经历多个周期探活成功，推动 `half-open → close`——这验证了"恢复由主动探测推动，而非业务请求探活"。
- **D 段 enable toggle**：PUT `faultDetectConfig.enable=false` → 服务端 push → SDK `OnEvent` → `scheduleHealthCheck` → `realRefreshHealthCheck` 门控重判不通过 → stop checker → 日志 `[FaultDetect] health check ... is disabled, now stop`。再 PUT `enable=true` → 同上链路门控通过 → 重建 checker → 新 `schedule task`。全程 push 延迟在数百毫秒内。
- **D 段 interval 热更新**：PUT 探测规则 `interval=5s` → 服务端 push → SDK 检测到 `faultDetector.Revision` 变化 → stop 旧 checker + 重建新 checker（新 interval）→ 新 `schedule task` 日志含 `interval=5s`。

---

## 用例三：实例级(INSTANCE) HTTP 探测闭环（ok 型，单实例故障方案）

INSTANCE 级**不经过 AcquirePermission**：某实例 OPEN 时被服务路由层摘除、LB 不选它，业务请求落到
其余健康实例返回 200，**不是 abort**。因此判定指标与 abort 型完全不同，采用「单实例故障」方案：
只挂 provider-b，观察"b 被摘除 → b 恢复可选"的完整闭环。

### Caller 写法
- 复用 `newCircuitBreakerCaller/consumer`（Decorator 写法），端口 18097

### 规则
- `level=INSTANCE`，`name=cb-fd-INSTANCE-CircuitBreakerFDInstanceCaller`
- `CONSECUTIVE_ERROR=5`，`sleepWindow=6s`，`faultDetectConfig.enable=true`
- 探测规则：`protocol=HTTP`(1)，`GET /echo`，`interval=2s`，`port=0`

### 验证步骤

| 步骤 | 动作 | 判定指标 |
|------|------|---------|
| [1] 环境复位 | provider-a/b 均置 200 | — |
| [2] 启动 consumer + 下发规则 | 启动 fd_instance_consumer(18097)；下发 INSTANCE 级熔断规则 + 探测规则；等待规则就绪 | — |
| [3] 触发 b 实例 OPEN | **仅 provider-b 置 500**（a 保持 200），burst `FD_TRIGGER_COUNT`(30) 次 | `trigger_fail >= 1`（b 被选中时 500，**不要求 abort**） |
| [4] 维持 OPEN(b 被摘除) | b 保持 500，等 `WAIT_KEEP_OPEN_SECONDS`(12s)，burst 验证 | `keep_ok >= FD_VERIFY_COUNT-1`（业务落 a，容忍 1 次抖动） |
| [5] 探活恢复(b 重新可选) | provider-b 翻回 200，等 `WAIT_RECOVER_SECONDS`(16s)，burst 验证 | `recover_ok >= FD_VERIFY_COUNT-1`（b 恢复可选） |
| [6] 日志佐证 | grep SDK 熔断日志确认探测调度 + **INSTANCE 级** half-open→close | `探测调度=yes` 且 `INSTANCE状态切换=yes` |

### 通过条件（5 项全满足）
`trigger_fail >= 1` 且 `keep_ok >= FD_VERIFY_COUNT-1` 且 `recover_ok >= FD_VERIFY_COUNT-1` 且
`探测调度=yes` 且 `INSTANCE状态切换=yes`。

### 验证原理
- **INSTANCE 级不进 `commonCheck`**（`circuitbreaker_flow.go:244`），OPEN 实例不被前置拦截，由 FilterOnlyRouter 从可选实例集中摘除。
- **维持/探活 ok 容忍 1 次抖动**：50/50 LB 下 b 刚 OPEN 的瞬间可能有 1 个在途请求仍命中 b。
- **INSTANCE 级恢复**：b 进入 half-open 态后，SDK 放行业务请求去探活 b，因此 b 的最终 close 通常由 half-open 态业务请求探活推动；主动探测在 b OPEN 期间也持续调度（探测调度=yes 佐证）。
- **不用 provider-b 探测请求增量作铁证**：provider 为多用例共享，计数会串扰。以 SDK 日志中 **INSTANCE 级 b 实例 half-open→close** 作为状态机恢复的铁证。

---

## 用例四 / 用例五：TCP / UDP 探测闭环（abort 型，单实例 provider-a）

TCP / UDP 用例都是 **SERVICE 级熔断规则（abort 型）**，闭环结构与 HTTP SERVICE 用例一致，脚本中共用
辅助函数 `_run_probe_tcpudp_case`。与 HTTP SERVICE 用例的区别：

- **探测协议**：TCP(2) / UDP(3)，探测打 provider-a 的**独立探测端口**（TCP=28091 / UDP=28101），用
  `send="ping"`、`receive="tcp-ok"`/`"udp-ok"` 做内容匹配判健康。
- **业务故障与探测故障分两个开关**：业务故障（触发熔断 OPEN）仍用 HTTP `/echo` 的 `openError` 开关；
  探测故障（维持 OPEN）用 provider 的 `openTcpError` / `openUdpError` 开关（让 TCP/UDP 探测端口不回包 → 探测失败）。
- **单实例 provider-a**：`FaultDetectRule.port` 单值、同机两进程不能绑同一 TCP/UDP 端口，探测规则
  `port` 固定指向 a 的探测端口。SERVICE 级闭环不依赖多实例，单实例足够。

### Caller 写法
- 复用 `newCircuitBreakerCaller/consumer`（Decorator 写法），端口 18098 / 18099

### 规则
- 熔断规则：`level=SERVICE`，`CONSECUTIVE_ERROR=5`，`sleepWindow=6s`，`faultDetectConfig.enable=true`
- TCP 探测规则：`protocol=TCP`(2)，`port=28091`，`tcp_config={send:"ping", receive:["tcp-ok"]}`，`interval=2s`，`timeout=1000ms`
- UDP 探测规则：`protocol=UDP`(3)，`port=28101`，`udp_config={send:"ping", receive:["udp-ok"]}`，`interval=2s`，`timeout=1000ms`

### 验证步骤

| 步骤 | 动作 | 判定指标 |
|------|------|---------|
| [1] 环境复位 | provider-a/b 的 `/echo` 置 200；TCP/UDP 探测端口正常回包 | — |
| [2] 启动 consumer + 下发规则 | 启动独立 consumer（TCP=18098 / UDP=18099）；创建 SERVICE 级熔断规则 + TCP/UDP 探测规则；等待 `WAIT_RULE_READY_SECONDS`(6s) | — |
| [3] 触发熔断 OPEN | provider-a/b 的 `/echo` 置 500，burst `FD_TRIGGER_COUNT`(30) 次 | `trigger_abort >= 1` |
| [4] 维持 OPEN | `/echo` 保持 500 **且** 探测端口故障（`openTcpError`/`openUdpError` 让探测端口不回包），等 `WAIT_KEEP_OPEN_SECONDS`(12s)，burst 验证 | `keep_abort >= 1`（探测失败不恢复） |
| [5] 探活恢复 | `/echo` 与探测端口都恢复正常，等 `WAIT_RECOVER_SECONDS`(16s)，主动探测探活推动 close，burst 验证 | `recover_ok == FD_VERIFY_COUNT`（恢复 CLOSE） |
| [6] 日志佐证 | grep SDK 熔断日志确认探测调度 + 状态切换 | `探测调度=yes` 且 `状态切换=yes` |

### 通过条件（5 项全满足）
`trigger_abort >= 1` 且 `keep_abort >= 1` 且 `recover_ok == FD_VERIFY_COUNT` 且 `探测调度=yes` 且 `状态切换=yes`。

### 验证原理
- **维持 OPEN 需业务 500 + 探测端口故障双重保证**：SERVICE 级 half-open 会放行业务请求探活，若维持阶段业务 `/echo` 恢复 200，业务请求会抢先推动 close。故维持阶段让业务 `/echo` 仍 500、同时让 TCP/UDP 探测端口故障，双重保证熔断不被意外恢复。恢复阶段则业务与探测端口同时转绿。
- **TCP 探测器**：`doTCPDetect` 通过 `conn.SetDeadline(timeout)` 设置读写超时，`ioutil.ReadAll(conn)` 读对端回包（TCP 有 EOF 故 ReadAll 自然结束），内容匹配 `receive` 列表判健康。
- **UDP 探测器**：`doUDPDetect` 通过 `conn.Write(send)` 发探测包，`conn.SetReadDeadline(timeout)` 设读超时，**单次 `conn.Read(buf)`** 读一个 UDP 响应包（不用 `ReadAll`，UDP 无 EOF、ReadAll 必等满 deadline 且返回 timeout err 丢弃已收到的响应），内容匹配 `receive` 列表判健康。
- **UDP 探测器潜伏 bug**：此前 UDP 探测器从未被真正调用过，潜伏 4 个 SDK bug——`Name()` 误返回 `"tcp"` 与 TCP 探测器撞名导致插件 map 覆盖；`ReadAll` 无 read deadline 导致对端不回包时永久阻塞拖死 SDK worker goroutine；`DetectResultImp` 缺 `Code` 哨兵导致探测失败被误判成功；`ReadAll+deadline` 把已收到的正确响应也丢弃。这些 bug 在加入 TCP/UDP 探测用例后逐个暴露并修复。

---

## 用例六：协议/方法维度 + HTTP Headers 验证

验证 `selectFaultDetectRules` 对 `target_service.method` 的过滤正确性，以及 `generateHttpRequest`
对 `http_config.headers` 的设置正确性。**复用已有 SERVICE consumer（18095）和 METHOD consumer（18096），不新增进程。**

### Caller 写法
- 段 A/C 复用 SERVICE consumer（端口 18095）
- 段 B 复用 METHOD consumer（端口 18096）

### 验证步骤

#### 段 A：SERVICE 级 method 过滤

| 步骤 | 动作 | 判定指标 |
|------|------|---------|
| [A.1] | 创建 `method.value=/api/protocol/http` 的探测规则（非通配） | `schedule task` 行数不变（SERVICE 级拒绝非通配 method） |
| [A.2] | 创建 `method.value=*` 的探测规则（通配） | `schedule task` 行数增加（SERVICE 级接受通配 method） |

#### 段 B：METHOD 级 method 过滤 + 完整闭环

| 步骤 | 动作 | 判定指标 |
|------|------|---------|
| [B.1] | 创建 `method.value=/echo` + `method.value=/api/protocol/http` 两条探测规则 | 只有 `/echo` 的 `schedule task` 出现 |
| [B.2] | 触发熔断 OPEN（复用 METHOD consumer `/echo` handler） | `trigger_abort >= 1` |
| [B.3] | 维持 OPEN | `keep_abort >= 1` |
| [B.4] | 探活恢复 | `recover_ok == FD_VERIFY_COUNT` |
| [B.5] | 日志佐证 | 探测调度=yes, 状态切换=yes |

#### 段 C：HTTP Headers 验证

| 步骤 | 动作 | 判定指标 |
|------|------|---------|
| [C.1] | PUT 更新已有探测规则添加 `http_config.headers=[{key:"X-Health-Probe", value:"true"}]` | `schedule task` 出现 |
| [C.2] | 通过 HTTP API 查询规则 body 确认含 headers；等待探测周期后 grep provider-a 日志作为辅助验证 | `c_api_has_header=yes`（主验证）；provider 日志中 `X-Health-Probe=true` 出现在探测请求中（辅助） |

### 通过条件
- **段 A**：`has_a1=yes`（拒绝非通配）且 `has_a2=yes`（接受通配）
- **段 B**：`has_b_filter=yes` 且 `trigger_abort>=1` 且 `keep_abort>=1` 且 `recover_ok==FD_VERIFY_COUNT` 且 `探测调度=yes` 且 `状态切换=yes`
- **段 C**：`c_has_schedule=yes` 且 `c_api_has_header=yes`（主验证：HTTP API 查询规则 body 含 headers；provider 日志为辅助，不作为硬性条件）

三段**全部满足**方为 PASS。

### 验证原理
- **段 A SERVICE 级 method 过滤**：`selectFaultDetectRules` 对 SERVICE 级资源只接受 `targetService.method.value` 为空或 `*` 的探测规则（`match.IsMatchAll`），非通配值会被过滤——因此 `schedule task` 行数不变。
- **段 B METHOD 级 method 过滤**：METHOD 级资源调用 `matchMethod` 按 `resource.Path` 精确匹配 `targetService.method`——`/api/protocol/http` 不与 `/echo` 匹配，因此不被选中。
- **段 C HTTP headers**：`generateHttpRequest`（`http.go`）从 `rule.GetHttpConfig().GetHeaders()` 读取 headers 并设置到 HTTP 探测请求上。`X-Health-Probe=true` header 只会出现在主动探测请求上（业务转发带的是 `X-Request-ID`），因此在 provider 端可区分。

---

## 日志佐证的实现要点

SDK 内部熔断日志**不写到 consumer 应用 stdout**，而是落在各 consumer 独立的 SDK 日志文件：

```
.build/<consumer_name>_run/polaris/log/circuitbreaker/polaris-circuitbreaker.log
```

步骤 6 必须 grep 该文件（而非 consumer 应用日志）：
- 探测调度关键字：**`[CircuitBreaker] schedule task`**（`checker.go`，探测周期任务真正注册）。
  不能用宽松的 `[FaultDetect]` / `health check`：关停日志
  `[FaultDetect] health check for ... is disabled, now stop the previous checker` 同时含这两个词，
  会把"探测被关停"误判为"探测调度=yes"。
- 状态切换关键字（**小写连字符**，非驼峰）：`status change: half-open -> close` / `open -> half-open`；
  INSTANCE 级额外要求该行含 `INSTANCE`（区分是 b 实例而非其它资源恢复）。

> SDK 日志（zap+lumberjack）带写缓冲，consumer 运行中关键字可能尚未落盘，步骤 6 对关键字做带超时
> 重试（12 次 × 0.5s，最多约 6s）等待落盘，避免误报 no。

---

## 失败诊断

| 现象 | 可能原因 | 排查 |
|------|---------|------|
| SERVICE/METHOD `trigger_abort=0` | provider 未正确置 500；或 burst 次数不够命中阈值 | 检查 provider 日志；增大 `FD_TRIGGER_COUNT` |
| SERVICE/METHOD `keep_abort=0` | sleepWindow ���探测意外成功（provider 被其他进程翻回 200） | 检查 `WAIT_KEEP_OPEN_SECONDS` 是否 > sleepWindow；确认 provider 状态 |
| SERVICE/METHOD `recover_ok < N` | 探测间隔太长或 `WAIT_RECOVER_SECONDS` 不够；或熔断规则误配 ERROR_RATE 导致 close 抖动 | 增大 `WAIT_RECOVER_SECONDS`；确认规则只含 CONSECUTIVE_ERROR |
| INSTANCE `keep_ok < N-1` | b 未被正确摘除（INSTANCE 规则未生效）；或 a 也被误置 500 | 确认只挂 b；检查 INSTANCE 级规则是否下发 |
| INSTANCE 状态切换=no | 日志里无 INSTANCE 级 half-open→close；b 实际未走完恢复 | 看 `polaris-circuitbreaker.log` 中 28082 实例的 status change 链路 |
| `探测调度=no` | grep 错文件（grep 了 consumer stdout 而非 SDK 日志）；或 `faultDetectConfig.enable` 未生效 | 确认 grep 的是 `.build/<name>_run/.../polaris-circuitbreaker.log` |
| 规则创建失败 | Polaris 鉴权未通过或服务不存在 | 检查 `POLARIS_TOKEN`；确认被调服务已注册 |
| 端口占用 | 上次脚本未正常退出 | 执行 `./cleanup.sh -f` 后重试 |
| D 段 `disabled_stop=no` | PUT `faultDetectConfig.enable=false` 后 SDK 未收到 push；或规则 id 错导致更新无效 | 检查 `polaris-circuitbreaker.log` 是否有 `[FaultDetect] start to pull fault detect rule`；确认 `cb_rule_id` 正确 |
| D 段 `resume_schedule=no` | PUT `enable=true` 后 schedule task 行数未增加 | grep SDK 日志确认是否有新 `schedule task`；检查 `WAIT_RULE_READY_SECONDS` 是否够长 |
| D 段 `new_interval=no` | PUT 探测规则 interval 后 SDK 未重建 checker | 检查是否有 `fault detect rule revision changed`；确认 PUT 是否成功（返回 200） |

## 与 polaris-go 代码改造的对应关系

| 改造点 | 由哪个用例验证 |
|--------|-------------|
| `faultDetectConfig.enable=true` 作为探测门控 | 全部用例步骤 2 规则下发 → 步骤 6 日志佐证 |
| `ResourceHealthChecker` 周期调度（TaskExecutor 注入） | 全部用例步骤 4/6 探测调度=yes |
| `IntervalExecute` 周期任务不退化为一次性 | 步骤 4 维持 OPEN 期间探测持续运行（多周期） |
| `doCheck` 用规则 protocol 选取探测器（非实例 protocol） | 步骤 6 探测调度=yes 且无 `plugin not found` |
| 探测结果 `record=false` 上报 | 步骤 4 探测失败不触发实例重新注册 |
| HALF_OPEN 恢复判定（探测与业务共用） | SERVICE/METHOD 步骤 5 纯等待推动 CLOSE；INSTANCE 步骤 5 b 恢复可选 |
| 三级（SERVICE/METHOD/INSTANCE）规则匹配与状态机 | 三个用例各自的 level 维度 |
| `realRefreshHealthCheck` 门控重判 — enable toggle 实时关闭/开启探测 | **SERVICE 用例 D 段**：`faultDetectConfig.enable` true→false→true，验证 stop/rebuild checker |
| `realRefreshHealthCheck` revision 检测 — 探测规则参数热更新 | **METHOD 用例 D 段**：PUT interval 2s→5s，验证 checker 重建且新 interval 生效 |
| `selectFaultDetectRules` method 过滤 | 用例六 段 A（SERVICE 拒绝非通配）/ 段 B（METHOD 按 path 精确匹配） |
| `generateHttpRequest` headers 设置 | 用例六 段 C（`X-Health-Probe=true` header 出现在探测请求） |
| UDP 探测器 `Name()` 返回 `"udp"`（原误返回 `"tcp"` 与 TCP 探测器同名） | 用例五 UDP 探测调度=yes（探测器可被正确选取） |
| UDP 探测改用 `conn.Read` + `SetReadDeadline` | 用例五 维持 OPEN（探测端口不回包时探测失败而非卡死） |
| UDP `DetectResultImp` 失败置 `Code="-1"` | 用例五 维持 OPEN（探测失败正确命中失败哨兵，不误判恢复） |
| 探测日志收敛：状态变化立即 INFO、连续失败 30s 定时 INFO、连续成功静默 | 全部用例步骤 4/6 日志佐证 |
| 探测器异常日志收敛：http/tcp/udp 首次 err 打印 Errorf，内容不变静默 | 维持 OPEN 阶段（探测持续异常不刷屏） |
