# 服务鉴权（Authenticator / BlockAllowList）测试方案

本文档描述 `examples/auth/` 目录下端到端测试脚本 [`verify_auth.sh`](./verify_auth.sh) 的**测试方案（目标、前置条件、拓扑、用例、校验点）**。脚本采用**全自动模式**：通过北极星 OpenAPI 自动创建/删除 `BlockAllowListRule`，每个用例独立运行（先清理本服务下所有规则 → 创建本用例所需规则 → 等待 SDK 缓存刷新 → 发请求 → 校验响应 → 进入下一个用例），用例之间互不干扰。

---

## 一、通用信息

| 项 | 值 |
| --- | --- |
| 命名空间 | `default` |
| 被调（Provider）服务 | `AuthEchoServer` |
| 被调端口 | 由 `--provider-port` 指定，`0` 表示自动分配；脚本从日志解析实际端口 |
| 鉴权插件 | `blockAllowList`（polaris-go 内置） |
| 主调标识透传方式 | HTTP Header：`X-Polaris-Caller-Namespace`、`X-Polaris-Caller-Service`、`X-Caller-Meta-<Key>`、`X-Custom-Arg-<Key>` |
| 调用接口 | `GET /echo`：业务接口（鉴权拦截）；`GET /auth-info`：调试接口（不鉴权） |
| 鉴权放行响应 | `HTTP 200`，`X-Auth-Result: ok` |
| 鉴权拒绝响应 | `HTTP 403`，`X-Auth-Result: forbidden`、`X-Auth-Info: <reason>` |
| 规则 OpenAPI 端点 | `${POLARIS_HTTP_ADDR}${RULE_API_PATH}`（默认 `/naming/v1/blockallow/rules`） |
| 规则缓存等待 | `--rule-cache-wait` 秒（默认 5s），用于 SDK LocalRegistry 拉到最新规则 |

通用流程：`构建 provider → 启动（按用例切换 polaris.yaml） → 创建规则 → 等待缓存 → curl → 校验 HTTP + X-Auth-Result`，**provider 进程按需重启**：仅当 `polaris.yaml`（用例 3/4/5 切 enable/chain）或 `--register-metadata`（用例 12 切 env=prod、用例 14 切回空）发生变化时才重启，其余用例复用现有进程，仅 `wait_rule_cache` 让 SDK LocalRegistry 周期拉新规则。一次完整跑全程仅 ~5 次重启。

---

## 二、测试目标

验证 polaris-go SDK 的服务鉴权链路：

1. `provider.auth.enable=false` 或 `chain=[]` 时直接放行（**零开销路径**）；
2. `enable=true` 但控制台无 `BlockAllowListRule` 时按通过处理（与 polaris-java 一致）；
3. 控制台配置规则后，按 `checkAllow` 13 条语义命中放行/拒绝；
4. 覆盖 `HEADER` / `QUERY` / `CALLER_SERVICE` / `CALLER_IP` / `CALLER_METADATA` / `CALLEE_METADATA` / `CUSTOM` **共 7 个维度**的 EXACT/REGEX 匹配（`BlockAllowConfig.MatchArgument` 全部类型端到端覆盖）；
5. `CALLEE_METADATA` 专项：验证 polaris-go Engine 在 Register 成功后登记本端实例 metadata、鉴权时反查命中的新链路（对齐 polaris-java `default` 分支语义）；
6. **多 MatchArgument AND 关系**：单条规则 `arguments` 数组里多个 MatchArgument 之间是 AND 关系（任一不满足整体不命中），由 `plugin/authenticator/blockallowlist/process.go::matchArguments` 实现；
7. **SDK 主调侧透传链路**：用例 1/2 用真实的 polaris-go consumer demo（不是 curl 模拟），验证 `ConsumerAPI.GetOneInstance` 选址 + 自动添加 `X-Polaris-Caller-*` / `X-Caller-Meta-*` Header 的端到端流程，consumer 自身服务名 `AuthEchoClient` 必须正确透传到 provider 鉴权链。

---

## 三、拓扑与端口

```
（用例 3-28：curl 直接模拟主调）
   curl ──► provider:${PROVIDER_PORT}/echo[-<case>]
              │
              │  Authenticate(req: AuthenticateRequest)
              ▼
       polaris-go AuthAPI
              │  按 chain 顺序：blockAllowList
              ▼
       LocalRegistry (BlockAllowRule from Polaris Server)
              ▲
              │  Polaris OpenAPI ${RULE_API_PATH}
              │  GET   → 列举/查询规则（用于 ensure_rule 幂等检查）
              │  POST  → 创建规则（首次跑或同名规则不存在时）
              │  PUT   → 更新规则（仅用例 2 改 caller_service）
        verify_auth.sh

（用例 1/2：consumer ↔ provider 端到端 SDK 透传链路）
   curl ──► consumer:38080/echo
              │
              │  ConsumerAPI.GetOneInstance(svc=AuthEchoServer)  // 服务发现 / 选址
              │  自动加 Header X-Polaris-Caller-Service: AuthEchoClient
              │  自动加 Header X-Caller-Meta-env=dev / version=1.0.0
              ▼
       provider:${PROVIDER_PORT}/echo  → 鉴权链同上
```

`provider/main.go::runWebServer` 用 `http.HandleFunc("/", echoHandler)` 注册兜底处理器，使 `/echo` 与所有 `/echo-*` 路径均走鉴权拦截；`/auth-info` 是精确路径，DefaultServeMux 自动优先匹配。echoHandler 在每个请求里：
1. 把 `Method` / `Path` / `Protocol` / Header / Query / Cookie / RemoteIP 全部装进 `AuthenticateRequest`；
2. 解析 Header `X-Polaris-Caller-Namespace`、`X-Polaris-Caller-Service` 构造 `SourceService`；
3. 解析 Header `X-Caller-Meta-<Key>: <Value>` 构造 `SourceService.Metadata`；
4. 解析 Header `X-Custom-Arg-<Key>: <Value>` 调用 `model.BuildCustomArgument` 上报，用于 `CUSTOM` 维度匹配；
5. 调用 `AuthAPI.Authenticate(req)` —— 命中规则时返回 403 + `X-Auth-Info`；放行时返回 200。

provider 启动时额外支持 `--register-metadata "k1=v1,k2=v2"`：注册时把 metadata 塞进 `InstanceRegisterRequest.Metadata`，polaris-go Engine 成功注册后会在本端登记一份副本供鉴权 `CALLEE_METADATA` 维度反查。

---

## 四、自动化建规则机制

