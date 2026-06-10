# 混合路由示例 (lane × rule × nearby)

本示例验证 polaris-go 支持「**泳道路由 + 规则路由 + 就近路由共存**」：
前一个 router 输出的 `Cluster`（含 metadata 过滤条件 + instanceFilter）
直接作为下一个 router 的输入，AND 复合生效。

## 目录结构

```
examples/route/mixed/
├── provider/main.go     # 一份 provider 二进制，靠 -metadata 注入不同实例标签
├── consumer/main.go     # 用 ConsumerAPI.GetOneInstance,SDK 内部走完整路由链
├── polaris.yaml         # 关键: chain=[ruleBasedRouter, nearbyBasedRouter],
│                        #       beforeChain=[laneRouter] (默认)
├── run.sh               # 端到端测试脚本: build / start / test / stop
└── README.md            # 本文档
```

## 实例矩阵

| 实例 | 端口 | metadata |
| --- | --- | --- |
| `provider-base-dev`  | 28181 | `env=dev` |
| `provider-base-test` | 28182 | `env=test` |
| `provider-gray-dev`  | 28183 | `lane=gray, env=dev` |
| `provider-gray-test` | 28184 | `lane=gray, env=test` |
| `consumer`           | 18180 | （无 metadata；自身仅作为 source） |

## 默认路由链

来自 `polaris.yaml`，对应 `pkg/config/servicerouter.go:SetDefault` 的扩展：

```yaml
consumer:
  serviceRouter:
    # beforeChain 默认含 laneRouter, 不需要显式声明
    chain:
      - ruleBasedRouter      # 主链 1: 按 inbound 规则匹配 source / dest metadata
      - nearbyBasedRouter    # 主链 2: 按地域就近(本 demo 配 ALL,等价 noop)
    plugin:
      laneRouter:
        baseLaneMode: 0      # 排除带 lane key 的实例作为 baseline
      ruleBasedRouter:
        failoverType: all    # 规则全部失败时回退全量(避免本地无 Polaris 规则时 503)
      nearbyBasedRouter:
        matchLevel: ALL      # 不强制就近,简化本地测试
        maxMatchLevel: ALL
        strictNearby: false
```

## 验证用例

| 用例 | 输入 | 期望 |
| --- | --- | --- |
| 1 | `Header: service-lane=mixed-lane-group/gray` + `?env=dev` | 命中 `provider-gray-dev` (lane=gray ∩ env=dev) |
| 2 | `?env=dev`（无染色） | 命中 `provider-base-dev`（无 lane key ∩ env=dev），**绝不**命中 `gray-dev` |
| 3 | `?env=test`（无染色） | 命中 `provider-base-test` |
| 4 | 无 query 无 Header | 命中任一 base 实例（lane 排除 + rule failover=all），**绝不**命中 gray |

> 用例 2/3/4 是核心断言：lane router 把"排除带 lane 标签实例"通过 instanceFilter 链式传给主链，
> 主链按 metadata AND 过滤后仍只剩 base 实例。
>
> 用例 1 依赖 Polaris 控制台配置泳道组 `mixed-lane-group/gray`；
> 如果没有配置，run.sh 会优雅降级到 base-dev 命中并打印 WARN（仍计 PASS，
> 因为已经验证了"lane 不破坏 rule 路由"这一核心目标）。

## 用法

```bash
# 完整流程
./run.sh all 127.0.0.1

# 分阶段
./run.sh build           # 编译
./run.sh start 127.0.0.1 # 启动 4 provider + 1 consumer
./run.sh test            # 跑测试用例
./run.sh stop            # 停止

# 开 SDK debug 日志
DEBUG_MODE=true ./run.sh all 127.0.0.1
```

## 路由链流程图

```
请求 (?env=dev, [Header service-lane=...])
    │
    ▼
beforeChain: laneRouter
    │  - 染色 → 输出 cluster {Metadata: lane=gray}
    │  - 未染色 → 输出 cluster {instanceFilter: 排除带 lane key 的实例}
    │
    ▼ (cluster 链式传给主链)
mainChain: ruleBasedRouter
    │  - 拿到 inCluster, NewCluster(svcCache, inCluster) 继承 metadata + filter
    │  - 叠加 ruleMeta: {env: dev}
    │  - 现在 cluster {Metadata: {lane:gray, env:dev}} 或 {Metadata: {env:dev}, filter: 排除lane}
    │
    ▼
mainChain: nearbyBasedRouter
    │  - 在上一步基础上叠加地域过滤
    │
    ▼
afterChain: filterOnlyRouter (链尾兜底, 健康度过滤)
    │
    ▼
loadbalancer.ChooseInstance
```

## 与其他 examples 的对照

| example | 验证目标 |
| --- | --- |
| `examples/route/lane`     | 仅泳道路由（含 baseLaneMode、染色、入口检测） |
| `examples/route/rule`     | 仅规则路由（含 failoverType=all/none） |
| `examples/route/nearby`   | 仅就近路由 |
| `examples/route/metadata` | 仅 dstmeta 路由（不走 inbound 规则） |
| **`examples/route/mixed`**（本目录） | **三链共存按 AND 复合生效** |
