# 元数据路由测试方案

本文档汇总 `examples/route/metadata/` 目录下端到端测试脚本的**测试方案（目标、前置条件、拓扑、用例、校验点）**，便于在 Polaris 服务端 + polaris-go SDK 环境下做元数据路由回归验证。

- 测试脚本：[verify_metadata_route.sh](./verify_metadata_route.sh)
- 被测能力：SDK 内置 `dstMetaRouter` 插件 + 业务侧手动过滤两种风格的元数据路由

---

## 一、通用信息

| 项 | 值 |
| --- | --- |
| 命名空间 | `default` |
| Provider 服务 | `RouteMetadataEchoServer` |
| Consumer 服务 | `RouteMetadataEchoCaller` |
| Provider 标签 | `env=dev` / `env=test` / `env=pre` / `env=prod` |
| 过滤用请求条件 | Consumer 启动参数 `-metadata "env=<value>"` |
| 命令入口 | `./verify_metadata_route.sh [选项]` （单命令一次跑全流程） |
| 典型启动 | `./verify_metadata_route.sh --polaris-server 127.0.0.1` |

脚本流程：`环境准备 → 编译 → 启动 4 Provider → 启动 8 Consumer（2 链路 × 4 env） → 发请求 → 负用例（env=noexist） → 汇总`。

> **关键特点**：元数据路由**不依赖** Polaris 服务端规则，脚本**不会**调用任何规则管理 API。

---

## 二、`verify_metadata_route.sh` —— 测试方案

### 2.1 测试目标

验证 polaris-go SDK 的元数据路由在不同调用风格下对 `metadata` 标签的精确匹配与负载均衡能力，覆盖：

1. **正用例**：Consumer 指定 `env=X` 时，所有请求只路由到 `metadata env=X` 的 Provider；不会错路由到其他 env 实例
2. **负用例**：Consumer 指定 `env=noexist` 时，SDK 过滤后应无可用实例；请求应失败（或触发 failover）
3. **两条链路等价性**：`simple-consumer`（`GetOneInstance + dstMetaRouter`） 与 `consumer`（`ProcessRouters + 业务过滤`）两种实现在相同输入下得到一致的过滤结果

### 2.2 前置条件

- Polaris 服务端已启动（任意版本，仅用作注册中心）
- Go 1.18+；本机 Python 3（仅脚本日志解析需要）
- **无需**在 Polaris 控制台配置任何路由规则（`dstMetaRouter` 完全在 SDK 本地匹配）
- `consumer/polaris.yaml` 配置 `serviceRouter.chain: [ruleBasedRouter]`（不启用 dstMetaRouter，演示手动过滤）
- `simple-consumer/polaris.yaml` 配置 `serviceRouter.chain: [dstMetaRouter]`

### 2.3 拓扑与端口

服务端口（来自脚本配置区）：

| 角色 | 端口 | 标签 |
| --- | --- | --- |
| `Provider-dev`  | `:28071` | `env=dev` |
| `Provider-test` | `:28072` | `env=test` |
| `Provider-pre`  | `:28073` | `env=pre` |
| `Provider-prod` | `:28074` | `env=prod` |
| `simple-consumer` base | `:18090` | 每个 env `+offset` → `18090/18091/18092/18093` |
| `simple-consumer` noexist | `:18094`（base + 4） | `env=noexist` |
| `consumer` base | `:18190` | 每个 env `+offset` → `18190/18191/18192/18193` |
| `consumer` noexist | `:18194`（base + 4） | `env=noexist` |

```
                 ┌───────────────────────────────────┐
    curl / HTTP  │ simple-consumer                   │ ─┐
   ──────────────►  :18090 (env=dev)                 │  │  链路 A
                 │  :18091 (env=test)                │  │ GetOneInstance
                 │  :18092 (env=pre)                 │  │ + dstMetaRouter
                 │  :18093 (env=prod)                │  │
                 │  :18094 (env=noexist，负用例)     │ ─┤
                 └───────────────────────────────────┘  │
                                                        ▼
                 ┌───────────────────────────────────┐  │
                 │ consumer                          │ ─┤
    curl / HTTP  │  :18190 (env=dev)                 │  │  链路 B
   ──────────────►  :18191 (env=test)                │  │ ProcessRouters
                 │  :18192 (env=pre)                 │  │ + 业务过滤
                 │  :18193 (env=prod)                │  │
                 │  :18194 (env=noexist，负用例)     │ ─┘
                 └───────────────────┬───────────────┘
                                     ▼
                 ┌───────────────────────────────────┐
                 │ Provider 集群（RouteMetadataEchoServer）│
                 │  :28071 (env=dev)                 │
                 │  :28072 (env=test)                │
                 │  :28073 (env=pre)                 │
                 │  :28074 (env=prod)                │
                 └───────────────────────────────────┘
```

### 2.4 用例矩阵

#### 2.4.1 链路 A：simple-consumer（GetOneInstance + dstMetaRouter）

