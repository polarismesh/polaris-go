# 泳道路由（Lane Router）示例

## 功能说明

泳道路由（Lane Router）是一种流量隔离机制，通过将实例打上泳道标签（如 `lane=gray`、`lane=stable`）将微服务集群划分为若干逻辑分组（泳道），实现灰度发布、A/B 测试等场景。

本示例演示以下核心能力：

- **Provider 侧**：注册带有不同泳道标签的服务实例
  - `lane=gray`：灰度泳道实例
  - `lane=stable`：稳定泳道实例
  - *(无标签)*：基线实例，所有无法匹配泳道的流量兜底到此

- **Consumer 侧**：依据 HTTP 请求头中的染色标签（`service-lane`）路由到对应泳道

---

## 前置条件

1. 已部署并运行 [Polaris 服务端](https://github.com/polarismesh/polaris)（版本需支持泳道路由，建议 v1.19.0+）。
2. 在 Polaris 控制台创建泳道组（LaneGroup），指定入口服务、目标服务（destinations）和流量识别规则（TrafficMatchRule）。
3. 各示例目录下的 `polaris.yaml` 需将 `global.serverConnector.addresses` 指向 Polaris 服务端的注册发现端口（默认 `8091`）。

> **提示**：如仅运行测试脚本（`lane-test.sh` / `lane-warmup-test.sh`），脚本会自动通过 Polaris 管理 API 检测并修复所需的泳道规则，无需手动在控制台配置。

---

## 目录结构

```
lane/
├── gateway/          # 泳道网关入口（染色点，反向代理到下游泳道实例）
│   ├── main.go
│   ├── go.mod
│   ├── Makefile
│   └── polaris.yaml
├── provider/         # 服务提供端（注册带泳道标签的实例）
│   ├── main.go
│   ├── go.mod
│   ├── Makefile
│   └── polaris.yaml
├── consumer/         # 服务消费端（显式 ProcessRouters + ProcessLoadBalance）
│   ├── main.go
│   ├── go.mod
│   ├── Makefile
│   └── polaris.yaml
├── simple-consumer/  # 简化消费端（基于 GetOneInstance 一步完成路由）
│   ├── main.go
│   ├── go.mod
│   ├── Makefile
│   └── polaris.yaml
├── polaris.yaml      # SDK 顶层默认配置
├── lane-test.sh      # 泳道路由端到端测试脚本
├── lane-warmup-test.sh # 泳道预热（Warmup）测试脚本
├── cleanup.sh        # 残留进程清理脚本
└── README-zh.md
```

---

## 快速开始

### 1. 启动 Provider

分别启动三种泳道的 Provider：

```bash
cd provider
make build

# 终端 1：灰度泳道实例（携带 lane=gray 元数据）
cd lane-gray && ./bin -lane=gray -port=18081

# 终端 2：稳定泳道实例（携带 lane=stable 元数据）
cd lane-stable && ./bin -lane=stable -port=18082

# 终端 3：基线实例（不携带泳道元数据）
cd lane-baseline && ./bin -lane= -port=18083
```

或者直接使用 make 命令：

```bash
make run-gray      # 启动灰度泳道
make run-stable    # 启动稳定泳道
make run-baseline  # 启动基线
```

### 2. 启动 Consumer

```bash
cd consumer
make run
```

Consumer 默认监听 `19080` 端口。

### 3. 发送测试请求

#### 路由到灰度泳道

在请求头中携带 `service-lane: gray/rule1`（值格式为 `{laneGroupName}/{laneRuleName}`）：

```bash
curl -H "service-lane: gray/rule1" http://127.0.0.1:19080/echo
```

预期响应示例：
```
Hello, I'm LaneEchoServer. lane=gray, host=127.0.0.1:18081
```

#### 路由到稳定泳道

```bash
curl -H "service-lane: stable/rule2" http://127.0.0.1:19080/echo
```

预期响应示例：
```
Hello, I'm LaneEchoServer. lane=stable, host=127.0.0.1:18082
```

#### 路由到基线（无染色标签）

```bash
curl http://127.0.0.1:19080/echo
```

预期响应示例：
```
Hello, I'm LaneEchoServer. lane=(baseline), host=127.0.0.1:18083
```

---

## 路由逻辑说明

### Consumer 端路由流程

```
HTTP 请求
  │
  ├── 携带 service-lane 请求头？
  │     ├── 是 → 从 header 提取染色标签 → 注入 EnvironmentVariables["service-lane"]
  │     │       → LaneRouter.matchByStainLabel() 直接匹配泳道规则
  │     └── 否 → 将 HTTP header/query 转为 RouteArguments
  │               → LaneRouter.matchByRouteInfo() 按 TrafficMatchRule 识别流量
  │
  ├── 找到匹配的泳道规则？
  │     ├── 是 → 执行灰度染色概率判断（TrafficGray）
  │     │       → 检查目标服务是否在泳道 destinations 中
  │     │       → 路由到带 lane={value} 元数据的实例
  │     └── 否 → 路由到基线实例（无 lane 元数据的实例）
  │
  └── 透传染色标签给下游服务
        → 下游 HTTP 请求头中携带 service-lane: {stainLabel}
```

### 泳道规则（需在 Polaris 控制台配置）

下表以虚构的 `gray` / `stable` 两个泳道组为例，说明「染色标签」与「目标实例 `lane` 元数据」的对应关系。
测试脚本 `lane-test.sh` 使用的是泳道组 `lane-go-example`、规则 `gray`，染色标签为 `lane-go-example/gray`。

| 泳道组名 | 泳道规则名 | 染色标签（stainLabel） | 目标实例标签    |
|---------|-----------|---------------------|---------------|
| gray    | rule1     | `gray/rule1`        | `lane=gray`   |
| stable  | rule2     | `stable/rule2`      | `lane=stable` |

### 实例元数据

| 实例类型 | `lane` 元数据值 | 说明                |
|---------|---------------|-------------------|
| 灰度实例 | `gray`        | 处理灰度泳道流量      |
| 稳定实例 | `stable`      | 处理稳定泳道流量      |
| 基线实例 | *(无此 key)*  | 兜底，处理未染色流量  |

---

## Consumer 配置说明（polaris.yaml）

```yaml
consumer:
  serviceRouter:
    # laneRouter 在 beforeChain 中默认启用，无需在 chain 中重复配置
    chain: []
    plugin:
      laneRouter:
        # baseLaneMode 基线泳道模式：
        #   0 = OnlyUntaggedInstance       只选取没有任何泳道标签的实例作为基线（默认）
        #   1 = ExcludeEnabledLaneInstance  排除已启用泳道规则所关联的实例，其余实例作为基线
        baseLaneMode: 0
```

### baseLaneMode 行为详解

当路由命中「需回退基线」的场景（如规则未命中、目标服务不在泳道 destinations、PERMISSIVE 降级等）时，
`laneRouter` 按以下顺序选取基线候选实例：

1. **优先选取「无 `lane` 元数据 key」的实例**。若存在则直接返回，与 `baseLaneMode` 取值无关。
2. 若上一步为空，且 `baseLaneMode = 1`（`ExcludeEnabledLaneInstance`），则退化为「排除已启用泳道对应标签值的实例」，返回剩余实例。
3. 仍为空时兜底返回全量实例，避免链路中断。

> **实践建议**：若 Provider 基线实例未携带 `lane` 元数据（推荐做法），使用默认 `baseLaneMode=0` 即可；
> 若基线实例也被迫打上了 `lane=baseline` 等标签值，则需要将 `baseLaneMode` 设为 `1`。

---

## 泳道降级说明

| 场景                        | STRICT 模式           | PERMISSIVE 模式（默认）|
|----------------------------|----------------------|----------------------|
| 目标泳道无存活实例            | 返回 filterOnly 状态  | 回退到基线实例         |
| 目标服务不在泳道 destinations | 回退到基线实例         | 回退到基线实例         |
| 无匹配的泳道规则              | 回退到基线实例         | 回退到基线实例         |

---

## 参数说明

### Provider 参数

| 参数          | 默认值          | 说明                                    |
|--------------|----------------|----------------------------------------|
| `-namespace` | `default`      | 服务命名空间                             |
| `-service`   | `LaneEchoServer` | 服务名                                |
| `-port`      | `0`（随机）     | 监听端口                                |
| `-lane`      | `""`（基线）    | 泳道标签值，如 `gray`、`stable`、空字符串 |
| `-token`     | `""`           | 服务鉴权 Token                          |
| `-debug`     | `false`        | 是否开启 Polaris SDK debug 日志          |

### Consumer 参数

| 参数              | 默认值           | 说明                    |
|------------------|-----------------|------------------------|
| `-namespace`     | `default`       | 目标服务命名空间          |
| `-service`       | `LaneEchoServer` | 目标服务名              |
| `-selfNamespace` | `default`       | 当前服务命名空间（用于流量入口匹配）|
| `-selfService`   | `LaneEchoClient` | 当前服务名              |
| `-port`          | `19080`         | Consumer HTTP 监听端口（0 表示随机） |
| `-times`         | `1`             | 每次请求的转发次数         |
| `-lane`          | `""`（基线）     | Consumer 自身的泳道标签（用于将 Consumer 实例也纳入泳道）|
| `-token`         | `""`            | 服务鉴权 Token            |
| `-debug`         | `false`         | 是否开启 Polaris SDK debug 日志 |

### Gateway 参数

| 参数              | 默认值                        | 说明                              |
|------------------|------------------------------|----------------------------------|
| `-selfNamespace` | `default`                    | 网关自身的 namespace（用于泳道入口匹配）|
| `-selfService`   | `LaneRouterGateway`          | 网关自身的服务名（泳道入口服务）      |
| `-namespace`     | `default`                    | 目标服务的 namespace               |
| `-port`          | `48080`                      | 网关 HTTP 监听端口                  |
| `-token`         | `""`                         | 服务鉴权 Token                     |
| `-debug`         | `false`                      | 是否开启 Polaris SDK debug 日志      |

### Simple Consumer 参数

Simple Consumer 基于 `GetOneInstance` 一步完成服务发现 + 路由过滤 + 负载均衡，适用于不需要手动控制路由流程的场景。

| 参数          | 默认值           | 说明                    |
|--------------|-----------------|------------------------|
| `-namespace` | `default`       | 目标服务命名空间          |
| `-service`   | `LaneEchoServer` | 目标服务名              |
| `-port`      | `19081`         | Consumer HTTP 监听端口  |
| `-debug`     | `false`         | 是否开启 Polaris SDK debug 日志 |

---

## 测试脚本

目录下提供了三个辅助脚本，用于自动化构建、启动、测试和清理：

### 测试用例覆盖

**lane-test.sh**（共 7 个用例）：

| 用例 | 场景                                                | 验证点                                          |
|-----|----------------------------------------------------|------------------------------------------------|
| 1   | 无 Header                                           | 全链路路由到基线实例 `lane=(baseline)`             |
| 2   | `service-lane` 直接染色                              | laneRouter 按染色标签直接路由到 gray 泳道         |
| 3   | 流量匹配 STRICT — Header `user=gray`                 | Gateway 按 TrafficMatchRule 染色，路由到 gray 泳道 |
| 4   | 流量匹配 PERMISSIVE — Header `user=noexist`          | 无目标泳道实例时自动回退基线                       |
| 5   | 未命中任何规则的 Header                               | 网关无法染色，回退基线                             |
| 6   | 泳道隔离并发验证                                      | baseline 与 gray 请求并发时互不干扰                |
| 7   | 服务移出泳道组（`OnlyUntaggedInstance` 模式）          | 移除 `LaneEchoServer` 后染色请求只走无标签基线实例   |

**lane-warmup-test.sh**（共 5 个用例）：

| 用例 | 场景                 | 验证点                                                         |
|-----|---------------------|---------------------------------------------------------------|
| 1   | 预热早期阶段          | uptime 极小时 probability ≈ 0%，绝大多数请求回退基线             |
| 2   | 预热进行中           | 按曲线采样多个时间点，验证实际 gray 比例与 `pow(u/i, c)` 理论值接近  |
| 3   | 预热完成             | uptime ≥ warmup_interval 后，gray 比例稳定在 100%                |
| 4   | 基线路由不受预热影响   | 无染色请求始终路由到基线                                         |
| 5   | 染色 / 基线并发隔离   | 预热期内两类流量独立判定，不互相串扰                              |

---

### lane-test.sh — 泳道路由测试

一键完成泳道路由功能的端到端验证，覆盖灰度路由和 PERMISSIVE 降级场景。

```bash
# 用法
./lane-test.sh <命令> [polaris地址]

# 命令说明
#   all     完整流程（构建 → 验证规则 → 启动 → 等待 → 测试 → 停止）
#   build   仅构建 Go 二进制
#   check   仅检查 Polaris 泳道规则是否已正确配置
#   start   构建并启动服务（含规则检查）
#   test    执行测试用例（服务需已启动）
#   stop    停止所有服务
```

**示例：**

```bash
# 对本地 Polaris 执行完整测试流程
./lane-test.sh all 127.0.0.1

# 仅构建二进制（不启动服务）
./lane-test.sh build

# 服务已启动，单独执行测试用例
./lane-test.sh test

# 停止所有服务
./lane-test.sh stop
```

**前置规则配置要求：**

| 泳道组名             | 入口服务              | 目标服务                          |
|---------------------|----------------------|----------------------------------|
| `lane-go-example`   | `LaneRouterGateway`  | `LaneEchoClient`, `LaneEchoServer` |

> **注意**：脚本启动时会自动检查泳道规则配置。如果检测到目标服务缺失（例如上次测试未正确恢复），脚本会自动调用 Polaris 管理 API 修复，无需手动干预。

| 泳道规则名      | 匹配条件              | 目标泳道          | 匹配模式       |
|---------------|----------------------|-----------------|--------------|
| `gray`        | Header `user=gray`   | `lane=gray`     | STRICT       |
| `permissive`  | Header `user=noexist` | `lane=noexist`  | PERMISSIVE   |

---

### lane-warmup-test.sh — 泳道预热测试

验证泳道预热（Warmup）功能，通过统计灰度泳道的流量比例验证预热曲线是否符合预期。

```bash
# 用法
./lane-warmup-test.sh <命令> [polaris地址]

# 命令说明（与 lane-test.sh 一致）
#   all     完整流程（构建 → 验证规则 → 启动 → 等待 → 测试 → 停止）
#   build   仅构建 Go 二进制
#   check   仅检查 Polaris 泳道规则
#   start   构建并启动服务（含规则检查）
#   test    执行测试用例（服务需已启动）
#   stop    停止所有服务
```

**示例：**

```bash
# 对本地 Polaris 执行完整预热测试
./lane-warmup-test.sh all 127.0.0.1

# 仅检查预热规则是否配置正确
./lane-warmup-test.sh check 127.0.0.1
```

**预热算法：**

```
probability = pow(uptime / warmup_interval, curvature) × 100%
uptime      = 当前时间 - etime（规则最近一次启用时间）
```

当 `uptime >= warmup_interval` 时，probability = 100%（预热完成）。

**前置规则配置要求：**

| 泳道组名           | 入口服务                    | 目标服务                          |
|-------------------|---------------------------|----------------------------------|
| `lane-go-warmup`  | `LaneRouterGatewayService` | `LaneEchoClient`, `LaneEchoServer` |

| 泳道规则名 | default_label_value | warmup_interval | curvature | enable |
|-----------|---------------------|-----------------|-----------|--------|
| `gray`    | `gray`              | 60（秒）         | 2         | true   |

> **注意**：`lane-warmup-test.sh` 与 `lane-test.sh` 使用相同的服务端口，不可同时运行。

---

### cleanup.sh — 进程清理

清理泳道测试残留的 provider / consumer 进程，支持 `lane-test.sh` 和 `lane-warmup-test.sh` 启动的所有进程。

```bash
# 用法
./cleanup.sh [选项]

# 选项说明
#   （无参数）     默认模式：先展示匹配的进程，确认后清理
#   -f, --force   强制模式：直接清理，不需要确认
#   --dry-run     仅展示匹配的进程，不执行清理
#   -h, --help    显示帮助信息
```

**示例：**

```bash
# 交互式清理（先展示再确认）
./cleanup.sh

# 强制清理（CI 环境推荐）
./cleanup.sh -f

# 仅查看残留进程
./cleanup.sh --dry-run
```

---

## 日志与排错

Polaris SDK 的日志按类别输出到 `$HOME/polaris/log/` 下的多个子目录，路由相关问题请优先查看路由日志：

| 日志目录            | 内容                                              |
|--------------------|--------------------------------------------------|
| `base/`            | SDK 通用日志（初始化、配置加载等）                 |
| `route/`           | **路由插件日志**（laneRouter、ruleBasedRouter 等） |
| `network/`         | 与 Polaris 服务端交互的网络日志                    |
| `stat/` `detect/`  | 统计 / 健康探测                                    |
| `lossless/`        | 无损上下线                                         |

排查泳道路由问题的常用操作：

1. 通过 `-debug` 参数（gateway / consumer / provider / simple-consumer 均支持）开启 DEBUG 日志：
   ```bash
   ./bin -debug=true ...
   ```
2. 查看路由日志：
   ```bash
   tail -f ~/polaris/log/route/polaris-route.log
   ```
3. 关键字检索：
   ```bash
   grep -E '\[Router\]\[Lane\]' ~/polaris/log/route/polaris-route.log
   ```