**设计原则**：
1. **服务复用**：脚本不每次新建 service。固定使用两个被调服务名 —— `${SERVICE_NAME}-norule`（用例 3-5 专用，下面不创建任何规则）与 `${SERVICE_NAME}`（用例 6-28 公用，下面常驻 18 条规则）。
2. **规则复用**：18 条规则的名字也是固定的（不带 `RUN_ID`），首次跑由 `ensure_rule` 创建，之后直接复用，跨运行幂等。
3. **path 隔离**：用例 6-28 的每条规则都带 EXACT `api.path` 限定（如 `/echo-allow-vip`、`/echo-callee-prod`、`/echo-mt-regex`、`/echo-query-cn`、`/echo-and-both` 等）。`checkAllow` 的 `matchMethod` 在 protocol/method/path 任一不匹配时直接 `continue`，因此**同 service 下多条规则按 path 互不干扰**，无需删除/更新规则即可保证用例独立性。
4. **不主动删除规则**：脚本只调用 `GET`（查规则）+ `POST`（创建规则），无 `DELETE`/`PUT`，对 token 权限要求最小。退出时只关停 provider。

服务与 path 映射：

| 用例 | 被调服务名 | 请求 path | 规则名 | 规则维度 |
| --- | --- | --- | --- | --- |
| 1/2 | `${SERVICE_NAME}` | `/echo`（consumer 内部硬编码） | `auth-it-smoke-allow-caller-client` | CALLER_SERVICE smoke ALLOW（用例 1 命中 / 用例 2 PUT 改成 NotConsumer 不命中） |
| 3/4/5 | `${SERVICE_NAME}-norule` | `/echo` | — | 不创建规则 |
| 6/7 | `${SERVICE_NAME}` | `/echo-allow-vip` | `auth-it-allow-vip` | HEADER ALLOW |
| 8/9 | `${SERVICE_NAME}` | `/echo-block-bad` | `auth-it-block-bad` | HEADER BLOCK |
| 10/11 | `${SERVICE_NAME}` | `/echo-allow-trusted` | `auth-it-allow-trusted` | CALLER_SERVICE ALLOW |
| 12 | `${SERVICE_NAME}` | `/echo-callee-prod` | `auth-it-allow-callee-env-prod` | CALLEE_METADATA env=prod |
| 13 | `${SERVICE_NAME}` | `/echo-callee-dev` | `auth-it-allow-callee-env-dev` | CALLEE_METADATA env=dev |
| 14 | `${SERVICE_NAME}` | `/echo-custom-v1` | `auth-it-allow-custom-biz-v1` | CUSTOM biz=v1 |
| 15 | `${SERVICE_NAME}` | `/echo-custom-v2` | `auth-it-allow-custom-biz-v2` | CUSTOM biz=v2 |
| 16 | `${SERVICE_NAME}` | `/echo-mt-regex` | `auth-it-block-regex` | HEADER user **REGEX** `^bad-.*$` |
| 17 | `${SERVICE_NAME}` | `/echo-mt-not-equals` | `auth-it-block-not-equals` | HEADER user **NOT_EQUALS** admin |
| 18 | `${SERVICE_NAME}` | `/echo-mt-in` | `auth-it-block-in` | HEADER user **IN** bad1,bad2,bad3 |
| 19 | `${SERVICE_NAME}` | `/echo-mt-not-in` | `auth-it-block-not-in` | HEADER user **NOT_IN** admin,operator |
| 20 | `${SERVICE_NAME}` | `/echo-mt-range` | `auth-it-block-range` | HEADER user **RANGE** 100~200 |
| 21/22 | `${SERVICE_NAME}` | `/echo-query-cn` | `auth-it-allow-query-cn` | QUERY region=cn ALLOW |
| 23 | `${SERVICE_NAME}` | `/echo-caller-ip-block` | `auth-it-block-caller-ip-loopback` | CALLER_IP REGEX `^(127\.0\.0\.1\|::1)$` BLOCK |
| 24 | `${SERVICE_NAME}` | `/echo-caller-ip-allow` | `auth-it-allow-caller-ip-loopback` | CALLER_IP REGEX `^(127\.0\.0\.1\|::1)$` ALLOW |
| 25/26 | `${SERVICE_NAME}` | `/echo-caller-meta-gold` | `auth-it-allow-caller-meta-gold` | CALLER_METADATA tier=gold ALLOW |
| 27/28 | `${SERVICE_NAME}` | `/echo-and-both` | `auth-it-allow-and-header-query` | **AND** [HEADER user=vip] ∧ [QUERY region=cn] ALLOW |

> 用例 1/2 是 consumer ↔ provider 端到端 smoke：用真实的 polaris-go consumer demo（`examples/auth/consumer/`）发请求，验证 SDK 主调侧 `X-Polaris-Caller-*` Header 透传链路。consumer 自身 `selfService=AuthEchoClient`，调用目标 `service=AuthEchoServer`。规则在两次跑之间通过 PUT 切换 caller_service=AuthEchoClient（命中→consumer 透传 200）和 caller_service=NotConsumer（不命中→consumer 透传 provider 的 403 给 curl）。consumer demo 已实现把 provider 的状态码 + `X-Auth-Result/X-Auth-Info` Header 透传给上层，所以鉴权拒绝时 curl 直接看到 403。
> 用例 6+7 / 8+9 / 10+11 / 21+22 / 25+26 / 27+28 共用一条规则（共用 path），只是改请求参数验证命中/不命中；
> 用例 12/13、14/15、23/24 拆开两条不同 path 的规则，原因是同一 path 下若同时存在两条白名单规则，
> 任一命中即放行，"不命中"用例无法证伪。
> 用例 16-20 每个对应一种 MatchString 类型，独立 path、独立规则；全部 BLOCK_LIST + 命中场景，
> BLOCK 命中走 `return false` 直接出，不依赖 `containsAllowList` 状态。
> 用例 21-26 补齐 QUERY / CALLER_IP / CALLER_METADATA 三个维度，让 `BlockAllowConfig.MatchArgument` 7 种类型端到端全覆盖。
> 用例 27-28 验证单条规则内两个 MatchArgument 的 **AND** 关系：27 全满足 → ALLOW 命中；28 仅 HEADER 满足 → matchArguments 返回 false → 白名单未命中 → 403。

provider 侧 path 路由：`provider/main.go::runWebServer` 把 `http.HandleFunc("/", echoHandler)` 注册成兜底处理器，使 `/echo` 与所有 `/echo-*` 路径均走鉴权拦截；`/auth-info` 是精确路径，DefaultServeMux 自动优先匹配。

脚本提供以下 OpenAPI 调用封装：

