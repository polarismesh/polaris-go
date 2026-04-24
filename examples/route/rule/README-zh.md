# 规则路由（Rule Router）示例

## 功能说明

规则路由（Rule Router）通过 SDK 内置的 **`ruleBasedRouter`** 插件，按 Polaris 服务端配置的**路由规则**匹配请求标签并选中目标实例集合，实现按请求参数（Query / Header / 自定义参数）、主调方服务、目标实例标签（metadata）进行精确流量分发。典型场景：按 `env` / `region` / `user-group` 等维度做环境隔离、A/B 分组。

本示例演示以下核心能力：

- **Provider 侧**：注册带 `env` 标签的多个实例（`env=dev/test/pre/prod`）
- **Consumer 侧**：通过两种调用方式触发规则路由
  - **simple-consumer**：`GetOneInstance` 一步到位（只上报 QUERY 参数）
  - **consumer**：`GetAllInstances + ProcessRouters + ProcessLoadBalance` 手动三段式（同时上报 QUERY + CUSTOM 参数，兼容旧规则）
- **`failoverType` 兜底策略**：规则全未命中时的行为对比
  - `all`：返回全量实例（宽松兜底）
  - `none`：返回空列表（严格匹配，调用失败）

> **依赖**：必须在 Polaris 服务端配置路由规则；`verify_rule_route.sh` 会通过管理 API 自动创建 4 条 env 规则。

---

## 前置条件

