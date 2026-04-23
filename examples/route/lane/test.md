# 全链路灰度泳道测试方案

本文档汇总 `examples/route/lane/` 目录下两个端到端测试脚本的**测试方案（目标、前置条件、拓扑、用例、校验点）**，便于在 Polaris 服务端 + polaris-go SDK 环境下做路由/预热回归验证。

- 测试脚本：
  - [lane-test.sh](./lane-test.sh)：**泳道路由**功能测试（baseline / gray 隔离、流量匹配 STRICT/PERMISSIVE、baseLaneMode、泳道组剔除等）
  - [lane-warmup-test.sh](./lane-warmup-test.sh)：**泳道预热（Warmup）**功能测试（按概率曲线灰度放量）

两个脚本使用相同端口段（`48095 / 19080-19091` 等），**不可同时运行**，需先 `stop` 或 `./cleanup.sh` 后再切换。

---

## 一、通用信息

| 项 | 值 |
| --- | --- |
| 命名空间 | `default` |
| 入口网关服务 | 主脚本：`LaneRouterGateway`；warmup 脚本：`LaneRouterGatewayService` |
| Provider 服务 | `LaneEchoServer`；`baseLaneMode=1` 专项使用 `StableLaneEchoServer` |
| Consumer 服务 | `LaneEchoClient` / `SimpleLaneEchoClient` |
| 染色 Header（直接染色） | `service-lane: <laneGroup>/<laneRule>`（如 `lane-go-example/gray`、`lane-go-warmup/gray`） |
| 流量匹配 Header | 主脚本：`user: gray` / `user: noexist` / `user: other`；warmup 脚本：`warmup-user: gray` |
| 支持的命令 | `all` / `build` / `check` / `start` / `test` / `stop` |
| 典型启动 | `./lane-test.sh all 127.0.0.1`、`./lane-warmup-test.sh all 127.0.0.1` |

通用流程：`构建二进制 → 校验 Polaris 泳道规则 → 启动服务 → 等待就绪 → 执行用例 → 汇总统计 → 停止服务`。

---

## 二、`lane-test.sh` —— 泳道路由测试方案

### 2.1 测试目标
验证 polaris-go SDK 的 `laneRouter` 在不同链路组合下对染色请求 / 基线请求的路由正确性，覆盖：
1. 无 Header → 走 baseline
2. `service-lane` 直接染色 → 走 gray 泳道
3. 流量匹配规则 `STRICT` 模式命中染色
4. 流量匹配规则 `PERMISSIVE` 模式回退基线
5. 规则未命中 → 走 baseline
6. 并发下 baseline 与 gray 路径相互隔离
7. `baseLaneMode=ExcludeEnabledLaneInstance (=1)` 时 baseline 只走未启用泳道实例
8. 服务不在泳道组内（破坏性）→ 仅走无标签实例

### 2.2 前置条件（Polaris 控制台配置）
- 泳道组：`lane-go-example`
- 入口服务：`LaneRouterGateway / default`
- 目标服务：`LaneEchoClient`、`SimpleLaneEchoClient`、`LaneEchoServer`、`StableLaneEchoServer`
- 规则 `gray`：Header `user=gray` → `lane=gray`，匹配模式 `STRICT`，`enable=true`
- 规则 `permissive`：Header `user=noexist` → `lane=noexist`（无实例），匹配模式 `PERMISSIVE`，`enable=true`

脚本中 `validate_lane_rules()` 会自动拉取并校验以上配置；若不满足则终止测试。

### 2.3 拓扑与端口

服务端口（来自脚本配置区）：

| 角色 | base | gray / 其他 |
| --- | --- | --- |
| `gateway` | `:48095` | — |
| `simple-gateway` | `:48096` | — |
| `gateway-excl`（baseLaneMode=1 专用） | `:48097` | — |
| `LaneEchoClient` | `:19080` | `:19081` |
| `SimpleLaneEchoClient` | `:19082` | `:19083` |
| `LaneEchoServer` | `:19090` | `:19091` |
| `StableLaneEchoServer` | `:19092`（stable） | `:19093`（gray） |

