# 全链路灰度泳道测试方案

本文档汇总 `examples/route/lane/` 目录下两个端到端测试脚本的**测试方案（目标、前置条件、拓扑、用例、校验点）**，便于在 Polaris 服务端 + polaris-go SDK 环境下做路由/预热回归验证。

- 测试脚本：
  - [lane-test.sh](./lane-test.sh)：**泳道路由**功能测试（baseline / gray 隔离、流量匹配 STRICT/PERMISSIVE、baseLaneMode、泳道组剔除、非泳道组服务的 baseLaneMode、half 链路染色穿透 等）
  - [lane-warmup-test.sh](./lane-warmup-test.sh)：**泳道预热（Warmup）+ 百分比灰度（Percentage）**功能测试（按概率曲线灰度放量 + 按百分比切流）

两个脚本使用相同端口段（`48095 / 19080-19093` 等，新增的 NLG 用例占用 `19100-19111`，half 链路占用 `19120-19131`），**不可同时运行**，需先 `stop` 或 `./cleanup.sh` 后再切换。

---

## 一、通用信息

| 项 | 值 |
| --- | --- |
| 命名空间 | `default` |
| 入口网关服务 | 主脚本：`LaneRouterGateway`；warmup 脚本：`LaneRouterGatewayService` |
| Provider 服务 | `LaneEchoServer`；`baseLaneMode=1` 专项使用 `StableLaneEchoServer`；用例 6.2 专用 `NoLaneGroupEchoServer`（不在任何泳道组内）；用例 7.x 专用 `HalfLaneEchoServer` |
| Consumer 服务 | `LaneEchoClient` / `SimpleLaneEchoClient`；用例 6.2 专用 `NoLaneGroupEchoClient`（不在任何泳道组内）；用例 7.x 专用 `HalfLaneEchoClient`（仅 baseline 实例） |
| 染色 Header（直接染色） | `service-lane: <laneGroup>/<laneRule>`（如 `lane-go-example/gray`、`lane-go-warmup/gray`） |
| 流量匹配 Header | 主脚本：`user: gray` / `user: noexist` / `user: strict` / `user: other` / `user: half-permissive` / `user: half-strict`；六类维度规则守卫：`lane-test: <规则名>`；warmup 脚本：`warmup-user: gray` |
| 支持的命令 | `all` / `build` / `check` / `start` / `test` / `stop` / `-d`（debug） |
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
5. 流量匹配规则 `STRICT` 模式 + 目标泳道无实例 → 期望 HTTP 503（不降级基线）
6. 规则未命中 → 走 baseline
7. 并发下 baseline 与 gray 路径相互隔离
8. **六类匹配维度全覆盖**：`$method` / `Header` / `Query` / `Cookie` / `$path` / `$caller_ip`，验证 SDK `findTrafficValue` 7 类 SourceMatch 中的 6 类对外暴露维度（CALLER_METADATA 由 sourceService 元数据提供，已在染色路径覆盖）
9. **正向 + 反向双重验证**：每条 `service-lane` 直接染色 / 流量匹配 / 六类维度的正向用例 `x.Y` 都额外执行一个反向用例 `x.Yn`，构造不满足规则的请求，验证 SDK **不会**误染色（防止规则匹配过宽或 stain label 索引被旁路命中）
10. `baseLaneMode=ExcludeEnabledLaneInstance (=1)` 时 baseline 只走未启用泳道实例
11. 服务不在泳道组内（破坏性）→ 仅走无标签实例
12. **非泳道组服务的 `baseLaneMode` 行为**：服务从未加入任何泳道组时，consumer 端 `baseLaneMode=0/1` 对带标签实例的可见性差异
13. **half 链路（中间一跳无目标 lane 实例）**：验证 PERMISSIVE 模式下染色 stain label 能透传穿过没有 gray 实例的中间 consumer，最终命中下游 provider 的 gray 实例；STRICT 模式则在 consumer 一跳直接 503，不会错误降级或穿透

### 2.2 前置条件（Polaris 控制台配置）
- 泳道组：`lane-go-example`
- 入口服务：`LaneRouterGateway / default`
- 目标服务：`LaneEchoClient`、`SimpleLaneEchoClient`、`LaneEchoServer`、`StableLaneEchoServer`、`HalfLaneEchoClient`、`HalfLaneEchoServer`
  - `NoLaneGroupEchoServer` / `NoLaneGroupEchoClient` **不需要**出现在泳道组里（用例 6.2 验证“服务不在任何泳道组”这一场景）
- 规则 `gray`：Header `user=gray` → `lane=gray`，匹配模式 `STRICT`，`enable=true`
- 规则 `permissive`：Header `user=noexist` → `lane=noexist`（无实例），匹配模式 `PERMISSIVE`，`enable=true`
- 规则 `strict-noexist`：Header `user=strict` → `lane=strict-noexist`（无实例），STRICT，命中后 SDK 不降级、最终网关/消费端返回 HTTP 503
- **六类匹配维度规则（全部 STRICT，染到 `lane=gray`，复用 provider-gray 实例）**：
  - 规则 `method-post`：`METHOD=POST` AND `Header lane-test=method-post` → `lane=gray`
  - 规则 `query-env`：`QUERY env=gray` AND `Header lane-test=query-env` → `lane=gray`
  - 规则 `cookie-user`：`COOKIE user=gray` AND `Header lane-test=cookie-user` → `lane=gray`
  - 规则 `path-gray`：`PATH = /LaneEchoClient/gray-path`（独占路径，无需守卫） → `lane=gray`
  - 规则 `caller-ip-local`：`CALLER_IP EXACT 127.0.0.1` AND `Header lane-test=caller-ip-local` → `lane=gray`
  - 规则 `caller-ip-not-zero`：`CALLER_IP NOT_EQUALS 0.0.0.0` AND `Header lane-test=caller-ip-not-zero` → `lane=gray`
- **half 链路专用规则**（验证染色穿透 baseline 中间节点）：
  - 规则 `half-gray-permissive`：Header `user=half-permissive` → `lane=gray`，匹配模式 `PERMISSIVE`
  - 规则 `half-gray-strict`：Header `user=half-strict` → `lane=gray`，匹配模式 `STRICT`

