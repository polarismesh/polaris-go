# 规则路由测试方案

本文档汇总 `examples/route/rule/` 目录下端到端测试脚本的**测试方案（目标、前置条件、拓扑、用例、校验点）**，便于在 Polaris 服务端 + polaris-go SDK 环境下做规则路由回归验证。

- 测试脚本：[verify_rule_route.sh](./verify_rule_route.sh)
- 被测能力：SDK 内置 `ruleBasedRouter` 插件 + Polaris 服务端 RulePolicy 规则 + `failoverType` 兜底

---

## 一、通用信息

| 项 | 值 |
| --- | --- |
| 命名空间 | `default` |
| Provider 服务 | `RouteEchoServer` |
| Consumer 服务 | `RouteEchoClient` |
| Polaris 规则名 | `RouteEchoServer-auto-rule`（脚本自动创建 4 条 env 规则） |
| 路由匹配维度 | `QUERY env` + `CUSTOM env`（兼容旧规则） |
| 命令入口 | `./verify_rule_route.sh [选项]`（单命令跑全流程） |
| 典型启动 | `./verify_rule_route.sh --polaris-server 127.0.0.1` |

脚本流程：`环境准备 → 编译 → 检查/创建 4 条 env 路由规则 → 启动 4 Provider → 启动 4 Consumer（2 链路 × 2 failoverType） → 跑 3 场景 → 汇总 4×3 矩阵`。

---

## 二、`verify_rule_route.sh` —— 测试方案

### 2.1 测试目标

验证 polaris-go SDK 的 `ruleBasedRouter` 在不同调用方式与 `failoverType` 下的路由决策，覆盖：

1. **matched env 正用例**：请求带 `?env=<dev|test|pre|prod>` 时，必须命中对应 env 的 Provider
2. **failoverType=all 兜底**：请求带未注册的 `?env=nomatch` 或空值时，返回全量实例
3. **failoverType=none 严格**：同上请求但链路配置 `none`，必须返回空实例（调用失败）
4. **两条链路对照**：
   - `simple-consumer`：`GetOneInstance`，仅上报 QUERY + Header
   - `consumer`：`ProcessRouters + ProcessLoadBalance`，同时上报 QUERY + CUSTOM + Header
5. **自动规则维护**：脚本自动创建/启用 4 条 env 规则

### 2.2 前置条件

- Polaris 服务端已启动（建议 v1.14.0+，支持路由 v2 API）
- Go 1.18+；本机 Python 3（脚本解析 JSON）
- 脚本会自动创建如下规则（也可通过 `--skip-rule-check` 跳过）：

```
规则名:           RouteEchoServer-auto-rule（合并为一组 4 条 env 规则）
作用服务:         RouteEchoServer (default 命名空间)
routing_policy:  RulePolicy
入站规则:
  对每个 env ∈ {dev, test, pre, prod}：
    source:      QUERY env=<env> (EXACT)
    destination: metadata env=<env>，priority=0, weight=100
enable:          true
```

- 脚本会为每条 Consumer 链路动态生成独立的 `polaris.yaml`（含 `failoverType` 差异）

### 2.3 拓扑与端口

服务端口（来自脚本配置区）：

| 角色 | 端口 | 标签/配置 |
| --- | --- | --- |
| `Provider-dev`  | `:28081` | `metadata env=dev` |
| `Provider-test` | `:28082` | `metadata env=test` |
| `Provider-pre`  | `:28083` | `metadata env=pre` |
| `Provider-prod` | `:28084` | `metadata env=prod` |
| `consumer`        × `failover=all`  | `:18080` | ProcessRouters + `failoverType: all` |
| `simple-consumer` × `failover=all`  | `:18081` | GetOneInstance + `failoverType: all` |
| `consumer`        × `failover=none` | `:18082` | ProcessRouters + `failoverType: none` |
| `simple-consumer` × `failover=none` | `:18083` | GetOneInstance + `failoverType: none` |

```
                 ┌──────────────────────────────────────┐
     curl ?env=X │ 4 条 Consumer 链路                   │
   ─────────────►│  :18080 consumer        failover=all │
                 │  :18081 simple-consumer failover=all │
                 │  :18082 consumer        failover=none│
                 │  :18083 simple-consumer failover=none│
                 └─────────────────┬────────────────────┘
                                   │
                                   ▼
                 ┌──────────────────────────────────────┐
                 │  Provider 集群（RouteEchoServer）    │
                 │    :28081  env=dev                   │
                 │    :28082  env=test                  │
                 │    :28083  env=pre                   │
                 │    :28084  env=prod                  │
                 └──────────────────────────────────────┘
```

### 2.4 用例矩阵

脚本跑 **3 个场景 × 4 条链路 = 12 个用例组**，每组默认 `REQUEST_COUNT`（正用例 = 10）或 `FAILOVER_REQUEST_COUNT`（失败场景 = 5）次请求。

#### 2.4.1 场景 1：matched env（正用例）

对每条链路的 4 个 env 值各发一组请求：

| 请求参数 | 链路 | 预期路由 | PASS 判定 |
| --- | --- | --- | --- |
| `?env=dev`  | 全部 4 链路 | 仅 `Provider-dev :28081` | 10 次请求全部命中 `:28081` |
| `?env=test` | 全部 4 链路 | 仅 `Provider-test :28082` | 同上 |
| `?env=pre`  | 全部 4 链路 | 仅 `Provider-pre :28083` | 同上 |
| `?env=prod` | 全部 4 链路 | 仅 `Provider-prod :28084` | 同上 |