1. 已部署并运行 [Polaris 服务端](https://github.com/polarismesh/polaris)（建议 v1.14.0+，支持 v2 路由规则）。
2. 在 Polaris 控制台**或通过脚本自动**配置 `RouteEchoServer` 的路由规则（按 `env` 分组）。
3. `polaris.yaml` 中 `global.serverConnector.addresses` 通过 `${POLARIS_SERVER}` 环境变量注入。
4. Go 1.18+。

> **提示**：直接运行 `verify_rule_route.sh` 会自动处理规则创建、进程启动、结果校验。

---

## 目录结构

```
rule/
├── provider/                 # Provider 示例（注册带 env 标签的实例）
│   ├── main.go
│   └── polaris.yaml
├── consumer/                 # 手动三段式 Consumer（同时上报 QUERY + CUSTOM 参数）
│   ├── main.go
│   └── polaris.yaml          # failoverType: all（可被测试脚本动态替换）
├── simple-consumer/          # 简化 Consumer（GetOneInstance，仅上报 QUERY）
│   ├── main.go
│   └── polaris.yaml
├── image/                    # 文档截图
├── verify_rule_route.sh      # 规则路由端到端测试脚本（4 链路 × 3 场景 = 12 用例组）
├── cleanup.sh                # 残留进程清理脚本
├── README-zh.md
├── README.md                 # 英文版本
└── test.md                   # 测试方案文档
```

---

## 快速开始

### 1. 启动 4 个 Provider（不同 env 标签）

```bash
cd provider
go build -o bin

# 终端 1：env=dev
POLARIS_SERVER=127.0.0.1 ./bin -metadata "env=dev"  -port=28081

# 终端 2：env=test
POLARIS_SERVER=127.0.0.1 ./bin -metadata "env=test" -port=28082

# 终端 3：env=pre
POLARIS_SERVER=127.0.0.1 ./bin -metadata "env=pre"  -port=28083

# 终端 4：env=prod
POLARIS_SERVER=127.0.0.1 ./bin -metadata "env=prod" -port=28084
```

### 2. 在 Polaris 控制台配置路由规则

在「路由规则」页面，为服务 `RouteEchoServer`（default 命名空间）创建 4 条规则，每条规则匹配一个 env：

| 规则 | 源（Source） | 目标（Destination） |
|---|---|---|
| env-dev  | QUERY `env=dev`  | 标签 `env=dev`  的实例 |
| env-test | QUERY `env=test` | 标签 `env=test` 的实例 |
| env-pre  | QUERY `env=pre`  | 标签 `env=pre`  的实例 |
| env-prod | QUERY `env=prod` | 标签 `env=prod` 的实例 |

> 或跳过此步，直接执行 `./verify_rule_route.sh --polaris-server 127.0.0.1` 让脚本自动创建。

### 3. 启动 Consumer 并发送带 env 的请求

#### 链路 A：simple-consumer（GetOneInstance）

```bash
cd simple-consumer
go build -o bin
POLARIS_SERVER=127.0.0.1 ./bin -port=18081
```

#### 链路 B：consumer（ProcessRouters）

```bash
cd consumer
go build -o bin
POLARIS_SERVER=127.0.0.1 ./bin -port=18080
```

#### 发请求

```bash
# 带 env=dev → 只路由到 provider-dev (:28081)
curl "http://127.0.0.1:18081/echo?env=dev"

# 带 env=test → 只路由到 provider-test (:28082)
curl "http://127.0.0.1:18081/echo?env=test"

# 不带 env 或 env=nomatch → 按 failoverType 兜底
curl "http://127.0.0.1:18081/echo?env=nomatch"
```

响应示例：

```
Hello, I'm RouteEchoServer Provider, My metadata's : "env=dev", host : 10.x.x.x:28081
```

### 4. 一键跑完整验证

```bash
./verify_rule_route.sh --polaris-server 127.0.0.1
```

脚本会自动：编译 → 自动创建规则 → 启动 4 Provider + 4 Consumer（两条链路 × 两种 failoverType） → 跑 3 类场景（正向 / nomatch / 空值） → 输出结果矩阵。

---

## 链路组合与两种调用方式

| 链路 | 调用风格 | 上报的 Argument 类型 | 默认 failoverType 端口 |
|---|---|---|---|
| A1 `simple-consumer` × `failoverType=all`  | `GetOneInstance` | QUERY（自动）+ Header | `:18081` |
| A2 `simple-consumer` × `failoverType=none` | `GetOneInstance` | 同上 | `:18083` |
| B1 `consumer` × `failoverType=all`         | `GetAllInstances + ProcessRouters + ProcessLoadBalance` | QUERY + **CUSTOM**（兼容旧规则） + Header | `:18080` |
| B2 `consumer` × `failoverType=none`        | 同上 | 同上 | `:18082` |

> `verify_rule_route.sh` 会动态生成 4 套 `polaris.yaml`（failoverType × 端口）并用不同命令行启动对应的 Consumer。

---

## 路由逻辑说明

### 规则路由执行流程（Consumer 侧）

```
HTTP 请求到达 /echo?env=dev
  │
  ├── Consumer 从 HTTP 请求中提取参数
  │     ├── Query: env=dev         → Argument{type=QUERY, key=env, value=dev}
  │     ├── Header (若有)          → Argument{type=HEADER, key=..., value=...}
  │     └── (consumer 额外上报)    → Argument{type=CUSTOM, key=env, value=dev}
  │
  ├── SDK convert() 把 Arguments 写入 SourceService.Metadata
  │     ├── $query.env = dev
  │     ├── $header.<k> = ...
  │     └── env = dev  (CUSTOM)
  │
  ├── ruleBasedRouter 遍历服务端下发的路由规则
  │     └── 匹配 source.arguments 的维度和值
  │           ├── 匹配成功 → 按 destination.labels 筛选目标实例
  │           └── 全部规则都不匹配 → 按 failoverType 兜底
  │                  ├── all:  返回全部实例
  │                  └── none: 返回空列表（调用失败）
  │
  └── ProcessLoadBalance 从筛选结果选一个实例 → HTTP 转发
```

### 支持的 source 匹配维度

ruleBasedRouter 支持以下 Argument 类型：

| 维度 | labelKey 前缀 | 说明 |
|---|---|---|
| **QUERY** | `$query.<k>` | URL 查询参数 |
| **HEADER** | `$header.<k>` | HTTP Header |
| **COOKIE** | `$cookie.<k>` | HTTP Cookie |
| **METHOD** | `$method` | HTTP 方法（GET/POST/...） |
| **CALLER_IP** | `$caller_ip` | 主调方 IP |
| **PATH** | `$path` | URL 路径 |
| **CUSTOM** | `<k>` | 用户自定义参数（无前缀） |
| **CALLER_SERVICE** | `$caller_service.<ns>` | 主调服务命名空间+服务名 |

### 支持的匹配类型（`MatchString.type`）

| 类型 | 语义 |
|---|---|
| `EXACT` | 精确匹配 |
| `REGEX` | 正则匹配 |
| `NOT_EQUALS` | 不等 |
| `IN` | 值 ∈ 候选集（`,` 分隔） |
| `NOT_IN` | 值 ∉ 候选集 |

### failoverType 兜底语义

| 值 | 行为 | 使用场景 |
|---|---|---|
| `all`（默认） | 规则全未命中时返回全量健康实例 | 生产环境容错，防止因规则临时失配导致全链路失败 |
| `none` | 规则全未命中时返回空列表，调用方报错 | 严格灰度，要求请求必须命中规则；任何兜底都视为配置错误 |

### Polaris 服务端路由规则示例

脚本自动创建的 4 条规则（以 `env=dev` 为例）：

```json
{
  "name": "RouteEchoServer-auto-rule",
  "enable": true,
  "routing_policy": "RulePolicy",
  "sources": [{
    "service": "*",
    "namespace": "*",
    "arguments": [{
      "type": "QUERY",
      "key": "env",
      "value": {"type": "EXACT", "value_type": "TEXT", "value": "dev"}
    }]
  }],
  "destinations": [{
    "service": "RouteEchoServer",
    "namespace": "default",
    "labels": {
      "env": {"type": "EXACT", "value_type": "TEXT", "value": "dev"}
    },
    "priority": 0,
    "weight": 100
  }]
}
```

---

## polaris.yaml 配置说明

### consumer / simple-consumer / polaris.yaml

```yaml
consumer:
  serviceRouter:
    chain:
      - ruleBasedRouter       # 仅启用规则路由；本示例未启用就近路由（已注释）
    plugin:
      ruleBasedRouter:
        # 规则全部未命中时的兜底行为：
        # - all:  返回全部健康实例（宽松兜底，默认）
        # - none: 返回空列表（严格匹配，调用失败）
        failoverType: all
```

> `verify_rule_route.sh` 会为 4 条 Consumer 链路**动态生成**各自的 polaris.yaml，分别设置 `failoverType: all` 和 `failoverType: none`，放在临时工作目录中启动。

---

## 参数说明

### Provider 参数

| 参数          | 默认值            | 说明                                    |
|--------------|-------------------|----------------------------------------|
| `-namespace` | `default`         | 服务命名空间                             |
| `-service`   | `RouteEchoServer` | 服务名                                  |
| `-port`      | `0`（随机）        | 监听端口                                |
| `-metadata`  | `""`              | 实例标签，格式 `key1=value1&key2=value2` |
| `-token`     | `""`              | 鉴权 Token                              |

### Consumer 参数（手动三段式）

| 参数             | 默认值              | 说明                    |
|-----------------|---------------------|------------------------|
| `-namespace`    | `default`           | 目标服务命名空间         |
| `-service`      | `RouteEchoServer`   | 目标服务名               |
| `-selfNamespace`| `default`           | 自身命名空间              |
| `-selfService`  | `""`                | 自身服务名（影响规则的 service 匹配） |
| `-port`         | `18070`             | Consumer HTTP 监听端口   |
| `-times`        | `1`                 | 每次请求的内部调用次数（用于观察负载均衡） |
| `-token`        | `""`                | 鉴权 Token              |

### Simple Consumer 参数

| 参数             | 默认值              | 说明                |
|-----------------|---------------------|--------------------|
| `-namespace`    | `default`           | 目标服务命名空间     |
| `-service`      | `RouteEchoServer`   | 目标服务名          |
| `-selfNamespace`| `default`           | 自身命名空间         |
| `-selfService`  | `RouteEchoClient`   | 自身服务名           |
| `-port`         | `18070`             | Consumer HTTP 监听端口 |
| `-token`        | `""`                | 鉴权 Token           |
| `-debug`        | `false`             | 开启 SDK DEBUG 日志   |

---

## 测试脚本

目录下提供了以下辅助脚本：

- [`verify_rule_route.sh`](./verify_rule_route.sh)：规则路由端到端测试（4 条 Consumer 链路 × 3 个场景 = 12 个用例组）
- [`cleanup.sh`](./cleanup.sh)：残留进程清理

> **完整测试方案（目标、前置、拓扑、用例矩阵、PASS/FAIL 判定、排障）请见 👉 [test.md](./test.md)**

### verify_rule_route.sh 常用命令

```bash
./verify_rule_route.sh                                         # 默认 Polaris 127.0.0.1
./verify_rule_route.sh --polaris-server 10.x.x.x               # 指定 Polaris 地址
./verify_rule_route.sh --request-count 30                      # 每条链路每个 env 发 30 请求
./verify_rule_route.sh --failover-request-count 10             # 失败场景每条链路发 10 请求
./verify_rule_route.sh --skip-rule-check                       # 跳过规则校验/创建
./verify_rule_route.sh --polaris-token <token>                 # 鉴权 Token
```

控制台最终输出 `✅ / ⚠️ / ❌` 三态结论 + `4 链路 × 3 场景` 的结果矩阵。日志：

| 文件 | 内容 |
|---|---|
| `.logs/verify_rule_route-<时间戳>.log` | 测试总日志 |
| `.logs/provider_<env>.log` | 4 个 Provider 日志 |
| `.logs/consumer_*.log` / `simple_*.log` | 4 条 Consumer 链路日志 |

### cleanup.sh — 进程清理

```bash
./cleanup.sh            # 展示后确认（默认）
./cleanup.sh -f         # 强制清理（CI 推荐）
./cleanup.sh --dry-run  # 仅展示
```

---

## 日志与排错

| 症状 | 可能原因 | 处置 |
|---|---|---|
| 所有请求都走 failover（未命中规则） | consumer 只上报了 CUSTOM 但规则用 QUERY，或反之 | 检查 consumer `convertRouteArguments`；或检查规则 source type |
| simple-consumer 命中率低于 consumer | simple-consumer 仅上报 QUERY+Header，不上报 CUSTOM | 若规则使用 CUSTOM，请改用 consumer 或把规则改为 QUERY |
| failoverType=none 链路全部失败 | 规则未命中时返回空实例 —— 符合预期 | 确认请求带了正确的 env 参数（`?env=dev`） |
| 规则创建失败 `code=4xxxx` | Polaris 版本过低或路由 v2 API 未启用 | 升级到 v1.14.0+；检查 Polaris 配置 |
| Provider 启动后立即退出 | 端口被占用（28081-28084） | 运行 `./cleanup.sh` 清理 |
| 请求返回 `no instance available` | 同一 env 的 Provider 全部未注册成功 | 查看 `.logs/provider_<env>.log` |

### SDK 日志

```bash
tail -f ~/polaris/log/route/polaris-route.log

# 过滤规则路由相关
grep -E 'ruleBasedRouter|route rule' ~/polaris/log/route/polaris-route.log
```

开启 SDK DEBUG 日志（simple-consumer 支持）：

```bash
./bin -debug=true ...
```
