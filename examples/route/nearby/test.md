# 就近路由测试方案

本文档汇总 `examples/route/nearby/` 目录下端到端测试脚本的**测试方案（目标、前置条件、拓扑、用例、校验点）**，便于在 Polaris 服务端 + polaris-go SDK 环境下做就近路由回归验证。

- 测试脚本：[verify_nearby_route.sh](./verify_nearby_route.sh)
- 被测能力：SDK 内置 `nearbyBasedRouter` 插件 + Polaris 服务端 NearbyPolicy 规则

---

## 一、通用信息

| 项 | 值 |
| --- | --- |
| 命名空间 | `default` |
| Provider 服务 | `RouteNearbyEchoServer` |
| Consumer 服务 | `RouteNearbyEchoClient` |
| Polaris 规则名 | `RouteNearbyEchoServer-auto-nearby`（脚本自动创建） |
| 地域层级 | Region / Zone / Campus（本示例同 Region 同 Zone，不同 Campus） |
| 命令入口 | `./verify_nearby_route.sh [选项]`（单命令跑全流程） |
| 典型启动 | `./verify_nearby_route.sh --polaris-server 127.0.0.1` |

脚本流程：`环境准备 → 编译 → 检查/创建就近路由规则 → 启动 3 Provider → 启动 2 Consumer → 发请求 → 汇总`。

---

## 二、`verify_nearby_route.sh` —— 测试方案

### 2.1 测试目标

验证 polaris-go SDK 的 `nearbyBasedRouter` 在 Polaris 服务端开启就近路由后，对同 Campus Provider 的优先路由能力，覆盖：

1. **就近命中**：Consumer 位于 `ap-guangzhou-1`，所有请求应只路由到同 Campus 的 `provider-1`
2. **两条链路等价性**：`simple-consumer`（`GetOneInstance`）与 `consumer`（`ProcessRouters + ProcessLoadBalance`）结果一致
3. **自动规则维护**：脚本能通过 Polaris 管理 API 自动创建/启用就近路由规则
4. **失败快速定位**：若规则缺失或 Consumer 地域信息异常，脚本能给出明确错误提示

### 2.2 前置条件

- Polaris 服务端已启动（版本需支持 NearbyPolicy，建议 v1.17.0+）
- Go 1.18+；本机 Python 3（脚本解析 JSON 用）
- 脚本会自动创建以下规则（也可通过 `--skip-rule-check` 跳过）：

```
规则名:           RouteNearbyEchoServer-auto-nearby
作用服务:         RouteNearbyEchoServer (default 命名空间)
routing_policy:  NearbyPolicy
match_level:     CAMPUS  （可通过 --rule-match-level 覆盖）
max_match_level: UNKNOWN （默认不限）
strict_nearby:   false
enable:          true
```

- `consumer/polaris.yaml` 与 `simple-consumer/polaris.yaml` 都启用 `chain: [ruleBasedRouter, nearbyBasedRouter]`，SDK 侧 `matchLevel: campus`

### 2.3 拓扑与端口

服务端口与地域分布（来自脚本配置区）：

| 角色 | 端口 | Region | Zone | Campus |
| --- | --- | --- | --- | --- |
| `Provider-1` | `:28091` | `china` | `ap-guangzhou` | `ap-guangzhou-1` |
| `Provider-2` | `:28092` | `china` | `ap-guangzhou` | `ap-guangzhou-2` |
| `Provider-3` | `:28093` | `china` | `ap-guangzhou` | `ap-guangzhou-3` |
| `simple-consumer` | `:18080` | `china` | `ap-guangzhou` | `ap-guangzhou-1` |
| `consumer` | `:18081` | `china` | `ap-guangzhou` | `ap-guangzhou-1` |

```
       ┌──────────────────────────────────┐
       │ simple-consumer  :18080          │ ── 链路 A (GetOneInstance)
  curl │   campus=ap-guangzhou-1          │
  ────►│                                  │
       │ consumer         :18081          │ ── 链路 B (ProcessRouters)
       │   campus=ap-guangzhou-1          │
       └────────────────┬─────────────────┘
                        │
                        ▼
       ┌──────────────────────────────────┐
       │  Provider 集群                    │
       │  :28091  campus=ap-guangzhou-1   │ ◄── 同 Campus，就近命中
       │  :28092  campus=ap-guangzhou-2   │     不应被命中
       │  :28093  campus=ap-guangzhou-3   │     不应被命中
       └──────────────────────────────────┘
```

### 2.4 用例矩阵

脚本对两条链路各发送 `REQUEST_COUNT`（默认 20）次请求并统计路由分布：

| 序号 | 用例 | 链路 | 请求条件 | 预期路由 | PASS 判定 |
| --- | --- | --- | --- | --- | --- |
| `A.1` | simple-consumer 就近命中 | `simple-consumer:18080` | GET `/echo` 20 次 | 全部到 `provider-1 (:28091)` | `campus_1_count == REQUEST_COUNT`，`campus_2_count == 0`，`campus_3_count == 0` |
| `B.1` | consumer 就近命中 | `consumer:18081` | GET `/echo` 20 次 | 同上 | 同上 |

