# 就近路由（Nearby Router）示例

## 功能说明

就近路由（Nearby Router）通过 SDK 内置的 **`nearbyBasedRouter`** 插件，依据实例的**地域信息（Region / Zone / Campus）**将请求优先路由到与 Consumer 处于同一地域的 Provider，降低跨地域延迟。

本示例演示以下核心能力：

- **Provider 侧**：在不同 `Campus` 注册多个实例（同 Region / 同 Zone）
- **Consumer 侧**：通过两种调用方式让 SDK 完成就近过滤
  - **simple-consumer**：`GetOneInstance` 一步到位（polaris.yaml 启用 `nearbyBasedRouter`）
  - **consumer**：`GetAllInstances` → `ProcessRouters`（规则路由 + 就近路由）→ `ProcessLoadBalance`

匹配层级（由高到低）：**Campus → Zone → Region → All**
- `matchLevel: campus` 表示从 Campus 开始向上匹配，同 Campus 的实例优先；若无则退化到同 Zone，以此类推
- `maxMatchLevel: unknown`（默认）表示不设退化上限
- `strictNearby: false`（默认）允许在最高层级也无匹配时返回全量实例

> **依赖**：就近路由**必须**在 Polaris 服务端为目标服务开启；`verify_nearby_route.sh` 会通过管理 API 自动创建/启用规则。

---

## 前置条件