> 上述 11 条规则（旧规则 3 条 + 六类维度 6 条 + half 链路 2 条）由脚本 `create_full_lane_group()` / `add_rule_to_lane_group()` 自动创建，亦可手工在控制台配置。除 `path-gray` 外，六类维度规则均叠加 `Header lane-test=<规则名>` 二级守卫，避免污染已有 baseline / permissive / strict-noexist / isolation 用例。

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
| `NoLaneGroupEchoServer`（用例 6.2，**不在**任何泳道组） | `:19100` | `:19101`（lane=gray） |
| `NoLaneGroupEchoClient`（用例 6.2，**不在**任何泳道组） | `:19110`（baseLaneMode=0） | `:19111`（baseLaneMode=1） |
| `HalfLaneEchoClient`（用例 7.x，**仅 baseline 实例**） | `:19120` | — |
| `HalfLaneEchoServer`（用例 7.x） | `:19130` | `:19131`（lane=gray） |

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
                │ NoLaneGroupEchoClient    │ :19110(mode0) / :19111(mode1)
                │ HalfLaneEchoClient       │ :19120 (仅 baseline)
                └─────────┬─────────────────┘
                          ▼
                ┌───────────────────────────┐
                │ LaneEchoServer            │ :19090(base) / :19091(gray)
                │ StableLaneEchoServer      │ :19092(stable) / :19093(gray)
                │ NoLaneGroupEchoServer     │ :19100(base) / :19101(lane=gray)
                │ HalfLaneEchoServer        │ :19130(base) / :19131(lane=gray)
                └───────────────────────────┘
```

四条被测主链路（编号与脚本 `log_title` 一致）：

| 链路编号 | 链路标签 | Gateway | Consumer | Provider | 用例数 |
| --- | --- | --- | --- | --- | --- |
| 1.x | 主链路 | `gateway` | `LaneEchoClient` | `LaneEchoServer` | **21**（6 基础 + 1 STRICT-503 + 6 维度 + 2 主反向 + 6 维度反向） |
| 2.x | `[simple]` | `simple-gateway` | `LaneEchoClient` | `LaneEchoServer` | **21**（6 基础 + 1 STRICT-503 + 6 维度 + 2 主反向 + 6 维度反向） |
| 3.x | `[gw→sc]` | `gateway` | `SimpleLaneEchoClient` | `LaneEchoServer` | **9**（6 基础 + 1 STRICT-503 + 2 主反向） |
| 4.x | `[sg→sc]` | `simple-gateway` | `SimpleLaneEchoClient` | `LaneEchoServer` | **9**（6 基础 + 1 STRICT-503 + 2 主反向） |

外加四个专项链路：
- **用例 5.1a/5.1b**：`gateway-excl` → `StableLaneEchoServer`（脚本把 `polaris.yaml` 中 `baseLaneMode: 0` 替换为 `baseLaneMode: 1`），验证 `baseLaneMode=ExcludeEnabledLaneInstance` 排除已启用泳道实例的语义
- **用例 6.1a/6.1b**：破坏性 —— 运行期调用 Polaris OpenAPI 把 `LaneEchoServer` 从泳道组 `destinations` 中移除，结束后自动恢复
- **用例 6.2a/6.2b**：`NoLaneGroupEchoClient` → `NoLaneGroupEchoServer`（两个服务均不在任何泳道组），验证 consumer 端 `baseLaneMode=0/1` 对带标签实例的可见性差异
- **用例 7.1 / 7.2 / 7.2d / 7.3**：half 链路 `gateway → HalfLaneEchoClient (仅 baseline) → HalfLaneEchoServer (baseline+gray)`，验证 PERMISSIVE 染色穿透 baseline 中间节点 / STRICT 一跳即 503 / 普通 header vs `service-lane` Header 双触发路径

### 2.4 用例矩阵

> **响应格式约定**：gateway/consumer/provider 在响应体中各贡献一行 `Hello, I'm <selfService>. lane=<myLane>, host=0.0.0.0:<port>, callee addr:<host>:<port>, callee lane=<lane>, callee resp=<callee's full body>`。校验时通常 `grep -E 'callee lane=(baseline)|:1909[0-9]|:1908[0-9]'` 即可定位关键字段。

> **用例编号约定**：
> - `x.Y`：正向用例，请求满足规则条件，期望路由到 gray 泳道
> - `x.Yn`：**反向用例**，构造不满足规则的请求，期望走 baseline，**用于校验 SDK 不会误染色**
> - `x.Yb`：边界用例（如 STRICT + 无实例 → HTTP 503）

#### 2.4.1 基础 6 用例（每条链路各执行一次，编号为 `<链路>.<序号>`，x ∈ {1, 2, 3, 4}）

**`x.1` 无 Header 基线**
- **请求**：`curl http://<gw>:<port>/<consumerSvc>/echo`，无任何染色/流量匹配 Header
- **测试逻辑**：期望 Polaris 服务端因找不到匹配的 `user` Header 规则走默认路径；gateway 的 `ProcessRouters` 拿不到染色标签与流量匹配结果，于是进入 `routeToBaseline` 分支，按各 consumer 的 `baseLaneMode`（默认 0）选无 lane 标签的实例
- **断言**：`grep -q "lane=(baseline)"` 必须命中（响应里有 `(baseline)` 标签）
- **预期响应片段**：
  ```
  Hello, I'm LaneRouterGateway. lane=(entry), host=0.0.0.0:48095, callee addr:127.0.0.1:19080, callee lane=(baseline), ...
  Hello, I'm LaneEchoClient. lane=(baseline), host=127.0.0.1:19080, callee addr:127.0.0.1:19090, callee lane=(baseline), ...
  Hello, I'm LaneEchoServer. lane=(baseline), host=127.0.0.1:19090
  ```

**`x.2` `service-lane` 直接染色**
- **请求**：`curl -H "service-lane: lane-go-example/gray" http://<gw>:<port>/<consumerSvc>/echo`
- **测试逻辑**：gateway 读取 `service-lane` Header，注入 `EnvironmentVariables["service-lane"]`；lane router `matchByStainLabel` 直接匹配到 `gray` 规则，把请求直接路由到 `lane=gray` 实例
- **断言 1**：`grep -q "lane=gray"`（响应链路里至少出现一次 gray 标签）
- **断言 2**：`grep -q ":${PROVIDER_GRAY_PORT}"`（`:19091` 必须出现在 provider callee 地址中，否则视为路由到 base）
- **预期响应片段**：
  ```
  Hello, I'm LaneRouterGateway. lane=(entry), host=0.0.0.0:48095, callee addr:127.0.0.1:19081, callee lane=gray, ...
  Hello, I'm LaneEchoClient. lane=gray, host=127.0.0.1:19081, callee addr:127.0.0.1:19091, callee lane=gray, ...
  Hello, I'm LaneEchoServer. lane=gray, host=127.0.0.1:19091
  ```