| 函数 | 作用 | 对应 OpenAPI |
| --- | --- | --- |
| `probe_open_api` | 启动时探测端点是否可达，否则告警提示用 `--rule-api-path` 覆盖 | `GET ${RULE_API_PATH}?namespace=&service=&offset=0&limit=1` |
| `rule_exists_by_name <name>` | 精确按 name 查询，返回 0=存在 / 1=不存在 | `GET ${RULE_API_PATH}?namespace=&service=&name=&offset=0&limit=1`（通过 `"amount": N` 判断） |
| `create_rule <rule_json>` | 创建规则，body 为 `[<rule_json>]` 数组 | `POST ${RULE_API_PATH}` |
| `ensure_rule <name> <rule_json>` | 同名规则存在则复用，不存在则 `create_rule` | 组合 `rule_exists_by_name` + `create_rule` |
| `list_rules_for_service`（仅调试） | 列举本服务下所有规则 id | `GET ${RULE_API_PATH}?namespace=&service=&offset=0&limit=100` |
| `build_api_path_block <path>` | 构造规则 JSON 中 `api.path` 子句（EXACT 匹配） | — |
| `rule_with_header_match` | 构造一条 Header EXACT 匹配的规则（白/黑名单），自带 path 限定 | — |
| `rule_with_caller_service_match` | 构造一条 CALLER_SERVICE EXACT 匹配的规则（白/黑名单），自带 path 限定 | — |
| `rule_with_callee_metadata_match` | 构造一条 CALLEE_METADATA EXACT 匹配的规则（白/黑名单），自带 path 限定 | — |
| `rule_with_custom_match` | 构造一条 CUSTOM EXACT 匹配的规则（白/黑名单），自带 path 限定 | — |
| `rule_with_query_match` | 构造一条 QUERY 维度匹配的规则（白/黑名单），自带 path 限定 | — |
| `rule_with_caller_ip_match` | 构造一条 CALLER_IP 维度匹配的规则（白/黑名单），自带 path 限定，key 固定为空 | — |
| `rule_with_caller_metadata_match` | 构造一条 CALLER_METADATA 维度匹配的规则（白/黑名单），自带 path 限定 | — |
| `rule_with_two_args_header_query` | 构造一条同时包含 HEADER + QUERY 两个 MatchArgument 的规则（AND 关系），自带 path 限定 | — |
| `update_rule <rule_json>` | PUT 更新一条规则（用例 2 把 smoke 规则的 caller_service 改成不命中值） | `PUT ${RULE_API_PATH}` |
| `start_consumer` | 启动 consumer 进程（仅用例 1/2 使用），用 polaris-go SDK 走 GetOneInstance + 透传 X-Polaris-Caller-* Header | — |
| `stop_consumer` | 用例 2 跑完立即关 consumer 释放 38080 端口；后续用例 3-28 不再用 consumer | — |
| `do_consumer_call <expect> <label>` | 通过 consumer `/echo` 转发到 provider，校验 consumer 透传出来的 HTTP 状态码（命中→200 / 未命中→403；consumer demo 已修复成透传 provider 的状态码 + `X-Auth-Result/X-Auth-Info` Header） | — |
| `verify_provider_caller_log <caller_svc>` | 在 provider 日志里查找 `[CALLER] caller service: ... service="<caller_svc>"`，验证 SDK 透传成功 | — |

> **OpenAPI 端点**（以 polaris-server `apiserver/httpserver/discover/v1/server.go` 为准）：
>
> | 操作 | Method | Path |
> | --- | --- | --- |
> | 列举 | GET  | `/naming/v1/blockallow/rules` |
> | 创建 | POST | `/naming/v1/blockallow/rules` |
> | 更新 | PUT  | `/naming/v1/blockallow/rules` |
> | 删除 | POST | `/naming/v1/blockallow/rules/delete` |
> | 启用/禁用 | PUT | `/naming/v1/blockallow/rules/enable` |
>
> 本脚本主要使用 **GET（列举/查询）+ POST（创建）**，**仅用例 2** 使用 **PUT（更新规则的 caller_service）**；如果 token 权限不足以 PUT，用例 2 自动 SKIP。其余 27 个用例不依赖 PUT，即便不传 `--polaris-token` 也能完整跑完。
>
> 若服务端实际端点不同，用 `--rule-api-path <路径>` 覆盖即可。
>
> **provider 进程按需重启**：`ensure_rule` 检查同名规则不存在才创建，存在则复用；`prepare_case_with_rule` 同时比较「本用例期望的 register-metadata」+「polaris.yaml 的 enable/chain 段」与 provider 当前进程状态，全部相同则不重启进程，仅 `wait_rule_cache` 让 SDK LocalRegistry 拉新规则；任一不同才重启。`provider.auth.enable` / `chain` 是 SDK 启动期配置（polaris-go 不支持热加载），用例 3/4/5 切配置必须重启。整轮跑 28 个用例仅触发 ~7 次重启（用例 1/2 的 consumer smoke 占 1 次 provider 启动 + 1 次 consumer 启动），相比原来"每用例独立重启"显著缩短。
>
> **退出清理**：`trap cleanup EXIT` 只关停 provider/consumer，不删除规则、不删除 service；下次跑 `ensure_rule` 直接命中已有规则，无副作用。

---

## 五、用例矩阵

下表中每个用例的"本 path 命中规则"列描述**经 `matchMethod` 过滤后实际进入 `matchArguments` 的那条规则**。