1. 已部署并运行 [Polaris 服务端](https://github.com/polarismesh/polaris)（版本需支持就近路由规则，建议 v1.17.0+）。
2. 在 Polaris 控制台**或通过脚本自动创建**目标服务 `RouteNearbyEchoServer` 的就近路由规则（NearbyPolicy）。
3. `polaris.yaml` 中 `global.serverConnector.addresses` 通过 `${POLARIS_SERVER}` 环境变量注入。
4. 各进程通过 `${REGION} / ${ZONE} / ${CAMPUS}` 环境变量注入自身地域信息，SDK 的 `global.location.providers[type=local]` 据此生成 Location。
5. Go 1.18+。

> **提示**：直接运行 `verify_nearby_route.sh` 会自动处理规则创建、地域注入、进程启动。

---

## 目录结构

```
nearby/
├── provider/                  # Provider 示例（携带地域元数据）
│   ├── main.go
│   ├── polaris.yaml
│   ├── provider-a/            # 预配置的 provider-a（含独立 polaris.yaml）
│   └── provider-b/            # 预配置的 provider-b
├── consumer/                  # 手动三段式 Consumer（ProcessRouters + ProcessLoadBalance）
│   ├── main.go
│   └── polaris.yaml
├── simple-consumer/           # 简化 Consumer（GetOneInstance）
│   ├── main.go
│   ├── polaris.yaml
│   └── run.sh                 # Simple Consumer 启动示例
├── k8s/                       # Kubernetes 部署清单
│   ├── configmap-provider.yaml
│   ├── configmap-consumer.yaml
│   ├── deployment-provider.yaml
│   └── deployment-consumer.yaml
├── image/                     # 文档截图
├── verify_nearby_route.sh     # 就近路由端到端测试脚本（两条链路 × 多轮验证）
├── cleanup.sh                 # 残留进程清理脚本
├── README-zh.md
├── README.md                  # 英文版本
└── test.md                    # 测试方案文档
```

---

## 快速开始

### 1. 启动 3 个 Provider（不同 Campus）

```bash
cd provider
go build -o bin

# 终端 1：provider-1 @ ap-guangzhou-1
REGION=china ZONE=ap-guangzhou CAMPUS=ap-guangzhou-1 \
POLARIS_SERVER=127.0.0.1 ./bin -port=28091

# 终端 2：provider-2 @ ap-guangzhou-2
REGION=china ZONE=ap-guangzhou CAMPUS=ap-guangzhou-2 \
POLARIS_SERVER=127.0.0.1 ./bin -port=28092

# 终端 3：provider-3 @ ap-guangzhou-3
REGION=china ZONE=ap-guangzhou CAMPUS=ap-guangzhou-3 \
POLARIS_SERVER=127.0.0.1 ./bin -port=28093
```

### 2. 启动 Consumer（两条链路任选其一）

#### 链路 A：simple-consumer（GetOneInstance）

```bash
cd simple-consumer
go build -o bin
REGION=china ZONE=ap-guangzhou CAMPUS=ap-guangzhou-1 \
POLARIS_SERVER=127.0.0.1 ./bin -port=18080
```

#### 链路 B：consumer（ProcessRouters + ProcessLoadBalance）

```bash
cd consumer
go build -o bin
REGION=china ZONE=ap-guangzhou CAMPUS=ap-guangzhou-1 \
POLARIS_SERVER=127.0.0.1 ./bin -port=18081
```

### 3. 在 Polaris 控制台开启就近路由

进入目标服务 `RouteNearbyEchoServer` 的「服务治理」→「路由规则」→「就近路由」页面，按如下配置启用：

| 字段 | 值 |
|---|---|
| 启用 | ✅ |
| Match Level | `CAMPUS` |
| Max Match Level | `UNKNOWN`（不限制） |
| Strict Nearby | `false` |

> 或直接跑 `./verify_nearby_route.sh` 让脚本通过 `POST /naming/v2/routings` 自动创建规则。

### 4. 发送测试请求

```bash
# Consumer 位于 campus=ap-guangzhou-1
# 预期：所有请求都应路由到 provider-1 (:28091)
for i in $(seq 1 10); do curl -s http://127.0.0.1:18080/echo; echo; done
```

响应示例：

```
Hello, I'm RouteNearbyEchoServer Provider, MyLocInfo's : {"region":"china","zone":"ap-guangzhou","campus":"ap-guangzhou-1"}, host : 10.x.x.x:28091
```

### 5. 一键跑完整验证

```bash
./verify_nearby_route.sh --polaris-server 127.0.0.1
```

脚本会自动：编译 → 创建/启用就近路由规则 → 启动 3 Provider + 2 Consumer → 各链路发 20 个请求 → 汇总结论。

---

## 链路组合与两种调用方式

| 链路 | 调用风格 | Consumer 端口 | polaris.yaml `serviceRouter.chain` |
|---|---|---|---|
| A  `simple-consumer` | `GetOneInstance` 一步到位 | `:18080` | `[ruleBasedRouter, nearbyBasedRouter]` |
| B  `consumer` | `GetAllInstances + ProcessRouters + ProcessLoadBalance` 手动三段式 | `:18081` | `[ruleBasedRouter, nearbyBasedRouter]` |

> 两条链路的 `polaris.yaml` 配置完全一致，区别仅在于 Consumer 侧调用 SDK 的 API 风格。`GetOneInstance` 内部等价于 `GetAllInstances + ProcessRouters + ProcessLoadBalance`。

---

## 路由逻辑说明

### SDK 就近路由流程

```
Consumer 请求到达 /echo
  │
  ├── 读取本进程 SDK Location（来自 global.location.providers）
  │     ├── type=local   → 从环境变量 REGION/ZONE/CAMPUS 读取（本示例）
  │     ├── type=remoteHttp / remoteService → 从外部服务获取
  │     └── 若全部失败，location 为空 → 走全量实例
  │
  ├── ConsumerAPI 调用（GetOneInstance 或 GetAllInstances+ProcessRouters）
  │     └── serviceRouter.chain：
  │           ├── ruleBasedRouter（若无规则直通）
  │           └── nearbyBasedRouter
  │                 ├── 按 matchLevel 逐级匹配（campus → zone → region → all）
  │                 ├── maxMatchLevel 限制退化上限
  │                 └── strictNearby=true 时只允许在 matchLevel 一层匹配
  │
  └── 选中的实例 → HTTP 转发 /echo
```

### 地域信息（Location）来源

SDK 通过 `global.location.providers` 插件生成 Consumer 自身的 Location：

| `type` | 来源 | 本示例 |
|---|---|---|
| `local` | 启动环境变量 `REGION / ZONE / CAMPUS` | ✅ |
| `remoteHttp` | HTTP 接口拉取（适合 k8s 节点 metadata） | — |
| `remoteService` | 另一个北极星服务提供 | — |

> **实践建议**：
> - 物理机 / 虚拟机：用 `local`，通过 CMDB 模板注入环境变量
> - Kubernetes：用 `remoteHttp` 读取 node label（`topology.kubernetes.io/region` 等），见 `k8s/` 目录
> - 跨云多活：用 `remoteService` 由专门的位置服务统一下发

### Polaris 服务端就近路由规则（NearbyPolicy）

脚本自动创建的规则：

```json
{
  "name": "RouteNearbyEchoServer-auto-nearby",
  "enable": true,
  "routing_policy": "NearbyPolicy",
  "routing_config": {
    "@type": "type.googleapis.com/v1.NearbyRoutingConfig",
    "service": "RouteNearbyEchoServer",
    "namespace": "default",
    "match_level": "CAMPUS",
    "max_match_level": "UNKNOWN",
    "strict_nearby": false
  }
}
```

---

## polaris.yaml 配置说明

两条链路的 `polaris.yaml` 内容基本一致：

```yaml
global:
  location:
    providers:
      - type: local
        options:
          region: ${REGION}
          zone: ${ZONE}
          campus: ${CAMPUS}

consumer:
  serviceRouter:
    chain:
      - ruleBasedRouter      # 规则路由（先执行）
      - nearbyBasedRouter    # 就近路由（后执行）
    plugin:
      nearbyBasedRouter:
        # 就近路由的最小匹配级别，范围：region / zone / campus
        matchLevel: campus
```

### 关键配置字段

| 字段 | 范围 | 说明 |
|---|---|---|
| `nearbyBasedRouter.matchLevel` | `region` / `zone` / `campus` | 从该层级开始匹配，逐级向上退化 |
| `nearbyBasedRouter.maxMatchLevel` | `region` / `zone` / `campus` / `unknown`（不限） | 退化上限；超过该层级仍无匹配则兜底 |
| `nearbyBasedRouter.strictNearby` | `true` / `false` | `true` 时仅允许在 matchLevel 一层匹配，不退化 |
| `nearbyBasedRouter.unHealthyPercentToDegrade` | 0-100 | 本地域实例不健康比例超过该阈值时降级 |

---

## 参数说明

### Provider 参数

| 参数          | 默认值                    | 说明                                    |
|--------------|---------------------------|----------------------------------------|
| `-namespace` | `default`                 | 服务命名空间                             |
| `-service`   | `RouteNearbyEchoServer`   | 服务名                                  |
| `-host`      | `""`（自动探测）            | 监听 IP                                  |
| `-port`      | `0`（随机）                 | 监听端口                                 |
| `-metadata`  | `""`                      | 实例标签，格式 `key1=value1&key2=value2` |
| `-token`     | `""`                      | 鉴权 Token                              |

### Consumer 参数（手动三段式）

| 参数             | 默认值                  | 说明                          |
|-----------------|-------------------------|------------------------------|
| `-namespace`    | `default`               | 被调服务命名空间               |
| `-service`      | `RouteNearbyEchoServer` | 被调服务名                    |
| `-selfNamespace`| `default`               | 自身命名空间                  |
| `-selfService`  | `RouteNearbyEchoClient` | 自身服务名                    |
| `-selfRegister` | `false`                 | 是否自注册到 Polaris          |
| `-port`         | `18080`                 | Consumer HTTP 监听端口（18081 为手工流程示例） |
| `-token`        | `""`                    | 鉴权 Token                    |
| `-debug`        | `false`                 | 开启 SDK DEBUG 日志           |

### Simple Consumer 参数

与 Consumer 参数完全相同（默认端口 `18080`）。

---

## 测试脚本

目录下提供了以下辅助脚本：

- [`verify_nearby_route.sh`](./verify_nearby_route.sh)：就近路由端到端测试（3 Provider × 2 Consumer 链路 × N 次请求）
- [`cleanup.sh`](./cleanup.sh)：残留进程清理

> **完整测试方案（目标、前置、拓扑、用例、PASS/FAIL 判定、排障）请见 👉 [test.md](./test.md)**

### verify_nearby_route.sh 常用命令

```bash
./verify_nearby_route.sh                                   # 默认 Polaris 127.0.0.1
./verify_nearby_route.sh --polaris-server 10.x.x.x         # 指定 Polaris 地址
./verify_nearby_route.sh --request-count 50                # 每个链路发 50 个请求
./verify_nearby_route.sh --rule-match-level ZONE           # 规则 match_level 改 ZONE
./verify_nearby_route.sh --rule-strict-nearby true         # 启用严格就近
./verify_nearby_route.sh --skip-rule-check                 # 跳过规则校验/创建
./verify_nearby_route.sh --no-auto-create                  # 规则不存在时仅报错而不创建
./verify_nearby_route.sh --polaris-token <token>           # 鉴权 Token
```

脚本执行完会在控制台打出 `✅ / ⚠️ / ❌` 三态结论，详细日志：

| 文件 | 内容 |
|---|---|
| `.logs/verify_nearby_route-<时间戳>.log` | 测试总日志 |
| `.logs/provider_1.log` ... `provider_3.log` | Provider 启动与请求日志 |
| `.logs/simple_consumer.log` / `consumer.log` | 两条 Consumer 链路的请求日志 |

### cleanup.sh — 进程清理

```bash
./cleanup.sh            # 展示后确认（默认）
./cleanup.sh -f         # 强制清理（CI 推荐）
./cleanup.sh --dry-run  # 仅展示
```

---

## Kubernetes 部署

`k8s/` 目录下提供 Deployment + ConfigMap 样例，展示：

- 通过 `configmap-provider.yaml` / `configmap-consumer.yaml` 下发 `polaris.yaml`
- 通过 Pod 环境变量（`REGION / ZONE / CAMPUS` 来自 node label）注入地域
- Provider 使用 DaemonSet 部署到每个节点
- Consumer 通过 Service / Ingress 暴露

> 生产部署时建议改用 `type: remoteHttp` 的 `location.providers`，通过 K8s downward API 或自定义 HTTP 接口获取地域信息。

---

## 日志与排错

| 症状 | 可能原因 | 处置 |
|---|---|---|
| 请求未按就近匹配（分散到所有 Provider） | Polaris 控制台未开启就近路由 / 规则未 enable | 运行 `./verify_nearby_route.sh` 自动创建；或控制台手动启用 |
| 所有请求仍走 provider-2/3（非同 campus） | Consumer 的 `CAMPUS` 环境变量未设置或值不对 | `env \| grep CAMPUS` 检查；确认 SDK 日志 `get location from local` |
| 日志 `empty location` | `global.location.providers` 未配置或环境变量全空 | 检查 `polaris.yaml` 的 location 配置段 |
| 同 campus 实例挂了后请求失败 | `strictNearby=true` 时不允许退化 | 改为 `false` 或设置 `maxMatchLevel` |
| 规则创建失败 `code=xxx` | Polaris 鉴权未开 token 或版本不支持 | 传 `--polaris-token`；升级 Polaris 到 v1.17.0+ |

### SDK 日志

```bash
# 路由插件日志
tail -f ~/polaris/log/route/polaris-route.log

# 过滤就近路由相关
grep -E 'nearby|Nearby|location' ~/polaris/log/route/polaris-route.log
```

开启 SDK DEBUG 日志：

```bash
./bin -debug=true ...
```