```
                ┌───────────────────────────┐
         HTTP   │ gateway        :48095     │ ┐
请求 ─────────►│ simple-gateway :48096     │ │ consumer 层
                │ gateway-excl   :48097     │ │ (base / gray)
                └─────────┬─────────────────┘ │    :19080 / :19081
                          ▼                     │ simple-consumer
                ┌───────────────────────────┐  │    :19082 / :19083
                │ LaneEchoClient           │◄─┘
                │ SimpleLaneEchoClient     │
                └─────────┬─────────────────┘
                          ▼
                ┌───────────────────────────┐
                │ LaneEchoServer            │ :19090(base) / :19091(gray)
                │ StableLaneEchoServer      │ :19092(stable) / :19093(gray)
                └───────────────────────────┘
```

四条被测链路，每条均跑 6 个基础用例（编号与脚本 `log_title` 保持一致）：

| 链路编号 | 链路标签 | Gateway | Consumer | Provider |
| --- | --- | --- | --- | --- |
| 1.x | 主链路 | `gateway` | `LaneEchoClient` | `LaneEchoServer` |
| 2.x | `[simple]` | `simple-gateway` | `LaneEchoClient` | `LaneEchoServer` |
| 3.x | `[gw→sc]` | `gateway` | `SimpleLaneEchoClient` | `LaneEchoServer` |
| 4.x | `[sg→sc]` | `simple-gateway` | `SimpleLaneEchoClient` | `LaneEchoServer` |

外加两个专项链路：
- **用例 5.1**：`gateway-excl` → `StableLaneEchoServer`（脚本把 `polaris.yaml` 中 `baseLaneMode: 0` 替换为 `baseLaneMode: 1`）
- **用例 6.1**：破坏性 —— 运行期调用 Polaris OpenAPI 把 `LaneEchoServer` 从泳道组 `destinations` 中移除，结束后自动恢复

### 2.4 用例矩阵

#### 2.4.1 基础 6 用例（每条链路各执行一次，编号为 `<链路>.<序号>`）

| 序号 | 用例 | 请求条件 | 预期路由 | 关键校验点 |
| --- | --- | --- | --- | --- |
| `x.1` | 无 Header 基线 | 无染色 Header | 全链路 `lane=(baseline)` | 响应中 `lane=(baseline)` |
| `x.2` | `service-lane` 直接染色 | `service-lane: lane-go-example/gray` | Provider 走 `:19091` `lane=gray` | 响应含 `lane=gray` 且端口匹配 |
| `x.3` | 流量匹配 STRICT | `user: gray` | `lane=gray` | 必须命中 `gray` 实例；未命中即失败 |
| `x.4` | 流量匹配 PERMISSIVE | `user: noexist`（目标 lane 无实例） | 回退 baseline | 响应 `lane=(baseline)` |
| `x.5` | 未命中规则 | `user: other` | baseline | 响应 `lane=(baseline)` |
| `x.6` | 泳道隔离并发 | 多轮交替发 baseline / gray 请求 | 两路径互不污染 | N 轮循环统计零泄漏 |

> x ∈ {1, 2, 3, 4}，共 **24 条基础用例**。

#### 2.4.2 专项用例

| 编号 | 用例 | 说明 | 关键校验点 |
| --- | --- | --- | --- |
| **5.1a** | `baseLaneMode=ExcludeEnabledLaneInstance`（无染色） | `gateway-excl` → `StableLaneEchoServer`（仅有 `stable` + `gray` 两种已打标签实例，**没有无标签实例**） | baseline 全部路由到 `lane=stable :19092`；不允许路由到启用集合里的 `gray`；出现负载均衡即失败 |
| **5.1b** | `baseLaneMode=ExcludeEnabledLaneInstance`（染色） | 同上，带 `service-lane: lane-go-example/gray` | 染色请求正确路由到 `lane=gray :19093`，`baseLaneMode` 不影响染色路径 |
| **6.1a** | 服务不在泳道组内（破坏性，染色请求） | 通过 OpenAPI 将 `LaneEchoServer` 从泳道组 `destinations` 中移除 | 染色请求 provider 全部路由到无标签 base 实例 `:19090`；默认模式下不得命中带标签实例 |
| **6.1b** | 服务不在泳道组内（破坏性，无 Header） | 同上 | 无 Header 请求同样路由到 base 实例 `:19090` |
| **6.1 SKIP** | Polaris 缓存传播超时 | 管理 API 已确认移除，但 SDK 本地缓存尚未刷新 | 判定为非 SDK 问题，标记为 SKIP 而非 FAIL |