> **本 service 全规则集（用例 6-28 共用 `${SERVICE_NAME}`）**：18 条规则常驻（含用例 1/2 创建的 1 条 smoke 规则），**11 条 ALLOW_LIST + 7 条 BLOCK_LIST**，每条带 EXACT `api.path` 限定。
>
> | 序号 | 规则名 | policy | api.path | argument |
> | --- | --- | --- | --- | --- |
> | 0 | `auth-it-smoke-allow-caller-client` | ALLOW_LIST | `/echo` | CALLER_SERVICE `default/<NotConsumer>` EXACT（用例 2 跑完是 NotConsumer 状态） |
> | 1 | `auth-it-allow-vip` | ALLOW_LIST | `/echo-allow-vip` | HEADER `user` EXACT `vip` |
> | 2 | `auth-it-block-bad` | BLOCK_LIST | `/echo-block-bad` | HEADER `user` EXACT `bad` |
> | 3 | `auth-it-allow-trusted` | ALLOW_LIST | `/echo-allow-trusted` | CALLER_SERVICE `default/trusted` EXACT |
> | 4 | `auth-it-allow-callee-env-prod` | ALLOW_LIST | `/echo-callee-prod` | CALLEE_METADATA `env` EXACT `prod` |
> | 5 | `auth-it-allow-callee-env-dev` | ALLOW_LIST | `/echo-callee-dev` | CALLEE_METADATA `env` EXACT `dev` |
> | 6 | `auth-it-allow-custom-biz-v1` | ALLOW_LIST | `/echo-custom-v1` | CUSTOM `biz` EXACT `v1` |
> | 7 | `auth-it-allow-custom-biz-v2` | ALLOW_LIST | `/echo-custom-v2` | CUSTOM `biz` EXACT `v2` |
> | 8 | `auth-it-block-regex` | BLOCK_LIST | `/echo-mt-regex` | HEADER `user` **REGEX** `^bad-.*$` |
> | 9 | `auth-it-block-not-equals` | BLOCK_LIST | `/echo-mt-not-equals` | HEADER `user` **NOT_EQUALS** `admin` |
> | 10 | `auth-it-block-in` | BLOCK_LIST | `/echo-mt-in` | HEADER `user` **IN** `bad1,bad2,bad3` |
> | 11 | `auth-it-block-not-in` | BLOCK_LIST | `/echo-mt-not-in` | HEADER `user` **NOT_IN** `admin,operator` |
> | 12 | `auth-it-block-range` | BLOCK_LIST | `/echo-mt-range` | HEADER `user` **RANGE** `100~200` |
> | 13 | `auth-it-allow-query-cn` | ALLOW_LIST | `/echo-query-cn` | QUERY `region` EXACT `cn` |
> | 14 | `auth-it-block-caller-ip-loopback` | BLOCK_LIST | `/echo-caller-ip-block` | CALLER_IP **REGEX** `^(127\.0\.0\.1\|::1)$` |
> | 15 | `auth-it-allow-caller-ip-loopback` | ALLOW_LIST | `/echo-caller-ip-allow` | CALLER_IP **REGEX** `^(127\.0\.0\.1\|::1)$` |
> | 16 | `auth-it-allow-caller-meta-gold` | ALLOW_LIST | `/echo-caller-meta-gold` | CALLER_METADATA `tier` EXACT `gold` |
> | 17 | `auth-it-allow-and-header-query` | ALLOW_LIST | `/echo-and-both` | **AND**: HEADER `user` EXACT `vip` ∧ QUERY `region` EXACT `cn` |
>
> 因 service 下存在 ALLOW_LIST 规则，`checkAllow` 内 `containsAllowList` 在 `matchMethod` 之前即被置 `true`（与 polaris-java 一致），**任何未命中规则的请求都会落入 `return !containsAllowList = false` → 403**。这就是用例 7/9/11/13/15/22/26/28 的共同根因。
>
> 用例 16-20 选 BLOCK_LIST + 命中：BLOCK 命中走 `return false` 直接出，不需要依赖 `containsAllowList` 的状态，能干净地断言"匹配类型本身工作正确"；其余 4 种 MatchString 类型 + 不命中 → 200 的语义由 `pkg/algorithm/match/match_test.go` 单测覆盖。
>
> 用例 21-26 把 BlockAllowConfig 7 种 MatchArgument 类型补齐：之前 4-13 已覆盖 HEADER / CALLER_SERVICE / CALLEE_METADATA / CUSTOM，19-24 再补 QUERY / CALLER_IP / CALLER_METADATA。CALLER_IP 用 REGEX 兜底 IPv4 / IPv6 loopback 差异。
>
> 用例 27-28 验证 `matchArguments` 的 **AND 关系**：单条规则两个 MatchArgument，全部满足才整体命中。25 同时带 HEADER `user=vip` 与 QUERY `region=cn` → ALLOW 命中 → 200；26 仅带 HEADER 不带 QUERY → 第二个 MatchArgument 取空 ≠ `cn` → matchArguments 返回 false → 白名单未命中 + containsAllowList=true → 403。

