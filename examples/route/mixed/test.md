# 混合路由 E2E 测试用例（lane × rule × nearby）

> 本文档描述 `examples/route/mixed/run.sh` 的完整用例矩阵、规则配置、断言逻辑。
>
> 涉及代码：`plugin/servicerouter/lane/lane_router.go` /
> `plugin/servicerouter/rulebase/` / `plugin/servicerouter/nearbybase/` /
> `pkg/model/cluster.go`（`instanceFilter` 链式继承）。

---

## 1. 验证目标

1. **三链共存**：lane router（beforeChain）→ ruleBasedRouter（chain）→ nearbyBasedRouter（chain）→ filterOnlyRouter（afterChain）按 AND 复合生效。
2. **lane router 输出作为下游输入**：lane 命中泳道时输出 `Cluster{Metadata: lane=val}`；lane 走 baseline 时输出 `Cluster{instanceFilter}`，两种 cluster 都通过 `model.NewCluster(svcCache, prevCluster)` 链式继承到主链，不再短路 FilterOnly 兜底。
3. **lane STRICT/PERMISSIVE 与 rule failoverType=all/none 的排列组合**：
   - STRICT × all：lane 严格无实例 → 503；
   - STRICT × none：同上（lane 短路在前）；
   - PERMISSIVE × all：lane 回退基线 → rule 找不到 → 兜底全量；
   - PERMISSIVE × none：lane 回退基线 → rule 找不到 → 严格 503；
   - 不染色 × rule 命中 × env 匹配 → 命中目标实例。
4. **就近路由 matchLevel=ZONE + maxMatchLevel=ALL** 在本地无地域信息时等价 noop（空 zone 视为通配 + 全量降级），验证它不破坏前置链 cluster 语义。

---

## 2. 服务模型

### 2.1 服务

| 服务 | 角色 | 说明 |
| --- | --- | --- |
| `MixedRouteEchoClient` | consumer 入口，承担「流量入口」用于泳道染色 | 实例本身无 metadata |
| `MixedRouteEchoServer` | provider | 不同实例携带 lane × env 矩阵元数据 |

### 2.2 实例矩阵（5 个 provider）

| 实例 | 端口 | metadata |
| --- | --- | --- |
| `provider-base-dev`  | 28181 | `env=dev` |
| `provider-base-test` | 28182 | `env=test` |
| `provider-base-prod` | 28183 | `env=prod` |
| `provider-gray-dev`  | 28184 | `lane=gray, env=dev` |
| `provider-gray-test` | 28185 | `lane=gray, env=test` |

> 故意**不**注册 `lane=gray, env=prod` 实例，用来验证「lane 命中 gray + rule 命中 env=prod」时的两种 lane match_mode 行为。

### 2.3 consumer 二份（mode 区分 failoverType）

| consumer | 端口 | `ruleBasedRouter.failoverType` |
| --- | --- | --- |
| `consumer-all`  | 18180 | `all`（规则失败回退全量） |
| `consumer-none` | 18181 | `none`（规则失败返回空集 → 503） |

两个 consumer 共用 lane / nearby 配置。

---

## 3. Polaris 控制台规则配置

### 3.1 泳道组 `mixed-lane-group`

```yaml
name: mixed-lane-group
entries:
  - type: SERVICE
    namespace: default
    service: MixedRouteEchoClient
destinations:
  - service: MixedRouteEchoServer
rules:
  # 规则 A: 染色到 gray 泳道, STRICT
  - name: gray
    match_mode: STRICT
    default_label_value: gray
    enable: true
    traffic_match_rule:
      arguments:
        - {type: HEADER, key: user, value: {type: EXACT, value_type: TEXT, value: gray}}
      match_mode: AND

  # 规则 B: 染色到不存在的泳道, STRICT (期望 503)
  - name: strict-noexist
    match_mode: STRICT
    default_label_value: strict-noexist
    enable: true
    traffic_match_rule:
      arguments:
        - {type: HEADER, key: user, value: {type: EXACT, value_type: TEXT, value: strict}}

  # 规则 C: 染色到不存在的泳道, PERMISSIVE (期望回退基线)
  - name: permissive-noexist
    match_mode: PERMISSIVE
    default_label_value: noexist
    enable: true
    traffic_match_rule:
      arguments:
        - {type: HEADER, key: user, value: {type: EXACT, value_type: TEXT, value: permissive}}
```

### 3.2 规则路由 `mixed-rule-route`

为每个 env 生成一条 inbound 子规则：

```yaml
name: mixed-rule-route
namespace: default
routing_policy: RulePolicy
enable: true
rules:
  - name: env-dev
    sources:
      - {service: '*', namespace: '*', arguments: [{type: QUERY, key: env, value: {type: EXACT, value: dev, value_type: TEXT}}]}
    destinations:
      - {service: MixedRouteEchoServer, namespace: default, labels: {env: {type: EXACT, value: dev, value_type: TEXT}}}
  - name: env-test  # 同上, env=test → labels.env=test
  - name: env-prod  # 同上, env=prod → labels.env=prod
```

### 3.3 就近路由 `mixed-nearby-route`

```yaml
name: mixed-nearby-route
routing_policy: NearbyPolicy
enable: true
service: MixedRouteEchoServer
namespace: default
match_level: ZONE
max_match_level: ALL
strict_nearby: false
```

