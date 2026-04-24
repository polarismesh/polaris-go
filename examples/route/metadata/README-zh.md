# 元数据路由（Metadata Router）示例

## 功能说明

元数据路由（Metadata Router）通过 SDK 内置的 **`dstMetaRouter`** 插件，在 Consumer 发起调用时按请求中携带的 `Metadata` 字段精确匹配目标实例的 `metadata` 标签，常用于按环境（`env=dev/test/pre/prod`）、地域、业务线等维度隔离流量。

本示例演示以下核心能力：

- **Provider 侧**：注册带有不同 `env` 标签的多个实例（如 `env=dev`、`env=test`）
- **Consumer 侧**：通过两种调用方式把 `metadata` 传给 SDK 完成过滤
  - **simple-consumer**：`GetOneInstance` 一步到位（polaris.yaml 启用 `dstMetaRouter`）
  - **consumer**：`GetAllInstances` → 业务侧手工 `filterByMetadata` → `ProcessRouters` → `ProcessLoadBalance`（手动三段式，用于演示流程）

> **重要**：元数据路由**不依赖** Polaris 服务端的路由规则，匹配完全在 SDK 本地完成。无需在控制台创建任何规则。

---

## 前置条件

1. 已部署并运行 [Polaris 服务端](https://github.com/polarismesh/polaris)（任意版本均可，仅用作注册中心）。
2. 各示例目录下的 `polaris.yaml` 中 `global.serverConnector.addresses` 通过环境变量 `${POLARIS_SERVER}` 注入 Polaris 地址；也可编辑为固定地址。
3. Go 1.18+。

> **提示**：如仅运行测试脚本 `verify_metadata_route.sh`，脚本会自动编译并以 `POLARIS_SERVER=<地址>` 启动所有进程。

---

## 目录结构

```
metadata/
├── provider/                    # 服务提供端（注册带 env 元数据标签的实例）
│   ├── main.go
│   └── polaris.yaml
├── consumer/                    # 手动三段式 Consumer（GetAllInstances + 业务过滤 + ProcessRouters）
│   ├── main.go
│   └── polaris.yaml             # chain: [ruleBasedRouter]（不启用 dstMetaRouter）
├── simple-consumer/             # 简化 Consumer（GetOneInstance + dstMetaRouter）
│   ├── main.go
│   └── polaris.yaml             # chain: [dstMetaRouter]
├── verify_metadata_route.sh     # 元数据路由端到端测试脚本（覆盖两条链路 × 5 用例）
├── cleanup.sh                   # 残留进程清理脚本
├── README-zh.md
└── test.md                      # 测试方案文档
```

> **两条链路**：`simple-consumer` 与 `consumer` 互相独立，分别演示 SDK 自动过滤和业务侧手动过滤两种风格，均被 `verify_metadata_route.sh` 覆盖。

---

## 快速开始

### 1. 启动多个 Provider（不同 env 标签）

```bash
cd provider
go build -o bin

# 终端 1：env=dev
POLARIS_SERVER=127.0.0.1 ./bin -metadata "env=dev"  -port=28071

# 终端 2：env=test
POLARIS_SERVER=127.0.0.1 ./bin -metadata "env=test" -port=28072

# 终端 3：env=pre
POLARIS_SERVER=127.0.0.1 ./bin -metadata "env=pre"  -port=28073

# 终端 4：env=prod
POLARIS_SERVER=127.0.0.1 ./bin -metadata "env=prod" -port=28074
```

### 2. 启动 Consumer（两条链路任选其一）

#### 链路 A：simple-consumer（GetOneInstance + dstMetaRouter）

```bash
cd simple-consumer
go build -o bin
POLARIS_SERVER=127.0.0.1 ./bin -metadata "env=dev" -port=18090
```

#### 链路 B：consumer（ProcessRouters + 业务过滤）

```bash
cd consumer
go build -o bin
POLARIS_SERVER=127.0.0.1 ./bin -metadata "env=dev" -port=18190
```

### 3. 发送测试请求

```bash
# 链路 A：所有请求只会路由到 provider-dev (:28071)
for i in $(seq 1 5); do curl -s http://127.0.0.1:18090/echo; echo; done

# 链路 B：同上，只会到 provider-dev (:28071)
for i in $(seq 1 5); do curl -s http://127.0.0.1:18190/echo; echo; done
```

预期响应示例：

```
Hello, I'm RouteEchoServer Provider, My metadata's : "env=dev", host : 10.x.x.x:28071
```

### 4. 一键跑完整验证

```bash
./verify_metadata_route.sh --polaris-server 127.0.0.1
```

脚本会自动编译、启动 4 个 Provider + 8 个 Consumer（两条链路 × 4 个 env）、跑 5 类正负用例、输出 CSV 结果到 `./.logs/metadata_route_result.csv`。

---

## 链路组合与两种调用方式

| 链路 | 调用风格 | Consumer 服务名 | Consumer 端口 | polaris.yaml `serviceRouter.chain` | 元数据过滤发生位置 |
|---|---|---|---|---|---|
| A  `simple-consumer` | `GetOneInstance` 一步到位 | `RouteMetadataEchoCaller` | `18090`（每个 env +1） | `[dstMetaRouter]` | SDK 内部 `dstMetaRouter` 插件 |
| B  `consumer` | `GetAllInstances + ProcessRouters` 手动三段式 | `RouteMetadataEchoCaller` | `18190`（每个 env +1） | `[ruleBasedRouter]` | 业务代码 `filterByMetadata()` |

> **什么时候选 simple-consumer？** 绝大多数生产场景——业务代码更简洁，SDK 自动完成过滤和负载均衡。
>
> **什么时候选 consumer？** 需要在过滤前后插入自定义逻辑（打日志、分流、结合其他上下文判断等），或者希望自己掌控实例候选集合。本示例保留它主要用于教学演示。

---

## 路由逻辑说明

### simple-consumer 路由流程

```
Consumer 请求到达 /echo
  │
  ├── 构造 GetOneInstanceRequest
  │     ├── Namespace / Service = 目标服务
  │     └── Metadata = 本实例自身启动参数里解析的 map（如 {env: dev}）
  │
  ├── ConsumerAPI.GetOneInstance(req)
  │     └── 路由链（polaris.yaml 配置）：
  │           └── dstMetaRouter
  │                 ├── 对比 req.Metadata 与每个实例的 metadata
  │                 ├── 全部匹配才保留
  │                 └── 无匹配 → 触发 failover（默认：返回所有健康实例）
  │
  └── 选中的单个实例 → HTTP 转发 /echo
```

### consumer 路由流程（手动三段式）

```
Consumer 请求到达 /echo
  │
  ├── ConsumerAPI.GetAllInstances(svc)   // 拉取全量实例（不走路由）
  │
  ├── filterByMetadata(instances, meta)  // 业务代码手工比对 metadata
  │
  ├── RouterAPI.ProcessRouters(已过滤实例)
  │     └── 路由链：
  │           └── ruleBasedRouter（若未配置规则则直通）
  │
  ├── RouterAPI.ProcessLoadBalance(路由结果)  // 选 1 个实例
  │
  └── HTTP 转发 /echo
```

### dstMetaRouter 的匹配语义

- Consumer 指定的 `Metadata = {k1:v1, k2:v2}`，目标实例的 metadata **必须同时包含** `k1=v1` 与 `k2=v2` 才算匹配（**AND** 语义）。
- 若无任何实例匹配，SDK 根据 `failoverType` 决定兜底行为，默认返回全部健康实例。本示例保持 SDK 默认。
- 与 `ruleBasedRouter` 不同，`dstMetaRouter` **不使用** Polaris 服务端规则，匹配完全在本地。

---

## polaris.yaml 配置说明

### simple-consumer/polaris.yaml（启用 dstMetaRouter）

```yaml
consumer:
  serviceRouter:
    chain:
      # 启用元数据路由；一般不建议同时启用 dstMetaRouter 和 ruleBasedRouter
      - dstMetaRouter
```

### consumer/polaris.yaml（不启用 dstMetaRouter）

```yaml
consumer:
  serviceRouter:
    # 本示例在业务侧手工做 metadata 过滤，所以不启用 dstMetaRouter，
    # 只保留规则路由用于演示"剩余路由链"的效果。
    chain:
      - ruleBasedRouter
    plugin:
      ruleBasedRouter:
        failoverType: all   # 规则全部未命中时的兜底：all=全量 / none=空
```

---

## 参数说明

### Provider 参数

| 参数          | 默认值                    | 说明                                    |
|--------------|---------------------------|----------------------------------------|
| `-namespace` | `default`                 | 服务命名空间                             |
| `-service`   | `RouteMetadataEchoServer` | 服务名                                  |
| `-port`      | `0`（随机）                | 监听端口                                |
| `-metadata`  | `""`                      | 实例标签，格式 `key1=value1&key2=value2` |
| `-token`     | `""`                      | 鉴权 Token（Polaris 开启鉴权时使用）      |

### Consumer 参数（手动三段式）

| 参数               | 默认值                        | 说明                                 |
|-------------------|------------------------------|-------------------------------------|
| `-calleeNamespace`| `default`                    | 被调服务命名空间                      |
| `-calleeService`  | `RouteMetadataEchoServer`    | 被调服务名                           |
| `-selfNamespace`  | `default`                    | 自身命名空间                         |
| `-selfService`    | `RouteMetadataEchoCaller`    | 自身服务名                           |
| `-metadata`       | `""`                         | 过滤条件，格式 `key1=value1&key2=value2` |
| `-port`           | `18090`                      | Consumer HTTP 监听端口               |
| `-token`          | `""`                         | 鉴权 Token                          |
| `-debug`          | `false`                      | 开启 Polaris SDK DEBUG 日志          |

### Simple Consumer 参数

| 参数               | 默认值                        | 说明                        |
|-------------------|------------------------------|-----------------------------|
| `-calleeNamespace`| `default`                    | 被调服务命名空间              |
| `-calleeService`  | `RouteMetadataEchoServer`    | 被调服务名                   |
| `-namespace`      | `default`                    | 自身命名空间                 |
| `-service`        | `RouteMetadataEchoCaller`    | 自身服务名                   |
| `-metadata`       | `""`                         | 过滤条件，格式 `key=value&...` |
| `-port`           | `18090`                      | Consumer HTTP 监听端口        |

---

## 测试脚本

目录下提供了以下辅助脚本，用于自动化构建、启动、测试和清理：

- [`verify_metadata_route.sh`](./verify_metadata_route.sh)：元数据路由端到端测试（两条链路 × 4 个 env 正用例 + 1 个 noexist 负用例）
- [`cleanup.sh`](./cleanup.sh)：残留进程清理

> **完整测试方案（测试目标、前置条件、拓扑端口、用例矩阵、PASS/FAIL 判定、排障指引）请见 👉 [test.md](./test.md)**

### verify_metadata_route.sh 常用命令

```bash
./verify_metadata_route.sh                             # Polaris 默认 127.0.0.1
./verify_metadata_route.sh --polaris-server 10.x.x.x   # 指定 Polaris 地址
./verify_metadata_route.sh --request-count 20          # 每个链路每个 env 发 20 个请求
./verify_metadata_route.sh --polaris-token <token>     # 开启鉴权时传 Token
```

脚本结束后会在控制台输出 `✅ / ⚠️ / ❌` 三态结论，详细数据写入：

| 文件 | 内容 |
|---|---|
| `.logs/metadata_route_result.csv` | 每条请求的链路 / env / HTTP 状态 / 路由目标 / 是否正确的详细列表 |
| `.logs/provider_<env>.log` | 各 Provider 启动与请求日志 |
| `.logs/consumer_<...>.log` | 各 Consumer 请求日志 |
| `.logs/verify_metadata_route-<时间戳>.log` | 测试总日志 |

### cleanup.sh — 进程清理

清理残留的 provider / consumer 进程和构建产物。

```bash
./cleanup.sh            # 展示后确认再清理（默认）
./cleanup.sh -f         # 强制清理，不需要确认（CI 推荐）
./cleanup.sh --dry-run  # 仅展示匹配的进程，不执行清理
```

---

## 日志与排错

### 路由命中失败时的常见原因

| 症状 | 可能原因 | 处置 |
|---|---|---|
| Consumer 请求返回 "no instance available" | Provider 未注册成功，或 metadata 过滤后为空 | 确认 Provider 启动日志里的 `register response`；确认 Consumer 的 `-metadata` 与 Provider 的 `-metadata` 值完全一致（包括大小写） |
| 请求路由到错误 env 的 Provider | simple-consumer 的 polaris.yaml `chain` 没有包含 `dstMetaRouter`，SDK 走了默认行为 | 检查 `simple-consumer/polaris.yaml`，确认 `chain: [dstMetaRouter]` |
| consumer 链路全量请求但无过滤 | 业务代码 `filterByMetadata` 没生效（可能 metadata 参数为空） | 确认启动命令里 `-metadata` 有传值 |

### Polaris SDK 日志

SDK 日志按类别输出到 `$HOME/polaris/log/`：

| 目录 | 内容 |
|---|---|
| `base/` | 初始化、配置加载 |
| `route/` | **路由插件日志**（dstMetaRouter / ruleBasedRouter） |
| `network/` | 与 Polaris 服务端通信 |
| `stat/` | 调用统计 |

开启 SDK DEBUG 日志（只对 consumer 有效）：

```bash
./bin -debug=true -metadata "env=dev" ...
```

或在代码中：

```go
api.SetLoggersLevel(api.DebugLog)
```

关键字检索：

```bash
grep -E 'dstMetaRouter|metadata' ~/polaris/log/route/polaris-route.log
```