| 编号 | 鉴权配置 | 本 path 命中规则（经 matchMethod 过滤） | 请求条件 | 预期 | 裁决路径 |
| --- | --- | --- | --- | --- | --- |
| **用例 1** | `enable=true`, `chain=[blockAllowList]` | `auth-it-smoke-allow-caller-client`（ALLOW_LIST，CALLER_SERVICE `default/AuthEchoClient`） | consumer demo `curl http://127.0.0.1:38080/echo` → 内部 `GetOneInstance` 选址 + 自动加 `X-Polaris-Caller-Service: AuthEchoClient` Header | consumer `200` | provider 收到带主调身份 Header 的请求，命中 ALLOW_LIST → 200；同时校验 provider 日志包含 `[CALLER] caller service: ... service="AuthEchoClient"` 行（验证 SDK 透传成功） |
| **用例 2** | `enable=true`, `chain=[blockAllowList]` | 同上规则但 PUT 更新 caller_service=`NotConsumer` | 同上 curl | consumer `403` | 主调身份 `AuthEchoClient` ≠ `NotConsumer` → 不命中 ALLOW_LIST → containsAllowList=true → provider 返回 403；consumer demo 透传 provider 的状态码与 `X-Auth-Result: forbidden` Header → curl 直接拿到 403 |
| **用例 3** | `enable=false` | service=`*-norule`，规则集为空 | `GET /echo` | `200`，`ok` | 鉴权链未加载，`SyncAuthenticate` 直接返回 Ok（零开销路径） |
| **用例 4** | `enable=true`, `chain=[blockAllowList]` | service=`*-norule`，规则集为空 | `GET /echo` | `200`，`ok` | 鉴权链加载，但 `getBlockAllowListRules` 返回空 → 直接放行 |
| **用例 5** | `enable=true`, `chain=[]` | service=`*-norule`，规则集为空 | `GET /echo` | `200`，`ok` | `loadAuthenticators` 因 `chain` 为空提前 return；`authenticators` 切片为空 → 等同未加载 |
| **用例 6** | `enable=true` | `auth-it-allow-vip`（ALLOW_LIST，HEADER `user=vip`） | `GET /echo-allow-vip` Header `user: vip` | `200`，`ok` | `matchArguments` 命中 → `return true`（ALLOW_LIST） |
| **用例 7** | `enable=true` | 同上 `auth-it-allow-vip` | `GET /echo-allow-vip`（不带 user） | `403`，`forbidden` | `matchArguments` 未命中；`containsAllowList=true` → `return !containsAllowList = false` |
| **用例 8** | `enable=true` | `auth-it-block-bad`（BLOCK_LIST，HEADER `user=bad`） | `GET /echo-block-bad` Header `user: bad` | `403`，`forbidden` | `matchArguments` 命中 → `return false`（BLOCK_LIST） |
| **用例 9** | `enable=true` | 同上 `auth-it-block-bad` | `GET /echo-block-bad` Header `user: normal` | `403`，`forbidden` | `matchArguments` 未命中；service 下存在其它 6 条 ALLOW_LIST → `containsAllowList=true` → 拒绝（混合策略） |
| **用例 10** | `enable=true` | `auth-it-allow-trusted`（ALLOW_LIST，CALLER_SERVICE `default/trusted`） | `GET /echo-allow-trusted` Header `X-Polaris-Caller-Namespace: default` + `X-Polaris-Caller-Service: trusted` | `200`，`ok` | provider 解析 `X-Polaris-Caller-*` 构造 `SourceService`；`getLabelValue(CALLER_SERVICE)` 返回 `trusted`；EXACT 匹配命中 |
| **用例 11** | `enable=true` | 同上 `auth-it-allow-trusted` | `GET /echo-allow-trusted` Header `X-Polaris-Caller-Service: other` | `403`，`forbidden` | `getLabelValue(CALLER_SERVICE)` 返回 `other`；`matchArguments` 未命中；containsAllowList=true → 拒绝 |
| **用例 12** | `enable=true` | `auth-it-allow-callee-env-prod`（ALLOW_LIST，CALLEE_METADATA `env=prod`） | provider 以 `--register-metadata env=prod` 启动；`GET /echo-callee-prod` | `200`，`ok` | Engine.`RegisterLocalInstanceMetadata` 在注册成功后把 `{env:prod}` 登记到本端；`getLabelValue(CALLEE_METADATA)` 命中 → ALLOW |
| **用例 13** | `enable=true` | `auth-it-allow-callee-env-dev`（ALLOW_LIST，CALLEE_METADATA `env=dev`） | provider 仍 `--register-metadata env=prod`；`GET /echo-callee-dev` | `403`，`forbidden` | 本端 metadata 只有 `env=prod`，规则要求 `env=dev` → 未命中；containsAllowList=true → 拒绝 |
| **用例 14** | `enable=true` | `auth-it-allow-custom-biz-v1`（ALLOW_LIST，CUSTOM `biz=v1`） | `GET /echo-custom-v1` Header `X-Custom-Arg-biz: v1` | `200`，`ok` | provider 把 `X-Custom-Arg-biz` 转成 `BuildCustomArgument("biz","v1")`；`getLabelValue(CUSTOM)` 取到 `v1`，EXACT 命中 |
| **用例 15** | `enable=true` | `auth-it-allow-custom-biz-v2`（ALLOW_LIST，CUSTOM `biz=v2`） | `GET /echo-custom-v2` Header `X-Custom-Arg-biz: v1` | `403`，`forbidden` | 请求 Argument `biz=v1` ≠ 规则 `biz=v2` → 未命中；containsAllowList=true → 拒绝 |
| **用例 16** | `enable=true` | `auth-it-block-regex`（BLOCK_LIST，HEADER `user` **REGEX** `^bad-.*$`） | `GET /echo-mt-regex` Header `user: bad-foo` | `403`，`forbidden` | `match.MatchString` 走 `MatchString_REGEX` 分支：`regexp2.MatchString(bad-foo, ^bad-.*$) = true` → BLOCK_LIST 命中 → `return false` |
| **用例 17** | `enable=true` | `auth-it-block-not-equals`（BLOCK_LIST，HEADER `user` **NOT_EQUALS** `admin`） | `GET /echo-mt-not-equals` Header `user: guest` | `403`，`forbidden` | 走 `MatchString_NOT_EQUALS` 分支：`guest != admin` → BLOCK_LIST 命中 → `return false`。**注意**：value 不能写 `*` 或空串，否则 `IsMatchAll` 短路会让 NOT_EQUALS 直接 `return true` |
| **用例 18** | `enable=true` | `auth-it-block-in`（BLOCK_LIST，HEADER `user` **IN** `bad1,bad2,bad3`） | `GET /echo-mt-in` Header `user: bad2` | `403`，`forbidden` | 走 `MatchString_IN` 分支：rule value 按 `,` 分隔，`bad2` 在集合中 → BLOCK_LIST 命中 → `return false` |
| **用例 19** | `enable=true` | `auth-it-block-not-in`（BLOCK_LIST，HEADER `user` **NOT_IN** `admin,operator`） | `GET /echo-mt-not-in` Header `user: guest` | `403`，`forbidden` | 走 `MatchString_NOT_IN` 分支：`guest` 不在集合 `{admin,operator}` 中 → BLOCK_LIST 命中 → `return false` |
| **用例 20** | `enable=true` | `auth-it-block-range`（BLOCK_LIST，HEADER `user` **RANGE** `100~200`） | `GET /echo-mt-range` Header `user: 150` | `403`，`forbidden` | 走 `MatchString_RANGE` 分支：rule value 按 `~` 分隔成 `[100, 200]`，`ParseInt("150")=150 ∈ [100,200]` → BLOCK_LIST 命中 → `return false`。**注意**：RANGE 仅支持整数，非整数输入（如 `abc`/`1.5`）会让 `ParseInt` 失败 → 该规则视为不匹配 |
| **用例 21** | `enable=true` | `auth-it-allow-query-cn`（ALLOW_LIST，QUERY `region=cn`） | `GET /echo-query-cn?region=cn` | `200`，`ok` | provider 把 `r.URL.Query()` 转成 `BuildQueryArgument("region","cn")`；`getLabelValue(QUERY)` 取到 `cn`，EXACT 命中 → ALLOW |
| **用例 22** | `enable=true` | 同上 `auth-it-allow-query-cn` | `GET /echo-query-cn?region=us` | `403`，`forbidden` | QUERY 取到 `us` ≠ `cn` → 未命中；containsAllowList=true → 拒绝 |
| **用例 23** | `enable=true` | `auth-it-block-caller-ip-loopback`（BLOCK_LIST，CALLER_IP **REGEX** `^(127\.0\.0\.1\|::1)$`） | `GET /echo-caller-ip-block`（本机 curl） | `403`，`forbidden` | provider 调 `BuildCallerIPArgument(extractRemoteIP)`；`getLabelValue(CALLER_IP)` 取到 `127.0.0.1` 或 `::1`；正则命中 → BLOCK 拒绝 |
| **用例 24** | `enable=true` | `auth-it-allow-caller-ip-loopback`（ALLOW_LIST，CALLER_IP **REGEX** `^(127\.0\.0\.1\|::1)$`） | `GET /echo-caller-ip-allow`（本机 curl） | `200`，`ok` | 同上路径，规则改为 ALLOW；正则命中 → ALLOW 放行 |
| **用例 25** | `enable=true` | `auth-it-allow-caller-meta-gold`（ALLOW_LIST，CALLER_METADATA `tier=gold`） | `GET /echo-caller-meta-gold` Header `X-Polaris-Caller-Service: app` + `X-Caller-Meta-tier: gold` | `200`，`ok` | provider 解析 `X-Polaris-Caller-*` 构造 `SourceService.Metadata={tier:gold}`；`getLabelValue(CALLER_METADATA)` 取到 `gold`，EXACT 命中 |
| **用例 26** | `enable=true` | 同上 `auth-it-allow-caller-meta-gold` | `GET /echo-caller-meta-gold` Header `X-Polaris-Caller-Service: app` + `X-Caller-Meta-tier: silver` | `403`，`forbidden` | CALLER_METADATA 取到 `silver` ≠ `gold` → 未命中；containsAllowList=true → 拒绝 |
| **用例 27** | `enable=true` | `auth-it-allow-and-header-query`（ALLOW_LIST，**AND**：HEADER `user=vip` ∧ QUERY `region=cn`） | `GET /echo-and-both?region=cn` Header `user: vip` | `200`，`ok` | `matchArguments` 遍历 2 个 MatchArgument 都满足 → 整体命中 → ALLOW 放行（验证 AND 关系正向） |
| **用例 28** | `enable=true` | 同上 `auth-it-allow-and-header-query` | `GET /echo-and-both` Header `user: vip`（不带 query） | `403`，`forbidden` | HEADER 满足；QUERY `region` 取到空 ≠ `cn` → `matchArguments` 在第二个 MatchArgument 上 `return false`；白名单未命中 + containsAllowList=true → 拒绝（验证 AND 关系反向：拆穿一条即整体失败） |