**`x.2n` `service-lane` 直接染色反向**（反向用例）
- **请求**：`curl -H "service-lane: lane-go-example/nonexist-rule" http://<gw>:<port>/<consumerSvc>/echo`
- **测试逻辑**：构造一个不存在的规则名 stain label。lane router 的 `matchByStainLabel` 在 `stainLabelIndex` 中找不到对应规则，按规范应回退基线，不应被错误路由到任意已存在规则
- **断言**：`grep -q "lane=(baseline)"`（不应出现 `lane=gray`）
- **失败模式**：若响应出现 `lane=gray`，说明 `matchByStainLabel` 实现存在问题（如把短格式标签错误地按 `defaultLabelValue` 模糊匹配）

**`x.3` 流量匹配 STRICT（`user: gray`）**
- **请求**：`curl -H "user: gray" http://<gw>:<port>/<consumerSvc>/echo`
- **测试逻辑**：gateway 把 `user: gray` Header 转成 `model.BuildHeaderArgument("user", "gray")` 注入到 `ProcessRouters.AddArguments`；lane router `matchByRouteInfo` 命中 `gray` 规则（STRICT 模式），把消费端和服务端都路由到 `lane=gray`
- **断言**：`grep -q "lane=gray"`（只断言链路中至少一次出现 `lane=gray`，不强制端口，让上游容错）
- **预期响应片段**：同 `x.2`，但少了 `service-lane` Header

**`x.3n` 流量匹配 STRICT 反向（`user: graymismatch`）**（反向用例）
- **请求**：`curl -H "user: graymismatch" http://<gw>:<port>/<consumerSvc>/echo`
- **测试逻辑**：构造一个与目标值仅一字符之差的 Header 值。`gray` 规则使用 `EXACT` 匹配，应当严格判等 —— 任何近似值都不应命中
- **断言**：`grep -q "lane=(baseline)"`（`x.3n` 必须严格走 baseline）
- **设计意图**：反向校验 EXACT 语义，防止 SDK 误把 `EXACT` 实现成 `CONTAINS` 或 `STARTS_WITH` 等宽松匹配

**`x.4` 流量匹配 PERMISSIVE（`user: noexist`）**
- **请求**：`curl -H "user: noexist" http://<gw>:<port>/<consumerSvc>/echo`
- **测试逻辑**：规则 `permissive`（`default_label_value=noexist`、PERMISSIVE 模式）命中 Header 匹配，但 `lane=noexist` 在 Polaris 中**没有实例**；PERMISSIVE 模式下 SDK 把泳道路由降级为基线路由（与 `routeToBaseline` 等价）
- **断言**：`grep -q "lane=(baseline)"`（不应出现 `lane=noexist`，也不应出现 `lane=gray`）
- **预期响应片段**：同 `x.1`（全链路 baseline）

**`x.4b` 流量匹配 STRICT + 无实例（`user: strict`）**
- **请求**：`curl -i -H "user: strict" http://<gw>:<port>/<consumerSvc>/echo`
- **测试逻辑**：规则 `strict-noexist`（STRICT 模式）命中 Header 匹配，但 `lane=strict-noexist` 没有实例；STRICT 模式下 SDK **不**降级基线，而是把状态置为 `DegradeToFilterOnly` + `HasLimitedInstances=true`，最终在 `ProcessLoadBalance` 抛 `ErrCodeAPIInstanceNotFound`；gateway/consumer 捕获该错误后返回 `HTTP 503`
- **断言 1**：`http_code == "503"`（从临时文件 `tmp_body` 读出）
- **断言 2**：`body` **不**包含 `lane=gray` 和 `lane=(baseline)`（防止 SDK 错误降级到 baseline）
- **预期响应**：`HTTP/1.1 503 Service Unavailable`，body 类似 `no instance available: ...`

**`x.5` 未命中规则（`user: other`）**
- **请求**：`curl -H "user: other" http://<gw>:<port>/<consumerSvc>/echo`
- **测试逻辑**：无任何规则的 Header 匹配上，SDK 直接走 baseline
- **断言**：`grep -q "lane=(baseline)"`（必须 baseline，不应出现 gray）
- **预期响应片段**：同 `x.1`

**`x.6` 泳道隔离并发**
- **测试逻辑**：`rounds=5` 轮循环，每轮连续发两个请求：① 无 Header（应 baseline）、② `user: gray`（应 gray）。逐轮检查响应分别落入 `lane=(baseline)` 与 `lane=gray`，任一交叉即 `baseline_ok=false` 或 `gray_ok=false`
- **断言**：`baseline_ok && gray_ok` 必须同时为 true（5 轮 × 2 类 = 10 次请求零交叉）
- **请求模板**（脚本内 `seq + curl`）：
  ```bash
  for round in 1..5:
    resp_base=$(curl http://<gw>:<port>/<consumerSvc>/echo)
    resp_gray=$(curl -H "user: gray" http://<gw>:<port>/<consumerSvc>/echo)
  ```

> **小节合计**：四条链路每条 6 基础用例 + 1 STRICT-503（`x.4b`）+ 2 反向用例（`x.2n`/`x.3n`），共 **36 条**（4 链路 × 9）。

#### 2.4.2 六类匹配维度用例（仅 1.x / 2.x 两条入口链路）

**目的**：验证 gateway / simple-gateway 把 HTTP 请求中的 `$method / Header / Query / Cookie / $path / $caller_ip` 六类输入构造为 `model.Argument` 上报给 SDK（`buildRouteArguments`），并被 `TrafficMatchRule` 正确识别。`Header` 维度已由基础用例 `x.3 / x.4 / x.5` 覆盖。

每条**正向**用例的统一断言：`grep -q "lane=gray"`（链路中至少出现一次 gray 标签）。每条**反向**用例的统一断言：`grep -q "lane=(baseline)"`（不应被误染色）。**`x.10` 例外**：不校验 HTTP 状态码（consumer 没有 `/gray-path` handler，会返回 404，但 gateway 仍把 `callee lane=gray` 拼到响应 msg 里）。