> **设计原因**：`matchLevel` 必须是 `region`/`zone`/`campus` 之一（合法约束）；本地测试环境实例无地域信息（region/zone 为空），nearby router 会按 `matchLocation` 把空字符串视为通配，等价不强制就近 + `maxMatchLevel=ALL` 兜底，效果近似 noop。

---

## 4. 用例矩阵

### 4.1 维度

| 维度 | 取值 |
| --- | --- |
| consumer | all / none |
| Header `user` | (none) / gray / strict / permissive |
| Query `?env=` | (none) / dev / test / prod / nomatch |

排列组合 = 2 × 4 × 5 = 40。脚本按「**有意义子集 + 关键边界**」抽取 16 个核心用例。

### 4.2 详细用例（共 16 个）

| # | consumer | Header user | Query env | 预期目标实例 | 预期 HTTP | 验证点 |
| --- | --- | --- | --- | --- | --- | --- |
|  1 | all  | (none) | dev      | `base-dev`              | 200 | baseline + rule 命中 |
|  2 | all  | (none) | test     | `base-test`             | 200 | baseline + rule 命中 |
|  3 | all  | (none) | prod     | `base-prod`             | 200 | baseline + rule 命中（prod 无 gray 实例） |
|  4 | all  | (none) | nomatch  | 任一 base 实例（非 gray）| 200 | baseline + rule failover=all 兜底 |
|  5 | all  | gray   | dev      | `gray-dev`              | 200 | lane STRICT 命中 + rule 命中 |
|  6 | all  | gray   | test     | `gray-test`             | 200 | lane STRICT 命中 + rule 命中 |
|  7 | all  | gray   | prod     | (空 → lane STRICT 无实例) | 503 | lane × rule 交集为空,STRICT 触发 503 |
|  8 | all  | strict | dev      | (空 → lane STRICT 无实例) | 503 | lane STRICT 指向不存在泳道 |
|  9 | all  | permissive | dev  | `base-dev`              | 200 | lane PERMISSIVE 回退基线 + rule 命中 |
| 10 | all  | permissive | test | `base-test`             | 200 | lane PERMISSIVE 回退基线 + rule 命中 |
| 11 | none | (none) | dev      | `base-dev`              | 200 | baseline + rule 命中 |
| 12 | none | (none) | nomatch  | (空 → rule failover=none) | 503 | rule 严格无匹配 |
| 13 | none | gray   | dev      | `gray-dev`              | 200 | lane × rule 都命中 |
| 14 | none | gray   | nomatch  | (空 → rule failover=none) | 503 | lane 命中 gray 但 rule 无匹配 |
| 15 | none | strict | dev      | (空 → lane STRICT 无实例) | 503 | lane 短路在前 |
| 16 | none | permissive | nomatch | (空 → rule none 无匹配) | 503 | lane 回退基线 + rule 严格失败 |

### 4.3 关键判定逻辑

```
请求 = (consumer, Header.user, Query.env)
   │
   ▼
lane router:
   user=gray        → 命中 gray, OutputCluster{lane:gray}
   user=strict      → 命中 strict-noexist (无实例), DegradeToFilterOnly + IgnoreFilterOnly=true, 空集 503
   user=permissive  → PERMISSIVE 无实例 → routeToBaseline (instanceFilter 排除带 lane key)
   user=(none)      → 无规则匹配 → routeToBaseline
   │
   ▼
ruleBasedRouter (除 user=strict 短路外):
   env=<dev|test|prod>   → 叠加 metadata env=val
   env=(none|nomatch)    → 规则失败:
                              consumer=all   → 兜底返回上一步 cluster
                              consumer=none  → 返回空集 503
   │
   ▼
nearbyBasedRouter (matchLevel=ALL): 等价 noop
   │
   ▼
filterOnlyRouter (afterChain): 健康度过滤
   │
   ▼
LB ChooseInstance → 实例 / 503
```

---

## 5. 测试脚本布局

`run.sh` 使用以下子命令：

| 子命令 | 行为 |
| --- | --- |
| `all` | 完整流程：build → 检查/创建规则 → 启动 → 等待就绪 → 跑用例 → 停止 |
| `build` | 仅编译 provider/consumer |
| `check` | 仅校验 Polaris 上的三条规则是否存在并启用 |
| `start` | 编译 + 启动服务 |
| `test` | 跑用例（要求服务已启动） |
| `stop` | 停止 provider/consumer |
| `cleanup` | 清理 .build / .logs |

环境变量：

- `POLARIS_HOST`：Polaris 服务端 IP（默认 127.0.0.1，HTTP 端口 8090，gRPC 8091）
- `POLARIS_TOKEN`：鉴权 token（可选）
- `DEBUG_MODE`：true/false 是否打开 SDK debug 日志
- `AUTO_CREATE_RULE`：true/false 是否自动创建缺失的 Polaris 规则（默认 true）

---

## 6. 与其他 demo 的区别

| Demo | 验证内容 | 互斥点 |
| --- | --- | --- |
| `lane/lane-test.sh` | 仅泳道路由 + baseLaneMode 行为 | 主链只配 ruleBasedRouter，不联动 |
| `rule/verify_rule_route.sh` | 仅规则路由 + failoverType | 不配泳道组 |
| `nearby/verify_nearby_route.sh` | 仅就近路由 | 不配规则路由 |
| `metadata/verify_metadata_route.sh` | 仅 dstMetaRouter | 不走 inbound 规则 |
| **`mixed/run.sh`**（本目录） | **三链共存按 AND 复合生效** | 自建 5 个 provider × 2 个 consumer 矩阵 |