> **`checkAllow` 三段式语义**（参考 polaris-java，本 SDK 实现位于 `plugin/authenticator/blockallowlist/process.go::checkAllow`）：
> 1. **全部为白名单**：任一匹配即通过；都不匹配则拒绝（`containsAllowList=true → return false`）；
> 2. **全部为黑名单**：任一匹配即拒绝；都不匹配则通过；
> 3. **混合**：任一白名单匹配即通过；都不匹配且存在白名单则拒绝。
>
> 端到端脚本是**第 3 类（混合）场景**的覆盖：service 下同时存在 ALLOW_LIST 与 BLOCK_LIST 规则，因此用例 7/9/11/13/15/22/26/28 都属于"白名单未命中 → 拒绝"分支。**纯黑名单不命中 → 200** 的纯净语义由单测 `TestCheckAllow_OnlyBlockList_NotMatch_ShouldPass` 覆盖；**纯白名单不命中 → 403** 的语义由 `TestCheckAllow_OnlyAllowList_NotMatch_ShouldReject` 覆盖。
>
> **MatchString 6 种类型完整覆盖**：
> - `EXACT`：用例 6-15、19-20、23-26（HEADER/CALLER_SERVICE/CALLEE_METADATA/CUSTOM/QUERY/CALLER_METADATA 各维度）
> - `REGEX` / `NOT_EQUALS` / `IN` / `NOT_IN` / `RANGE`：用例 16-20（端到端 + BLOCK_LIST 命中场景），其中 21/22 也用 REGEX 兜底 IPv4/IPv6 loopback
> - 不命中场景由 `pkg/algorithm/match/match_test.go` 单测覆盖（共 14 用例）
>
> **MatchArgument 7 种类型完整覆盖**（`BlockAllowConfig.MatchArgument.Type`）：
> - `HEADER`：用例 6-9、14-18、25-26
> - `QUERY`：用例 21-22、25-26
> - `CALLER_SERVICE`：用例 10-11
> - `CALLER_IP`：用例 23-24
> - `CALLER_METADATA`：用例 25-26
> - `CALLEE_METADATA`：用例 12-13
> - `CUSTOM`：用例 14-15
>
> **多 MatchArgument AND 关系**（`matchArguments` 遍历语义）：
> - 用例 27：单条规则 2 个 MatchArgument 都满足 → 整体命中（AND 正向）
> - 用例 28：仅 1 个 MatchArgument 满足 → 整体不命中（AND 反向）

---

## 六、规则 JSON 模板

> **字段名采用 `specification` proto 中明确指定的 `json_name`，全部为 snake_case**（如 `block_allow_config` / `block_allow_policy` / `value_type`）。如果你手工拼写请勿用驼峰 `blockAllowConfig`，polaris-server 的 `jsonpb.UnmarshalNext` 默认严格校验，未知字段会被拒绝。

> **每条规则都带 `api` 子段限定 path**（path 隔离），让同 service 下多条规则按路径互不干扰。`api.path.value` 为对应用例的请求 path（如 `/echo-allow-vip`）。

`rule_with_header_match`（path 限定 + Header EXACT）：
```json
{
  "name": "<rule-name>",
  "namespace": "default",
  "service": "AuthEchoServer",
  "enable": true,
  "block_allow_config": [{
    "api": {
      "protocol": "HTTP",
      "path": {"type": "EXACT", "value_type": "TEXT", "value": "/echo-allow-vip"}
    },
    "block_allow_policy": "ALLOW_LIST",        // 或 BLOCK_LIST
    "arguments": [{
      "type": "HEADER",
      "key": "user",
      "value": {"type": "EXACT", "value_type": "TEXT", "value": "vip"}
    }]
  }]
}
```

`rule_with_caller_service_match`（path 限定 + CALLER_SERVICE EXACT）：
```json
{
  "name": "<rule-name>",
  "namespace": "default",
  "service": "AuthEchoServer",
  "enable": true,
  "block_allow_config": [{
    "api": {
      "protocol": "HTTP",
      "path": {"type": "EXACT", "value_type": "TEXT", "value": "/echo-allow-trusted"}
    },
    "block_allow_policy": "ALLOW_LIST",
    "arguments": [{
      "type": "CALLER_SERVICE",
      "key": "default",                       // 主调命名空间，"*" 表示匹配所有
      "value": {"type": "EXACT", "value_type": "TEXT", "value": "trusted"}
    }]
  }]
}
```

`rule_with_callee_metadata_match`（path 限定 + CALLEE_METADATA EXACT）：
```json
{
  "name": "<rule-name>",
  "namespace": "default",
  "service": "AuthEchoServer",
  "enable": true,
  "block_allow_config": [{
    "api": {
      "protocol": "HTTP",
      "path": {"type": "EXACT", "value_type": "TEXT", "value": "/echo-callee-prod"}
    },
    "block_allow_policy": "ALLOW_LIST",
    "arguments": [{
      "type": "CALLEE_METADATA",
      "key": "env",                           // 被调实例 metadata 的 key
      "value": {"type": "EXACT", "value_type": "TEXT", "value": "prod"}
    }]
  }]
}
```