| 序号 | 用例 | Consumer 启动参数 | 预期路由 | 关键校验点 |
| --- | --- | --- | --- | --- |
| `A.1` | env=dev 正用例 | `-metadata "env=dev"` `:18090` | 10 次请求全部到 `Provider-dev :28071` | CSV 中 `correct_env_count == REQUEST_COUNT` |
| `A.2` | env=test 正用例 | `-metadata "env=test"` `:18091` | 10 次请求全部到 `Provider-test :28072` | 同上 |
| `A.3` | env=pre 正用例 | `-metadata "env=pre"` `:18092` | 10 次请求全部到 `Provider-pre :28073` | 同上 |
| `A.4` | env=prod 正用例 | `-metadata "env=prod"` `:18093` | 10 次请求全部到 `Provider-prod :28074` | 同上 |
| `A.5` | env=noexist 负用例 | `-metadata "env=noexist"` `:18094` | SDK 无匹配实例；请求失败或触发 failover 返回默认实例 | HTTP 非 2xx 或 body 含错误信息 |

#### 2.4.2 链路 B：consumer（ProcessRouters + 业务侧 filterByMetadata）

| 序号 | 用例 | Consumer 启动参数 | 预期路由 | 关键校验点 |
| --- | --- | --- | --- | --- |
| `B.1` | env=dev 正用例 | `-metadata "env=dev"` `:18190` | 同 A.1 | 业务侧过滤后仅 1 个实例，ProcessLoadBalance 选中即为 `:28071` |
| `B.2` | env=test 正用例 | `-metadata "env=test"` `:18191` | 同 A.2 | 同上 |
| `B.3` | env=pre 正用例 | `-metadata "env=pre"` `:18192` | 同 A.3 | 同上 |
| `B.4` | env=prod 正用例 | `-metadata "env=prod"` `:18193` | 同 A.4 | 同上 |
| `B.5` | env=noexist 负用例 | `-metadata "env=noexist"` `:18194` | 业务侧过滤后为空数组，ProcessRouters 收到空实例 → 调用失败 | HTTP 非 2xx 或 body 含 "no instance" 字样 |

> **共 10 个用例**（2 链路 × 5 场景），每个正用例默认 `REQUEST_COUNT=10` 次请求。

### 2.5 执行与结果

- **测试日志**：`./.logs/verify_metadata_route-<时间戳>.log`
- **各服务独立日志**：`./.logs/provider_<env>.log`、`./.logs/consumer_<链路>_<env>.log`
- **结果 CSV**：`./.logs/metadata_route_result.csv`，每行记录一次请求的链路、env、HTTP 状态码、路由目标端口、是否正确
- **控制台结论**：脚本末尾输出 `✅ 完全通过` / `⚠️ 部分通过` / `❌ 未通过` 三态判定

**PASS 判定**：

| 链路 | 正用例判定 | 负用例判定 |
|---|---|---|
| A 或 B | 4 个 env 各自 `REQUEST_COUNT` 次请求 **全部** 路由到匹配 Provider | `env=noexist` 请求**无一成功路由**（全部 HTTP 非 2xx 或业务失败） |

任一正用例出现跨 env 路由，或任一负用例有请求被成功转发到任何 Provider，即视为 **FAIL**。

---

## 三、常见问题排查

| 症状 | 可能原因 | 处置 |
| --- | --- | --- |
| `wait_for_services` 超时 | Polaris 未启动，或 `POLARIS_SERVER` 地址错误 | 确认 Polaris 服务端进程在 `${POLARIS_SERVER}:8091` 可达 |
| 某个 Provider 未注册成功 | 端口被占用（28071-28074） | 使用 `./cleanup.sh` 清理残留进程后重试 |
| A 链路所有请求都路由到 `Provider-dev`（未按 env 过滤） | `simple-consumer/polaris.yaml` 的 `chain` 没有包含 `dstMetaRouter` | 检查 `chain: [dstMetaRouter]` 并重启 |
| B 链路请求全量转发（无过滤效果） | consumer 启动时 `-metadata` 参数为空 | 确认命令行有传 `-metadata "env=X"` |
| 负用例 A.5 / B.5 返回 200 | SDK 触发了 failover 返回全量实例 | 默认行为；若要验证严格过滤，需改 `ruleBasedRouter.failoverType: none`（仅对链路 B 有效） |
| 脚本卡在 `启动 Consumer` 步骤 | Consumer 端口被占用 | `lsof -i :18090-18194` 查看并清理 |
| Provider 启动后马上退出 | Polaris 鉴权开启但未传 `-token` | 使用 `--polaris-token <token>` 启动脚本 |

---

## 四、清理

- 脚本执行完毕后会自动清理所有 `provider` / `consumer` / `simple-consumer` 子进程。
- 若脚本异常中断（Ctrl+C、panic 等），使用 `./cleanup.sh` 兜底清理：

```bash
./cleanup.sh            # 展示残留进程后确认清理（默认）
./cleanup.sh -f         # 强制清理（CI 推荐）
./cleanup.sh --dry-run  # 仅展示，不执行清理
```

- `cleanup.sh` 同时清理 `.build/` 和 `.logs/` 目录（会询问确认）。