> **共 2 个核心用例**（链路 A 和 B）。每条链路独立统计路由命中率。

**结果判定（每条链路）**：

| 情况 | 判定 | 说明 |
|---|---|---|
| 全部请求路由到 provider-1 | ✅ **PASS** | 就近路由正常工作 |
| 部分请求到 provider-1，其余到 2/3 | ⚠️ **PARTIAL** | 部分就近，可能 Consumer 地域信息配置不完整 |
| 没有任何请求到 provider-1 | ❌ **FAIL** | 就近路由未生效或规则未创建 |
| 请求失败（HTTP 5xx / 超时） | ❌ **FAIL** | 基础链路问题（Provider 未注册、端口占用等） |

**综合结论（两条链路合并）**：

| 链路 A | 链路 B | 综合结论 |
|---|---|---|
| PASS | PASS | ✅ 完全通过 |
| PASS | PARTIAL / FAIL | ⚠️ 仅 simple 链路通过 |
| FAIL | PASS | ⚠️ 仅 consumer 链路通过 |
| FAIL | FAIL | ❌ 未通过（优先检查规则配置） |

### 2.5 执行与结果

- **测试日志**：`.logs/verify_nearby_route-<时间戳>.log`
- **各服务独立日志**：`.logs/provider_1.log`、`provider_2.log`、`provider_3.log`、`simple_consumer.log`、`consumer.log`
- **控制台结论**：脚本末尾输出 ✅/⚠️/❌ 三态结论，列出两条链路的各自状态

---

## 三、命令参数

| 参数 | 默认 | 说明 |
|---|---|---|
| `--polaris-server <地址>` | `127.0.0.1` | Polaris 服务端地址 |
| `--polaris-token <令牌>` | `""` | Polaris 鉴权开启时的 Token |
| `--namespace <命名空间>` | `default` | 服务命名空间 |
| `--service <服务名>` | `RouteNearbyEchoServer` | 被测 Provider 服务名 |
| `--request-count <次数>` | `20` | 每条链路发送的请求次数 |
| `--rule-match-level <级别>` | `CAMPUS` | 服务端规则 match_level（REGION / ZONE / CAMPUS） |
| `--rule-max-match-level <级别>` | `UNKNOWN` | 服务端规则 max_match_level |
| `--rule-strict-nearby <bool>` | `false` | 服务端规则 strict_nearby |
| `--no-auto-create` | — | 规则不存在时直接报错退出，不自动创建 |
| `--skip-rule-check` | — | 完全跳过规则校验/创建（假设规则已就绪） |
| `--debug` | — | 开启 SDK DEBUG 日志 |

---

## 四、常见问题排查

| 症状 | 可能原因 | 处置 |
| --- | --- | --- |
| 所有请求均匀分布到 3 个 Provider | Polaris 就近路由规则未启用 | 在控制台手动启用，或去掉 `--skip-rule-check` 让脚本自动创建 |
| 规则已创建但请求仍不就近 | SDK 侧 `matchLevel` 与规则 match_level 不一致 | 确保 `polaris.yaml` 的 `matchLevel` 与 `--rule-match-level` 一致 |
| Consumer 日志显示 `location is empty` | 环境变量 `REGION/ZONE/CAMPUS` 未设置 | `env \| grep -E 'REGION\|ZONE\|CAMPUS'` 检查；必须三项齐全 |
| 规则创建返回 `code=5xxxx` | Polaris 版本不支持 NearbyPolicy | 升级到 v1.17.0+ |
| 规则创建返回 `unauthorized` | Polaris 开启了鉴权但未传 Token | 加 `--polaris-token <token>` |
| 链路 B 失败而链路 A 通过 | consumer 的 ProcessRouters 路由链未含 nearbyBasedRouter | 检查 `consumer/polaris.yaml` 的 `chain` |
| Provider 启动后立即退出 | 端口被占用（28091-28093） | 运行 `./cleanup.sh` 清理后重试 |
| strict_nearby=true 时跨 campus 访问失败 | 严格模式不允许退化 | 改为 `false`，或确保同 campus 有可用实例 |

---

## 五、清理

- 脚本执行完毕会自动清理 provider / consumer 子进程
- 若脚本异常中断，使用 `cleanup.sh` 兜底清理：

```bash
./cleanup.sh            # 展示后确认（默认）
./cleanup.sh -f         # 强制清理（CI 推荐）
./cleanup.sh --dry-run  # 仅展示，不执行清理
```

- `cleanup.sh` 同时清理 `.build/` 和 `.logs/` 目录（会询问确认）
- 规则 `RouteNearbyEchoServer-auto-nearby` 会保留在 Polaris 服务端；如需删除请手动在控制台或通过 API 操作