> CALLEE_METADATA 数据源 = polaris-go SDK 本端通过 `RegisterInstance` 登记的实例 metadata。
> provider 必须以 `--register-metadata env=prod` 启动，此规则才可能命中；否则 `GetLocalInstanceMetadata` 返回空 → 白名单不命中 → 拒绝。

`rule_with_custom_match`（path 限定 + CUSTOM EXACT）：
```json
{
  "name": "<rule-name>",
  "namespace": "default",
  "service": "AuthEchoServer",
  "enable": true,
  "block_allow_config": [{
    "api": {
      "protocol": "HTTP",
      "path": {"type": "EXACT", "value_type": "TEXT", "value": "/echo-custom-v1"}
    },
    "block_allow_policy": "ALLOW_LIST",
    "arguments": [{
      "type": "CUSTOM",
      "key": "biz",                           // 自定义标签 key
      "value": {"type": "EXACT", "value_type": "TEXT", "value": "v1"}
    }]
  }]
}
```

> CUSTOM 数据源优先从请求 Arguments(Custom) 取；示例 provider 约定通过 `X-Custom-Arg-<Key>: <Value>` Header 透传并在 `authenticate()` 中调用 `model.BuildCustomArgument` 上报。

`rule_with_query_match`（path 限定 + QUERY EXACT）：
```json
{
  "name": "<rule-name>",
  "namespace": "default",
  "service": "AuthEchoServer",
  "enable": true,
  "block_allow_config": [{
    "api": {
      "protocol": "HTTP",
      "path": {"type": "EXACT", "value_type": "TEXT", "value": "/echo-query-cn"}
    },
    "block_allow_policy": "ALLOW_LIST",
    "arguments": [{
      "type": "QUERY",
      "key": "region",                        // URL query 的 key
      "value": {"type": "EXACT", "value_type": "TEXT", "value": "cn"}
    }]
  }]
}
```

`rule_with_caller_ip_match`（path 限定 + CALLER_IP REGEX 兜底 IPv4/IPv6 loopback）：
```json
{
  "name": "<rule-name>",
  "namespace": "default",
  "service": "AuthEchoServer",
  "enable": true,
  "block_allow_config": [{
    "api": {
      "protocol": "HTTP",
      "path": {"type": "EXACT", "value_type": "TEXT", "value": "/echo-caller-ip-block"}
    },
    "block_allow_policy": "BLOCK_LIST",
    "arguments": [{
      "type": "CALLER_IP",
      "key": "",                              // SDK 取值时 key 被忽略，固定写空串
      "value": {"type": "REGEX", "value_type": "TEXT", "value": "^(127\\.0\\.0\\.1|::1)$"}
    }]
  }]
}
```

> CALLER_IP 数据源是 provider `BuildCallerIPArgument(extractRemoteIP(r))`。本机 curl 时 `r.RemoteAddr` 通常是 `127.0.0.1`，IPv6 优先时可能是 `::1`。用 REGEX 兜底两种环境差异。

`rule_with_caller_metadata_match`（path 限定 + CALLER_METADATA EXACT）：
```json
{
  "name": "<rule-name>",
  "namespace": "default",
  "service": "AuthEchoServer",
  "enable": true,
  "block_allow_config": [{
    "api": {
      "protocol": "HTTP",
      "path": {"type": "EXACT", "value_type": "TEXT", "value": "/echo-caller-meta-gold"}
    },
    "block_allow_policy": "ALLOW_LIST",
    "arguments": [{
      "type": "CALLER_METADATA",
      "key": "tier",                          // 主调实例 metadata 的 key
      "value": {"type": "EXACT", "value_type": "TEXT", "value": "gold"}
    }]
  }]
}
```

> CALLER_METADATA 数据源 = `SourceService.Metadata`。请求必须同时带 `X-Polaris-Caller-Service: <svc>` 与 `X-Caller-Meta-<Key>: <Value>`，否则 provider 不构造 `SourceService`，CALLER_METADATA 永远取空 → 不命中。

`rule_with_two_args_header_query`（path 限定 + 双 MatchArgument AND）：
```json
{
  "name": "<rule-name>",
  "namespace": "default",
  "service": "AuthEchoServer",
  "enable": true,
  "block_allow_config": [{
    "api": {
      "protocol": "HTTP",
      "path": {"type": "EXACT", "value_type": "TEXT", "value": "/echo-and-both"}
    },
    "block_allow_policy": "ALLOW_LIST",
    "arguments": [
      {
        "type": "HEADER",
        "key": "user",
        "value": {"type": "EXACT", "value_type": "TEXT", "value": "vip"}
      },
      {
        "type": "QUERY",
        "key": "region",
        "value": {"type": "EXACT", "value_type": "TEXT", "value": "cn"}
      }
    ]
  }]
}
```

> `arguments` 数组里的多个 MatchArgument 是 **AND** 关系（`plugin/authenticator/blockallowlist/process.go::matchArguments` 遍历，任一不满足返回 false）。两个 MatchArgument 全部命中才算整条规则命中；缺任一即不命中。

POST 提交时 body 为 `[<rule_json>]` 数组，与北极星 OpenAPI 批量 API 习惯一致。

---

## 七、执行与结果

```bash
# 推荐：传入 token 启用全部 28 条用例
./verify_auth.sh \
    --polaris-server 127.0.0.1 \
    --polaris-token <ADMIN_TOKEN>

# OpenAPI 端点不同则覆盖：
./verify_auth.sh --polaris-server 127.0.0.1 --polaris-token <token> \
    --rule-api-path /naming/v1/blockallow/rules

# Debug 模式（打印 OpenAPI 响应、curl 响应体）：
./verify_auth.sh --polaris-server 127.0.0.1 --polaris-token <token> --debug
```

- 脚本日志（双写）：`./.logs/verify-auth-<时间戳>.log`，与 stdout 内容一致，**已去除 ANSI 颜色码**便于 grep / CI 归档
- Provider 日志：`./.logs/provider.log`
- 汇总：脚本末尾输出 `TOTAL / PASS / FAIL / SKIP`
- 创建规则失败的用例自动 SKIP（不影响其他用例继续跑）；存在 FAIL 时整体退出码非 0

---

## 八、清理

```bash
./cleanup.sh        # 默认模式：先展示再确认
./cleanup.sh -f     # 强制模式
./cleanup.sh --dry-run  # 仅展示
```

`cleanup.sh` 会：
1. 杀掉残留的 `auth_provider` / `auth_consumer` 进程（用例 1/2 启动的 consumer 跑完会立即关闭，正常情况下 cleanup.sh 跑时只剩 provider）；
2. 询问后清理 `./.build` 与 `./.logs` 目录（包括 `verify-auth-*.log` / `provider.log` / `consumer.log`）。