> **16 个用例**（4 链路 × 4 env），要求全部命中才算 PASS。

#### 2.4.2 场景 2a：failover / nomatch（`?env=nomatch`）

| 链路 | failoverType | 预期行为 | PASS 判定 |
| --- | --- | --- | --- |
| `:18080` consumer all       | `all`  | 返回全量实例，请求 HTTP 200 | 所有请求成功（路由到任意 env Provider） |
| `:18081` simple-consumer all| `all`  | 同上 | 同上 |
| `:18082` consumer none      | `none` | 返回空实例，调用失败 | 所有请求失败（`no instance`） |
| `:18083` simple-consumer none| `none`| 同上 | 同上 |

#### 2.4.3 场景 2b：failover / 空值（`?env=`）

与 2a 相同的链路与判定逻辑，但请求 env 参数为空字符串。

> 场景 2a + 2b 共 **8 个用例**（2 场景 × 4 链路）。

### 2.5 结果矩阵

脚本末尾输出 4 × 3 结果矩阵：

```
                         env=match  env=nomatch  env=(空)
consumer        all      ✅         ✅           ✅
simple-consumer all      ✅         ✅           ✅
consumer        none     ✅         ✅失败预期   ✅失败预期
simple-consumer none     ✅         ✅失败预期   ✅失败预期
```

**综合结论判定**：

| 情况 | 结论 |
|---|---|
| 12 格全部 ✅ | ✅ **完全通过** |
| 部分 ✅、部分 PARTIAL | ⚠️ **部分通过**，查看详细日志 |
| 任一格 ❌ | ❌ **未通过** |

### 2.6 执行与结果

- **测试日志**：`.logs/verify_rule_route-<时间戳>.log`
- **各服务独立日志**：`.logs/provider_<env>.log`、`.logs/consumer_*.log`、`.logs/simple_*.log`
- **控制台结论**：12 格状态矩阵 + 综合 ✅/⚠️/❌ + 失败建议

---

## 三、命令参数

| 参数 | 默认 | 说明 |
|---|---|---|
| `--polaris-server <地址>` | `127.0.0.1` | Polaris 服务端地址 |
| `--polaris-token <令牌>` | `""` | 鉴权开启时的 Token |
| `--namespace <命名空间>` | `default` | 服务命名空间 |
| `--service <服务名>` | `RouteEchoServer` | 目标 Provider 服务名 |
| `--self-service <服务名>` | 自动 | 主调方服务名（覆盖 `CONSUMER_SERVICE`） |
| `--request-count <次数>` | `10` | 场景 1 每条（链路×env）请求次数 |
| `--failover-request-count <次数>` | `5` | 场景 2a/2b 每条链路的请求次数 |
| `--skip-rule-check` | — | 跳过规则校验/创建（假设规则已就绪） |
| `--debug` | — | 开启 SDK DEBUG 日志 |

---

## 四、常见问题排查

| 症状 | 可能原因 | 处置 |
| --- | --- | --- |
| 场景 1 全部失败（所有请求都走兜底） | 规则 source 类型与 Consumer 上报的 Argument 类型不一致 | `simple-consumer` 只上报 QUERY，规则必须是 `type: QUERY`；若规则用 CUSTOM，只有 `consumer` 链路能命中 |
| 场景 2b（空值 env）在 all 链路也失败 | 空值 QUERY 参数被 SDK 过滤掉了 | 正常行为：Argument 为空串时可能被跳过，直接走 failover；verify 脚本已把"all + 空值"列为成功预期 |
| 规则创建返回 `code=4xxxx` | Polaris 版本过低或 v2 路由 API 未启用 | 升级到 v1.14.0+ |
| 规则存在但请求都走 failover | 规则未 `enable=true` | 脚本会自动启用；或手动 PUT `/naming/v2/routings/enable` |
| `consumer` 链路命中，`simple-consumer` 链路不命中 | 规则只有 CUSTOM，simple-consumer 不上报 CUSTOM | 把规则改为 `type: QUERY`，或使用 `consumer` 链路 |
| failoverType=none 链路请求成功返回 200 | polaris.yaml 未正确应用（还在用 all） | 检查脚本生成的临时 polaris.yaml 是否 `failoverType: none` |
| Provider 启动失败 | 端口 28081-28084 被占用 | 运行 `./cleanup.sh` 清理 |

---

## 五、清理

- 脚本执行完毕会自动清理 provider / consumer 子进程与临时 polaris.yaml
- 若脚本异常中断，使用 `cleanup.sh` 兜底清理：

```bash
./cleanup.sh            # 展示后确认（默认）
./cleanup.sh -f         # 强制清理（CI 推荐）
./cleanup.sh --dry-run  # 仅展示，不执行清理
```

- `cleanup.sh` 同时清理 `.build/` 和 `.logs/` 目录（会询问确认）
- 自动创建的路由规则 `RouteEchoServer-auto-rule` 会保留在 Polaris 服务端；如需删除请在控制台或通过 `POST /naming/v2/routings/delete` 操作