**`x.7` `$method`（METHOD=POST）**
- **正向请求**：`curl -X POST -H "lane-test: method-post" http://<gw>:<port>/<consumerSvc>/echo`
- **测试逻辑**：规则 `method-post` 是 `METHOD=POST AND Header lane-test=method-post → lane=gray`。gateway 把 `r.Method="POST"` 转成 `BuildMethodArgument("POST")`，规则中 `METHOD` 类型的 Argument 才能命中

**`x.7n` `$method` 反向（METHOD=GET）**
- **反向请求**：`curl -H "lane-test: method-post" http://<gw>:<port>/<consumerSvc>/echo`（仅去掉 `-X POST`）
- **测试逻辑**：保留守卫 Header，但把 method 改成 GET，主条件 `METHOD=POST` 不满足，期望走 baseline

**`x.8` `Query`（`?env=gray`）**
- **正向请求**：`curl -H "lane-test: query-env" "http://<gw>:<port>/<consumerSvc>/echo?env=gray"`
- **测试逻辑**：规则 `query-env` 是 `QUERY env=gray AND Header lane-test=query-env → lane=gray`。`r.URL.Query()` 解析得到 `env=gray`，转成 `BuildQueryArgument("env", "gray")`

**`x.8n` `Query` 反向（`?env=stable`）**
- **反向请求**：`curl -H "lane-test: query-env" "http://<gw>:<port>/<consumerSvc>/echo?env=stable"`
- **测试逻辑**：query 值变更为 `env=stable`（与目标 EXACT 不等），期望走 baseline

**`x.9` `Cookie`（`user=gray`）**
- **正向请求**：`curl -H "lane-test: cookie-user" -H "Cookie: user=gray" http://<gw>:<port>/<consumerSvc>/echo`
- **测试逻辑**：规则 `cookie-user` 是 `COOKIE user=gray AND Header lane-test=cookie-user → lane=gray`。`r.Cookies()` 解析 `Cookie: user=gray`，转成 `BuildCookieArgument("user", "gray")`

**`x.9n` `Cookie` 反向（`user=stable`）**
- **反向请求**：`curl -H "lane-test: cookie-user" -H "Cookie: user=stable" http://<gw>:<port>/<consumerSvc>/echo`
- **测试逻辑**：cookie 值变更为 `user=stable`，期望走 baseline

**`x.10` `$path`（`/LaneEchoClient/gray-path`）**
- **正向请求**：`curl http://<gw>:<port>/<consumerSvc>/gray-path`
- **测试逻辑**：规则 `path-gray` 是 `PATH = /LaneEchoClient/gray-path → lane=gray`，**无 Header 守卫**（独占路径天然隔离）。`buildRouteArguments` 把完整 `r.URL.Path` 转成 `BuildPathArgument("/LaneEchoClient/gray-path")`。consumer 收到后没有 `/gray-path` handler 返回 404，但 gateway 响应 msg 仍带 `callee lane=gray`
- **常见陷阱**：若用 `curl -i` 看到的 HTTP 是 404，是预期；测试脚本的 `test_path_gray_match` 用 `grep` 校验 body，不读 status code

**`x.10n` `$path` 反向（默认 `/echo` 路径）**
- **反向请求**：`curl http://<gw>:<port>/<consumerSvc>/echo`
- **测试逻辑**：路径变更为默认 `/echo`，不在 `path-gray` 规则的 EXACT 匹配范围内，期望走 baseline

**`x.11` `$caller_ip`（EXACT 127.0.0.1）**
- **正向请求**：`curl -H "lane-test: caller-ip-local" http://127.0.0.1:<gw>/<consumerSvc>/echo`
- **测试逻辑**：规则 `caller-ip-local` 是 `CALLER_IP EXACT 127.0.0.1 AND Header lane-test=caller-ip-local → lane=gray`。`net.SplitHostPort(r.RemoteAddr)` 取出 host，本机访问得到 `127.0.0.1`，转成 `BuildCallerIPArgument("127.0.0.1")`
- **常见陷阱**：若不是本地访问（如通过远端 Polaris 部署），`RemoteAddr` 不是 `127.0.0.1`，规则不会命中；测试必须从本机 `127.0.0.1` 发起

**`x.11n` `$caller_ip` 反向（缺守卫 Header）**
- **反向请求**：`curl http://127.0.0.1:<gw>/<consumerSvc>/echo`（仅去掉 `lane-test` 守卫 Header）
- **测试逻辑**：CALLER_IP 主条件仍命中，但 AND 链上的守卫 Header 缺失，整条规则不应命中。期望走 baseline。**反向校验 AND 复合条件的严格性**

**`x.12` `$caller_ip`（NOT_EQUALS 0.0.0.0）**
- **正向请求**：`curl -H "lane-test: caller-ip-not-zero" http://127.0.0.1:<gw>/<consumerSvc>/echo`
- **测试逻辑**：规则 `caller-ip-not-zero` 是 `CALLER_IP NOT_EQUALS 0.0.0.0 AND Header lane-test=caller-ip-not-zero → lane=gray`。`NOT_EQUALS` 几乎匹配所有 IP，**必须**有 Header 守卫才能避免误染
- **设计要点**：与 `caller-ip-local` 验证同一 SourceMatch 类型（`CALLER_IP`）的不同 `value_type`（`EXACT` vs `NOT_EQUALS`）

**`x.12n` `$caller_ip` 反向（守卫 Header 取错值）**
- **反向请求**：`curl -H "lane-test: mismatch" http://127.0.0.1:<gw>/<consumerSvc>/echo`
- **测试逻辑**：守卫 Header 取一个错误值（不等于 `caller-ip-not-zero`），AND 链不命中。期望走 baseline。`NOT_EQUALS` 主条件无法独立绕开（任何本机 IP 都 ≠ 0.0.0.0），所以从守卫入手反向

> **小节合计**：六类维度正向 12 条 + 反向 12 条 = **24 条**（仅 1.x / 2.x 两条入口链路，每链路 6 + 6 = 12）。

#### 2.4.3 专项用例