> 用例 6.1 结束时会把 `LaneEchoServer` 重新加回泳道组（兼容别名写回），避免影响后续运行。

### 2.5 执行与结果
- 日志：`./.logs/lane-test-<时间戳>.log`，同时各服务独立写 `./.logs/<role>.log`
- 汇总：脚本末尾输出 `TOTAL_PASS / TOTAL_FAIL / TOTAL_COUNT`；任一 `fail` 即整体失败，退出码非 0

---

## 三、`lane-warmup-test.sh` —— 泳道预热测试方案

### 3.1 测试目标
验证 `laneRouter` 的 `WARMUP` 染色类型是否按以下公式做概率放量：

```
probability = pow(uptime / warmup_interval, curvature) * 100%
uptime = now - etime        // etime 为规则最近一次启用时间
当 uptime >= interval 时 probability = 100%
```

通过统计响应中 `lane=gray` 的比例，验证预热曲线在三个阶段的正确性。

### 3.2 前置条件
- 泳道组：`lane-go-warmup`（与 `lane-go-example` 互相隔离）
- 入口服务：`LaneRouterGatewayService / default`
- 目标服务：`LaneEchoServer`
- 规则 `gray`：
  - `default_label_value: gray`
  - `traffic_gray.type: WARMUP`
  - `traffic_gray.warmup_interval: 60`（建议 ≤ `MAX_WARMUP_WAIT=120` 的短区间，便于测试）
  - `traffic_gray.curvature: 2`
  - Header 匹配：**必须**使用 `warmup-user=gray`（避免与 `lane-go-example` 的 `user=gray` 冲突）
  - `enable: true`

脚本关键全局变量（见文件头部配置区）：

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `WARMUP_INTERVAL` | `120` | 初始占位值，由 `validate_lane_rules()` 从 Polaris 规则覆盖为 `traffic_gray.warmup_interval` |
| `WARMUP_CURVATURE` | `2` | 同上，覆盖为 `traffic_gray.curvature` |
| `GRAY_ETIME` | `0` | 由 `get_gray_etime()` 从 OpenAPI 拉取 |
| `MAX_WARMUP_WAIT` | `120` | 用例内等待 `uptime` 达到目标比例时的上限；超过则跳过采样点或整个用例 |

`validate_lane_rules()` 会：
1. 校验规则字段（`enable`、`type=WARMUP`、Header key = `warmup-user`、`curvature`、`warmup_interval`）；
2. 在每轮测试前通过 `validate_rules_with_wait()` 做 `disable → enable` 以刷新 `etime`，并等待 SDK 缓存生效。

### 3.3 拓扑与 Header
```
 HTTP 请求 ──►  gateway :48095
                   │
                   ▼
              LaneEchoClient  (:19080 base / :19081 gray)
                   │
                   ▼
              LaneEchoServer  (:19090 base / :19091 gray)

直接染色 Header:      service-lane: lane-go-warmup/gray
流量匹配 Header:      warmup-user: gray
```

### 3.4 用例矩阵