> verify_auth.sh 不删除规则、不删除 service：固定的 18 条规则首次跑创建后跨运行复用（含用例 1/2 的 smoke 规则 `auth-it-smoke-allow-caller-client`）；service 名也固定（`${SERVICE_NAME}-norule` 与 `${SERVICE_NAME}`），避免不断生成新 service。如需要彻底清理，去 Polaris 控制台手工操作。

---

## 九、常见问题排查

| 症状 | 可能原因 | 处置 |
| --- | --- | --- |
| `Provider 启动失败` | `polaris.yaml` 中 `serverConnector.addresses` 不可达 | 检查 `--polaris-server` 与 8091 端口连通性 |
| OpenAPI 探测告警 HTTP=404 | 服务端实际端点不同 | 用 `--rule-api-path` 覆盖（参考 polaris-server `apiserver/httpserver/discover/v1/server.go::addBlockAllowAccess`） |
| `create_rule 返回非成功码 400201 existed resource` | 规则已被之前的运行创建 | 正常现象：`ensure_rule` 会用 `rule_exists_by_name` 提前探测；若仍触发，说明 path/policy 等 metadata 与新版本不一致，请去控制台删除该规则后重跑 |
| 用例 6-28 创建规则成功但请求结果不符 | SDK 缓存还没拉到 | 加大 `--rule-cache-wait`（默认 5s）；或检查 `consumer.localCache.serviceRefreshInterval` |
| 用例 8/9 期望 403 但拿到 200 | provider 上报的 Header key 与规则 key 大小写不一致（Go `http.Header` 自动 canonicalize 成 `User`） | 已修复：`provider/main.go::authenticate` 同时上报 canonical 与小写 key |
| 用例 11 期望 403 但拿到 200 | provider 没有透传 `X-Polaris-Caller-*` Header | 确认 main.go 解析逻辑（位于 `authenticate()` 函数） |
| 用例 12 期望 200 但拿到 403 | provider 未通过 `--register-metadata` 上报 `env=prod`，或 Engine 未挂钩 `RegisterLocalInstanceMetadata` | 检查 `pkg/flow/sync_flow.go::doSyncRegister` 成功分支是否已调用登记，provider 日志里 `registerRequest` 是否包含 `Metadata` |
| 用例 13 期望 403 但拿到 200 | 用例 12 的 env=prod 规则被错误地不带 `api.path` 限定，泄漏到了用例 13 的 path 上 | 确认服务端那条规则真带 `api.path=/echo-callee-prod`；可用 `GET ${RULE_API_PATH}?namespace=&service=&offset=0&limit=100` 列出后核对；老版本规则可能没有 path，需在控制台删除后由本脚本重建 |
| 用例 14 期望 200 但拿到 403 | provider 未识别 `X-Custom-Arg-<Key>` Header 或 canonical 大小写问题 | 检查 `provider/main.go::authenticate` 对 `X-Custom-Arg-` 前缀的处理 |
| 用例 16（REGEX）期望 403 但拿到 200 | 正则写错或未转义 `$`（shell 变量插值） | 规则 JSON 提交后用控制台核对原文；脚本内已 `\$` 转义；可改用 `^bad-foo\$` 完整 EXACT 风格做对比验证 |
| 用例 17/19（NOT_EQUALS / NOT_IN）期望 403 但拿到 200 | rule value 写成了空串或 `*` | `MatchString` 第一行 `IsMatchAll` 短路：value=`*`/`""` 时**任何输入都 return true**，NOT_EQUALS/NOT_IN 也不例外。请填具体值/集合 |
| 用例 20（RANGE）期望 403 但拿到 200 | RANGE 仅支持整数；请求 value 不是整数（"abc"/"1.5"/空串）会让 `ParseInt` 失败 → 该规则被视为不匹配 | 检查 `do_request -H "user: <value>"` 的 value 是否纯整数；若用 EXACT 改回比对再确认 |
| 用例 21/22（QUERY）结果异常 | provider 没把 `r.URL.Query()` 转成 `BuildQueryArgument` 上报 | 检查 `provider/main.go::authenticate` 中 `for k, vs := range r.URL.Query()` 段；规则 key 与 query key 大小写要一致（Go Query 不会 canonicalize） |
| 用例 23/24（CALLER_IP）结果异常 | 本机环境 `r.RemoteAddr` 是 `::1`（IPv6 优先），与规则 EXACT `127.0.0.1` 不匹配 | 规则已用 REGEX `^(127\.0\.0\.1\|::1)$` 兜底；若仍异常，在 provider 日志找 `[CALLER] incoming request: remoteAddr=...` 行确认实际 IP |
| 用例 25/26（CALLER_METADATA）期望 200/403 但拿到反向 | 请求未带 `X-Polaris-Caller-Service`，导致 provider 不构造 `SourceService`，CALLER_METADATA 永远取空 → 始终未命中 → 23 应得 200 却得 403 | 必须同时带 `X-Polaris-Caller-Service: app` + `X-Caller-Meta-tier: <value>`；脚本已就位，若手动重现请保留两个 Header |
| 用例 27/28（AND 关系）结果异常 | 25 漏带 query 或 26 多带了 query | 25 必须 `?region=cn` + Header `user: vip`；26 必须只带 Header 不带 query。AND 关系下任一参数缺失/不符即整体不命中 |
| 用例 3-5 期望 200 但拿到 403 | `${SERVICE_NAME}-norule` 下被人手工建了 `BlockAllowListRule` | 在控制台删除该 service 下所有规则后重跑 |
| 希望彻底清理规则 | 脚本不主动删除规则（避免依赖管理员 token） | 用 Polaris 控制台手工删除 `auth-it-allow-vip`/`auth-it-block-regex`/`auth-it-allow-query-cn`/`auth-it-allow-and-header-query` 等 18 条固定名规则；或调用 `POST /naming/v1/blockallow/rules/delete`（需 token） |

---

## 十、参考实现

- 鉴权流程编排：`pkg/flow/auth_flow.go::SyncAuthenticate`
- 黑白名单插件：`plugin/authenticator/blockallowlist/process.go::checkAllow / matchArguments / matchMethod`
- MatchString 6 种类型实现：`pkg/algorithm/match/match.go::MatchString`（EXACT/REGEX/NOT_EQUALS/IN/NOT_IN/RANGE）
- AuthAPI：`api/auth.go` + `api_auth.go`
- 单元测试：
  - `plugin/authenticator/blockallowlist/process_test.go`（25 用例）
  - `pkg/algorithm/match/match_test.go`（14 用例，覆盖 MatchString 6 种类型）
- polaris-java 对照实现：`polaris-plugins/polaris-plugins-auth/auth-block-allow-list/.../BlockAllowListAuthenticator.java`