**`5.1a` `baseLaneMode=ExcludeEnabledLaneInstance`（无染色，10 次采样）**
- **链路**：`gateway-excl (:48097)` → `StableLaneEchoServer`（无未打标签实例，只有 `lane=stable :19092` 和 `lane=gray :19093`）。`gateway-excl` 的 `polaris.yaml` 通过 `sed 's/baseLaneMode: 0/baseLaneMode: 1/'` 生成 `baseLaneMode=1`
- **测试逻辑**：`StableLaneEchoServer` 不在任何泳道组的 `destinations` 之外，且**没有任何无 lane 标签实例**。`routeToBaseline` 走第二步：构造已启用规则 defaultLabelValue 集合 `{gray, noexist, strict-noexist, ...}`，从实例里排除 `lane=gray`，剩下 `lane=stable`。10 次请求应 100% 落到 `lane=stable :19092`
- **请求**：`for i in 1..10: curl http://127.0.0.1:48097/StableLaneEchoServer/echo`（无 Header）
- **断言**：统计 10 次响应中
  - 落到 `:19092`（stable）的次数 `stable_count == 10`
  - 落到 `:19093`（gray）的次数 `gray_count == 0`
  - 若两者均 > 0 → FAIL（出现负载均衡，违反 ExcludeEnabledLaneInstance 语义）

**`5.1b` `baseLaneMode=ExcludeEnabledLaneInstance`（染色，1 次）**
- **链路**：同 5.1a
- **测试逻辑**：染色路径走 `routeToLane`，不进入 `routeToBaseline`，`baseLaneMode` 不影响。`user: gray` 命中规则 `gray`，应路由到 `lane=gray :19093`
- **请求**：`curl -H "user: gray" http://127.0.0.1:48097/StableLaneEchoServer/echo`
- **断言 1**：`grep -q ":19093"`（命中 gray 实例）
- **断言 2**：若 body 含 `LaneEchoServer` 但端口不是 19093 → FAIL

**`6.1` 服务不在泳道组内（破坏性，含三个 phase）**

整体设计：测试 `LaneEchoServer` 不在泳道组 `destinations` 时，gateway 默认模式（`baseLaneMode=0`）如何处理染色请求与无 Header 请求。脚本自动移除 `LaneEchoServer` 改泳道组，结束后自动恢复。

- **Phase 1：管理 API 确认移除**
  - `remove_provider_from_lane_group()` 通过 `PUT /naming/v1/lane/groups` 把 `destinations` 里的 `LaneEchoServer` 去掉
  - 校验：`GET /naming/v1/lane/groups?name=lane-go-example&...` 解析后，`LaneEchoServer` 必须在 `destinations` **之外**
  - 失败路径：管理 API 错误 → `test_fail 6.1 / 6.1a / 6.1b` 三条同时记为级联失败

- **Phase 1.5：Discover API 缓存传播**
  - 关键判断：**管理 API 与 Discover API 是不同数据面**，前者改完就生效，后者要等服务端 naming cache 刷新才下发到 SDK
  - 最多 `max_discover_wait=120s` 轮询 `POST /v1/Discover`（type=LANE），直到返回的 `destinations` 不含 `LaneEchoServer`
  - **超时 → SKIP 6.1**（标 PASS 但语义为 SKIP），不进入 6.1a/6.1b；同时 `_restore_lane_group` 恢复泳道组
  - 这是测试**服务端**缓存传播延迟，**不是** SDK bug

- **Phase 2：SDK 行为探测**
  - 关键判断：1.5 已确认 Discover API 下发了新规则。SDK 默认 refreshInterval=2s，此时应很快拉到新规则
  - 30s 内每 5s 发一次 `-H "user: gray" http://127.0.0.1:48095/LaneEchoClient/echo`，探测是否路由到 `:19090`（base）而非 `:19091`（gray）
  - **30s 内未生效 → FAIL 6.1**：这是 SDK 端 bug（疑似 `matchByStainLabel` 短格式歧义或 `LaneGroup` 跨组合并错误）
  - 同时记 6.1a/6.1b 为级联失败

- **`6.1a` 染色请求验证**（10 次）
  - **请求**：`for i in 1..10: curl -H "user: gray" http://127.0.0.1:48095/LaneEchoClient/echo`
  - **断言**：`base_count == 10 && gray_count == 0`（必须 10 次全部落到 `:19090`）
  - 若同时出现 `base_count > 0 && gray_count > 0` → FAIL（默认模式下不应命中带标签实例）

- **`6.1b` 无 Header 请求验证**（1 次）
  - **请求**：`curl http://127.0.0.1:48095/LaneEchoClient/echo`
  - **断言**：`grep -q "lane=(baseline)"` **且** `grep -q ":19090"`
  - 失败原因：路由到 gray 实例，或落到非 `:19090` 端口

- **收尾**：`_restore_lane_group()` 把 `LaneEchoServer` 加回 `destinations`，无论用例 PASS/FAIL/SKIP 都执行，避免脏状态污染后续 run

**`6.2a` 非泳道组服务 + `baseLaneMode=0`（10 次）**
- **链路**：`NoLaneGroupEchoClient (:19110, baseLaneMode=0)` → `NoLaneGroupEchoServer`（base `:19100`、gray `:19101`）。两个服务均**不在任何泳道组内**
- **测试逻辑**：`baseLaneMode=0`（`OnlyUntaggedInstance`，默认）下 `routeToBaseline` 第一步直接选「无 lane 元数据 key」的实例，过滤掉 `lane=gray :19101`
- **请求**：`for i in 1..10: curl http://127.0.0.1:19110/echo`（直连 consumer，不走 gateway）
- **断言**：`base_count == 10 && gray_count == 0`（必须 10 次全到 `:19100`）
- **失败模式**：若 `gray_count > 0` → FAIL（说明 SDK 把 `lane=gray` 实例当成了 baseline）

**`6.2b` 非泳道组服务 + `baseLaneMode=1`（10 次）**
- **链路**：`NoLaneGroupEchoClient (:19111, baseLaneMode=1)` → `NoLaneGroupEchoServer`（同上）
- **测试逻辑**：`baseLaneMode=1`（`ExcludeEnabledLaneInstance`）下 `routeToBaseline` 第一步找不到无标签实例后，第二步构建已启用规则 defaultLabelValue 集合，**排除** `gray` 标签实例。`NoLaneGroupEchoServer` 不在任何泳道组，所以「已启用规则 defaultLabelValue 集合」**不包含** `gray`（来自 `lane-go-example` 组的 `gray` 规则），但 baseLaneMode=1 的本意是排除**已启用**的标签值，此处集合为空则不排除任何实例
  - 预期行为：10 次请求中至少有 1 次路由到 `:19101`（gray），证明 mode=1 允许命中带标签实例