| 编号 | 阶段 | 操作 | PASS 校验点 | SKIP 条件 | FAIL 条件 |
| --- | --- | --- | --- | --- | --- |
| 用例 1 预热早期 | 预热启用后极短时间内大量发送染色请求 | 用 `calc_warmup_probability(uptime, interval, curvature)` 计算理论值 | 实际 `gray%` ≤ 理论值 + tolerance | ① 无法获取有效 `GRAY_ETIME`；② `etime` 偏离 reset 时机 (`reset_drift` 过大) 表明重置未真正生效 | 实际 `gray%` 高于 `expected_prob + tolerance` |
| 用例 2 预热进展 | 多次采样，记录 `(uptime, gray%)` | 从若干目标百分位（如 25/50/75%）采样 | `gray%` 随 `uptime` 单调递增（允许统计抖动；整体趋势递增即通过） | ① 无法读取 `etime`（Polaris 版本可能不支持该字段）；② 所有目标采样点的 `target_uptime` 都 > `MAX_WARMUP_WAIT` | 非递增或严重偏离理论曲线 |
| 用例 3 预热完成 | 等待 `uptime ≥ warmup_interval` 后发送大批染色请求 | 统计 `gray%` | ≥ 90%（理想）；≥ 75% 视为基本符合预期 | 剩余等待时间 > `MAX_WARMUP_WAIT`（建议缩短 `warmup_interval`） | `gray%` < 75% |
| 用例 4 基线不受预热影响 | 不带染色 Header 发送请求 | 统计 `baseline` / `gray` 计数 | 全部走 baseline（或零 `gray` 泄漏） | — | 任一无染色请求被错误路由到 gray |
| 用例 5 混合隔离 | 染色 + 不染色请求多轮交替 | 分别统计两路结果 | 无染色请求**零泄漏**到 gray；染色请求按预热概率命中 gray（允许部分回退 baseline） | — | 任一无染色请求落到 gray（`baseline_leak > 0`） |

### 3.5 辅助能力
- `get_gray_etime()`：从 Polaris OpenAPI 读取 `gray` 规则的 `etime`，用于计算 `uptime`
- `validate_rules_with_wait(rule_name)`：`disable → enable` 规则，并轮询等待 SDK 侧生效（避免缓存滞后）
- `calc_warmup_probability(uptime, interval, curvature)`：Python 实现预热概率公式，作为实际比例的理论基线
- 当 `WARMUP_INTERVAL > MAX_WARMUP_WAIT` 时，脚本会在日志中提示「建议将 `warmup_interval` 设为 ≤`MAX_WARMUP_WAIT`s 以缩短测试耗时」

### 3.6 执行与结果
- 日志：`./.logs/lane-warmup-test-<时间戳>.log`
- 汇总：统一输出 `TOTAL_PASS / TOTAL_FAIL / TOTAL_SKIP / TOTAL_COUNT`；SKIP 不计入 FAIL，便于 CI 中区分「环境约束」与「功能缺陷」

---

## 四、常见问题排查

| 症状 | 可能原因 | 处置 |
| --- | --- | --- |
| `wait_for_services` 超时（当前上限 60s） | Polaris 尚未把 consumer / provider 注册信息推给 gateway；或端口被占用 | 查看 `./.logs/gateway.log`、`consumer.log`、`provider.log`；或先 `./cleanup.sh` |
| `ErrCodeAPIInstanceNotFound: LaneEchoClient` | Provider/Consumer 进程未正常注册 | 确认三个 `main.go` 已构建并启动，确认 `polaris.yaml` 指向正确的 Polaris 地址 |
| 规则校验失败（`gray 规则未启用` / Header Key 不符） | Polaris 控制台未按前置条件配置 | 按 2.2 / 3.2 节重新配置规则并 `enable` |
| 两个脚本同时运行冲突 | 共用 `48095 / 19080~19091` 端口 | 使用前先运行对方的 `stop`，或执行 `./cleanup.sh` |
| 用例 5.1 专项失败 | `gateway-excl/polaris.yaml` 未替换为 `baseLaneMode: 1` | 检查脚本生成的 `${gateway_excl_workdir}/polaris.yaml` |
| 用例 6.1 标记为 SKIP | Polaris 服务端缓存传播超时，管理 API 已确认移除但 SDK 尚未感知 | 属于非 SDK 问题；可加大等待或手动重跑 |
| warmup 比例明显偏高（用例 1） | 规则 `etime` 未刷新导致 `uptime` 被放大 | 确认 `validate_rules_with_wait` 成功执行；必要时手动在控制台 disable → enable |
| warmup 用例 2/3 频繁 SKIP | `warmup_interval` 过大 > `MAX_WARMUP_WAIT` | 将规则里的 `warmup_interval` 调小（建议 ≤ 120s） |

---

## 五、清理

- 两个脚本都提供 `stop` 子命令，按 `PID_FILE`（`.lane-test-pids` / `.lane-warmup-pids`）逐个杀掉进程
- 兜底清理：`./cleanup.sh`（覆盖端口、残留进程、临时目录 `.build/.logs/.pids`）