- **请求**：`for i in 1..10: curl http://127.0.0.1:19111/echo`
- **断言**：`gray_count2 > 0`（至少 1 次命中 gray 实例）
- **失败模式**：若 10 次全到 `:19100` → FAIL（说明 SDK 没走第二步的扩展规则；或 consumer 的 `polaris.yaml` 仍是 `baseLaneMode: 0`，检查 `${nlg_consumer_mode1_workdir}/polaris.yaml`）

> 用例 6.1 结束时会把 `LaneEchoServer` 重新加回泳道组（兼容别名写回），避免影响后续运行。
> 用例 6.2 不修改任何 Polaris 配置，consumer / provider 的 `polaris.yaml` 通过 `sed` 切换 `baseLaneMode`，互不污染。

#### 2.4.4 half 链路用例（用例 7.x）

**链路特点**：
- `HalfLaneEchoClient` 只注册 baseline 实例（`:19120`，**没有 lane=gray 实例**）
- `HalfLaneEchoServer` 同时有 baseline（`:19130`）+ lane=gray（`:19131`）两台实例
- 这是常见的"M 字型"灰度链路：中间一跳没有灰度实例，但下游 provider 已经做了灰度，染色请求需要能"穿过"中间节点

**测试目的**：
1. **PERMISSIVE 染色穿透（用例 7.2 / 7.2d）**：中间 consumer 没有 lane=gray 实例时，lane router 回退基线但**透传 stainLabel**，下游 provider 仍能命中 gray 实例
2. **STRICT 一跳即 503（用例 7.3）**：STRICT 不应因为下游 provider 有 gray 实例而被错误降级或穿透 —— consumer 一跳就该 503
3. **基线流量不受影响（用例 7.1）**：half 规则只对带相应 Header 的流量生效

**`7.1` 无 Header 基线**
- **请求**：`curl http://127.0.0.1:48095/HalfLaneEchoClient/echo`
- **测试逻辑**：无染色 / 流量匹配 Header，整个链路按 baseline 路由
- **断言 1**：`grep -q ":19130"`（half-provider 命中 baseline 实例）
- **断言 2**：`grep -q "lane=(baseline)"`
- **失败模式**：若响应出现 `:19131` → FAIL（无 Header 流量被错误染色）

**`7.2` PERMISSIVE 染色穿透（普通 header 触发）**
- **请求**：`curl -H "user: half-permissive" http://127.0.0.1:48095/HalfLaneEchoClient/echo`
- **测试逻辑**：
  - gateway 通过 `TrafficMatchRule` 识别 `user: half-permissive` → 命中 `half-gray-permissive` 规则（PERMISSIVE）→ 染色 `service-lane: lane-go-example/half-gray-permissive`
  - half-consumer 收到请求，自身没有 `lane=gray` 实例。PERMISSIVE 模式下 lane router 回退基线（half-consumer 自己只有 baseline 实例，命中 baseline）但**继续透传 service-lane Header** 给下游
  - half-provider 收到带 stainLabel 的请求，命中 `lane=gray` 实例（`:19131`）
- **断言 1**：`grep -q ":19131"`（half-provider 命中 lane=gray 实例）
- **断言 2**：`grep -q "HalfLaneEchoClient. lane=(baseline)"`（half-consumer 自己是 baseline）
- **失败模式**：
  - half-provider 命中 `:19130`（baseline）→ FAIL（染色未穿透）
  - 中间任意节点出错 → FAIL

**`7.2d` PERMISSIVE 染色穿透（service-lane 直染对照）**
- **请求**：`curl -H "service-lane: lane-go-example/half-gray-permissive" http://127.0.0.1:48095/HalfLaneEchoClient/echo`
- **测试逻辑**：与 7.2 形成对照。直接构造 `service-lane` stain label，**跳过 TrafficMatchRule 流量识别**，直接命中 lane router 的 `stainLabelIndex`。两条路径最终应走出**完全一样的链路结果**
- **断言**：与 7.2 完全相同（half-provider 命中 `:19131` + half-consumer baseline）
- **设计意图**：
  - 验证 SDK 的两条匹配路径（`stainLabelIndex` 直染 / `TrafficMatchRule` 流量识别）在 half 链路场景下行为一致
  - 若仅 7.2 通过而 7.2d 失败，说明 stain label 索引在跨节点透传时存在问题

**`7.3` STRICT 一跳即 503**
- **请求**：`curl -i -H "user: half-strict" http://127.0.0.1:48095/HalfLaneEchoClient/echo`
- **测试逻辑**：
  - gateway 命中 `half-gray-strict` 规则（STRICT）→ 染色 `service-lane: lane-go-example/half-gray-strict`
  - half-consumer 收到请求，自身没有 `lane=gray` 实例。STRICT 模式下 lane router 直接置空 cluster + `DegradeToFilterOnly`，最终 `ProcessLoadBalance` 抛 `ErrCodeAPIInstanceNotFound` → consumer 返回 HTTP 503
  - **请求根本走不到 half-provider**
- **断言 1**：`http_code == "503"`
- **断言 2**：body **不**包含 `:19131`（确认未穿透到 provider）
- **失败模式**：
  - 出现 `:19131` → FAIL（STRICT 被错误穿透到下游）
  - 任意 200 → FAIL（STRICT 被错误降级到 baseline）

> **设计要点**：7.2 vs 7.3 形成 PERMISSIVE / STRICT 行为差异的关键对照 —— 同样是"中间一跳无目标 lane 实例"，PERMISSIVE 透传染色到下游，STRICT 直接 503。这是 SDK 的强契约，不能颠倒。
>
> **小节合计**：4 条用例（7.1 / 7.2 / 7.2d / 7.3）。

### 2.5 执行与结果

- **日志**：
  - 主日志：`./.logs/lane-test-<时间戳>.log`（含 `[INFO] [PASS] [FAIL] [WARN]` 分级输出）
  - 服务日志：`./.logs/<role>.log`（如 `gateway.log`、`provider-base.log`、`consumer-gray.log`、`nlg-consumer-mode1.log`、`half-consumer.log`、`half-provider-gray.log` 等）
  - Polaris SDK 日志：每个进程工作目录下的 `polaris/` 子目录（由 `polaris.yaml` 的 `logDir` 控制）
- **执行命令**：
  - `./lane-test.sh all 127.0.0.1`：完整 build → check → start → wait → test → stop
  - `./lane-test.sh start 127.0.0.1`：仅 build + check + start + wait（服务保持运行）
  - `./lane-test.sh test`：服务已启动的前提下跑全部用例（避免重复启动）
  - `./lane-test.sh stop`：通过 `.lane-test-pids` 文件逐个 SIGTERM，等待 10s 触发 Polaris 反注册
  - `./lane-test.sh check 127.0.0.1`：仅校验泳道规则配置（dry-run）
- **用例执行模式**：
  - 顺序执行：`run_all_tests` 按主链路 → simple 链路 → gw→sc 链路 → sg→sc 链路 → baseLaneMode=1 专项 → half 链路 → 6.1 破坏性 → 6.2 非泳道组专项 的顺序逐组跑
  - 每条链路内部，正向用例与反向用例交错执行：`x.1 → x.2 → x.2n → x.3 → x.3n → x.4 → x.4b → x.5 → x.6 → x.7 → x.7n → ...`
  - 预热请求：`run_all_tests` 开头会循环 10 次发 `-H "service-lane: lane-go-example/gray"` 直到 `grep -q "lane=gray"` 成功，提示泳道规则已加载到 SDK 缓存
  - 6.1 必须最后跑：会改泳道组 `destinations`，提前跑会污染其他用例
- **结果汇总**：
  - 脚本末尾输出 `TOTAL_PASS / TOTAL_FAIL / TOTAL_COUNT`
  - 任一 `fail` 即整体失败，退出码非 0（CI 友好）
  - 用例数合计（PASS/FAIL 计数口径与 `test_pass` / `test_fail` 一致）：
    - 链路 1.x = **21**（6 基础 + 1 STRICT-503 + 6 维度 + 2 主反向 + 6 维度反向）
    - 链路 2.x = **21**（结构同 1.x）
    - 链路 3.x = **9**（6 基础 + 1 STRICT-503 + 2 主反向）
    - 链路 4.x = **9**（结构同 3.x）
    - 专项 5.1 = 2、6.2 = 2、7.x = 4
    - 6.1 计数随分支变化：正常路径 2（6.1a + 6.1b）、Discover API SKIP 路径 1（仅 6.1 自身）、级联失败路径 3（6.1 + 6.1a + 6.1b 全 FAIL）
    - 合计 **70 条**（正常成功）/ **69 条**（6.1 SKIP）/ **71 条**（6.1 级联失败）
- **单独跑某条用例**：
  - 脚本没有提供 `test <case>` 子命令。如需单独跑某条用例，常见做法：
    1. 启动服务：`./lane-test.sh start 127.0.0.1`
    2. 手动执行对应 curl（参见上文各用例的「请求」段）
    3. 停止服务：`./lane-test.sh stop`
  - 调试时常用：在 `test_out_of_lane_group` 失败后立即 `curl -H "user: gray" http://127.0.0.1:48095/LaneEchoClient/echo`，查看 gateway.log / consumer-base.log 实时路由决策

---

## 三、`lane-warmup-test.sh` —— 泳道预热 + 百分比灰度测试方案

### 3.1 测试目标
1. 验证 `laneRouter` 的 `WARMUP` 染色类型是否按以下公式做概率放量（用例 1-5）：

```
probability = pow(uptime / warmup_interval, curvature) * 100%
uptime = now - etime        // etime 为规则最近一次启用时间
当 uptime >= interval 时 probability = 100%
```

2. 验证 `laneRouter` 的 `PERCENTAGE` 百分比染色类型（用例 6-9），覆盖 `plugin/servicerouter/lane/lane_router.go:tryStainByPercentage` 的有效分支：
   - `percent` 极小值（=1） → 期望 `gray% ≈ 1%`，验证概率抽样在低概率端的正确性；
   - `percent` 中段（=50） → 期望 `gray% ≈ 50%`，验证常规概率抽样；
   - `percent` 满值（=100）→ 完全染色（命中 `percent >= 100 → return true` 分支）；
   - 以及基线请求在 PERCENTAGE 模式下不受染色概率影响。

> **不验证 `percent=0`**：specification/lane.proto 中 `Percentage.percent` 是 proto3 裸 `int32 (json:"omitempty")`，零值在 JSON 序列化时被 omit，Polaris 服务端会以 `400103` 拒绝该请求；SDK 端 `percent <= 0 → return false` 这条分支因此外部不可达。

通过统计响应中 `lane=gray` 的比例，验证两种染色类型各自的行为正确性。

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
| 用例 6 PERCENTAGE=1 | 通过管理 API 把 gray 规则切换为 `PERCENTAGE(percent=1)`，发送 300 个染色请求 | 实际 `gray%` ≤ 6%（N=300, p=0.01 的均值=3，3σ ≈ 5.2，留 6 作上限） | — | 管理 API 无权限或服务端拒绝（如 4xx） | `gray%` > 6% |
| 用例 7 PERCENTAGE=50 | 切换为 `PERCENTAGE(percent=50)`，发送 200 个染色请求 | 实际 `gray%` ∈ `[35%, 65%]`（N=200, p=0.5 的 ±3σ ≈ ±10%，留 ±15% 容忍） | — | 管理 API 无权限 | `gray%` 超出 `[35%, 65%]` 区间 |
| 用例 8 PERCENTAGE=100 | 切换为 `PERCENTAGE(percent=100)`，发送 50 个染色请求 | 实际 `gray%` ≥ 95%（理想）；≥ 85% 视为基本符合 | — | 管理 API 无权限 | `gray%` < 85% |
| 用例 9 PERCENTAGE 模式基线零泄漏 | 保持 `PERCENTAGE(percent=100)`，发送 50 个**无染色 Header** 的基线请求 | `gray == 0`：染色概率只影响 matched 流量，不影响 unmatched 流量 | — | 管理 API 无权限 | 任一无染色请求落到 gray |

> **用例 6-9 组收尾**：`run_all_tests` 在 4 个 PERCENTAGE 用例全部跑完后（或被 Ctrl-C 打断时通过 trap）自动调用 `restore_gray_rule_to_warmup`，把 gray 规则的 `traffic_gray` 还原为 WARMUP，使用 `validate_lane_rules` 从 Polaris 读取到的 `WARMUP_INTERVAL / WARMUP_CURVATURE`。
>
> **关于 percent=0 不可测**：`_set_gray_rule_traffic_gray` 在收到 4xx/5xx 错误码时会显式 `return 1`，让上层用例 FAIL 而不是静默继续；这是初次跑测发现 percent=0 被服务端拒绝（返回 `400103`）后加入的防御。

### 3.5 辅助能力
- `get_gray_etime()`：从 Polaris OpenAPI 读取 `gray` 规则的 `etime`，用于计算 `uptime`
- `validate_rules_with_wait(rule_name)`：`disable → enable` 规则，并轮询等待 SDK 侧生效（避免缓存滞后）
- `calc_warmup_probability(uptime, interval, curvature)`：Python 实现预热概率公式，作为实际比例的理论基线
- `set_gray_rule_percentage(percent)` / `restore_gray_rule_to_warmup()`：用例 6-9 专用。通过 `PUT /naming/v1/lane/groups` 将 gray 规则的 `traffic_gray` 在 `PERCENTAGE` / `WARMUP` 两种模式间切换；切换后 sleep 3s 给 SDK 拉缓存
- 当 `WARMUP_INTERVAL > MAX_WARMUP_WAIT` 时，脚本会在日志中提示「建议将 `warmup_interval` 设为 ≤`MAX_WARMUP_WAIT`s 以缩短测试耗时」

### 3.6 执行与结果
- 日志：`./.logs/lane-warmup-test-<时间戳>.log`
- 汇总：统一输出 `TOTAL_PASS / TOTAL_FAIL / TOTAL_SKIP / TOTAL_COUNT`；SKIP 不计入 FAIL，便于 CI 中区分「环境约束」与「功能缺陷」

---

## 四、常见问题排查

| 症状 | 可能原因 | 处置 |
| --- | --- | --- |
| `wait_for_services` 超时（当前上限 120s） | Polaris 尚未把 consumer / provider 注册信息推给 gateway；或端口被占用 | 查看 `./.logs/gateway.log`、`consumer.log`、`provider.log`；或先 `./cleanup.sh` |
| `ErrCodeAPIInstanceNotFound: LaneEchoClient` | Provider/Consumer 进程未正常注册 | 确认三个 `main.go` 已构建并启动，确认 `polaris.yaml` 指向正确的 Polaris 地址 |
| 规则校验失败（`gray 规则未启用` / Header Key 不符） | Polaris 控制台未按前置条件配置 | 按 2.2 / 3.2 节重新配置规则并 `enable` |
| 两个脚本同时运行冲突 | 共用 `48095 / 19080~19093` 端口（NLG 用例还会占用 `19100~19111`，half 链路占用 `19120~19131`） | 使用前先运行对方的 `stop`，或执行 `./cleanup.sh` |
| 用例 5.1 专项失败 | `gateway-excl/polaris.yaml` 未替换为 `baseLaneMode: 1` | 检查脚本生成的 `${gateway_excl_workdir}/polaris.yaml` |
| 用例 6.1 标记为 SKIP | 管理 API 已确认移除，但 **Discover API** 在 120s 内仍未下发新规则 → 服务端 naming cache 刷新延迟 | 属于服务端问题；可调小 Polaris 的 naming cache 刷新间隔或手动重跑 |
| 用例 6.1 SDK 在 30s 内未感知到变更 | 已被 1.5 排除服务端问题；可能是 SDK 泳道路由存在 bug（如 matchByStainLabel 短格式歧义） | 排查 `consumer-base.log` 中 `[Router][Lane]` 关键字；确认其它泳道组（如 `lane-go-warmup`）未与本规则产生冲突 |
| 用例 6.2b 全打到 base，未命中 gray | consumer `polaris.yaml` 未切换为 `baseLaneMode: 1` | 检查脚本生成的 `${nlg_consumer_mode1_workdir}/polaris.yaml` |
| 用例 7.2 / 7.2d 命中 `:19130` 而非 `:19131` | half-consumer 在 PERMISSIVE 回退基线时未透传 stainLabel | 排查 `half-consumer.log` 中 `[Router][Lane] passthrough stain label` 关键字；检查 consumer 业务代码是否把 `routedResp.RouteMetadata["service-lane"]` 透传给下游 |
| 用例 7.3 STRICT 未返回 503，而是穿透到 `:19131` | SDK 把 STRICT + 无目标 lane 实例错误地降级或穿透 | 定位 `lane_router.go` STRICT 分支：检查 `IgnoreFilterOnlyOnEndChain` 是否被置位，以及 LB 层是否正确返回 `ErrCodeAPIInstanceNotFound` |
| `x.4b` STRICT-503 用例未返回 503 | SDK 端把 STRICT+无实例降级成了 PERMISSIVE 行为（与契约不符） | 升级 polaris-go SDK 版本；定位到 laneRouter.routeToLane 中 STRICT 路径的降级逻辑 |
| `x.2n / x.3n / x.7n～x.12n` 反向用例 FAIL | SDK 把不该命中的请求误染色到了 lane=gray | 排查方向：① stainLabelIndex 是否对短格式标签做了模糊匹配；② Argument EXACT 比较是否被实现成 CONTAINS；③ AND 复合条件是否在缺少子条件时被错误满足 |
| warmup 比例明显偏高（用例 1） | 规则 `etime` 未刷新导致 `uptime` 被放大 | 确认 `validate_rules_with_wait` 成功执行；必要时手动在控制台 disable → enable |
| warmup 用例 2/3 频繁 SKIP | `warmup_interval` 过大 > `MAX_WARMUP_WAIT` | 将规则里的 `warmup_interval` 调小（建议 ≤ 120s） |

---

## 五、清理

- 两个脚本都提供 `stop` 子命令，按 `PID_FILE`（`.lane-test-pids` / `.lane-warmup-pids`）逐个杀掉进程
- 兜底清理：`./cleanup.sh`（覆盖端口、残留进程、临时目录 `.build/.logs/.pids`）
