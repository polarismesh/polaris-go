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
| 主调标识透传方式 | HTTP Header：`X-Polaris-Caller-Namespace`、`X-Polaris-Caller-Service`、`X-Caller-Meta-<Key>` |
| 调用接口 | `GET /echo`：业务接口（鉴权拦截）；`GET /auth-info`：调试接口（不鉴权） |
| 鉴权放行响应 | `HTTP 200`，`X-Auth-Result: ok` |
| 鉴权拒绝响应 | `HTTP 403`，`X-Auth-Result: forbidden`、`X-Auth-Info: <reason>` |
| 规则 OpenAPI 端点 | `${POLARIS_HTTP_ADDR}${RULE_API_PATH}`（默认 `/naming/v1/blockallow/rules`） |
| 规则缓存等待 | `--rule-cache-wait` 秒（默认 5s），用于 SDK LocalRegistry 拉到最新规则 |

通用流程：`构建 provider → 启动（按用例切换 polaris.yaml） → 创建规则 → 等待缓存 → curl → 校验 HTTP + X-Auth-Result → 删除规则`

---

## 二、测试目标

验证 polaris-go SDK 的服务鉴权链路：

1. `provider.auth.enable=false` 或 `chain=[]` 时直接放行（**零开销路径**）；
2. `enable=true` 但控制台无 `BlockAllowListRule` 时按通过处理（与 polaris-java 一致）；
3. 控制台配置规则后，按 `checkAllow` 9 条语义命中放行/拒绝；
4. `HEADER` / `CALLER_SERVICE` 两个常用维度的 EXACT 匹配。

---

## 三、拓扑与端口

```
   curl ──► provider:${PROVIDER_PORT}/echo
              │
              │  Authenticate(req: AuthenticateRequest)
              ▼
       polaris-go AuthAPI
              │  按 chain 顺序：blockAllowList
              ▼
       LocalRegistry (BlockAllowRule from Polaris Server)
              ▲
              │  Polaris OpenAPI ${RULE_API_PATH}
              │  POST  → 创建规则
              │  POST /delete → 删除规则
              │  GET   → 列举规则
        verify_auth.sh
```

`provider/main.go` 在每个 `/echo` 请求里：
1. 把 `Method` / `Path` / `Protocol` / Header / Query / Cookie / RemoteIP 全部装进 `AuthenticateRequest`；
2. 解析 Header `X-Polaris-Caller-Namespace`、`X-Polaris-Caller-Service` 构造 `SourceService`；
3. 解析 Header `X-Caller-Meta-<Key>: <Value>` 构造 `SourceService.Metadata`；
4. 调用 `AuthAPI.Authenticate(req)` —— 命中规则时返回 403 + `X-Auth-Info`；放行时返回 200。

---

## 四、自动化建规则机制

**设计原则：不主动删除规则**。脚本调用 Polaris OpenAPI 「先检查同名规则是否存在 → 不存在则创建、存在则复用」，退出时只关停 provider。这样即使服务端对删除操作有权限限制，脚本也能完整跑完所有用例。

为避免同服务下多条残留规则的 `checkAllow` 混合语义干扰用例，**用例 1-9 按功能分 4 组，每组使用独立的被调 service 名**（名字带 `RUN_ID` 后缀，每次跑都是新 service 名）：

| 用例组 | 被调服务名变量 | 模板 | 说明 |
| --- | --- | --- | --- |
| 用例 1/2/3 | `SERVICE_GROUP_NORULE` | `${SERVICE_NAME}-grp0-<RUN_ID>` | 鉴权未启用 / 无规则，不创建规则 |
| 用例 4/5 | `SERVICE_GROUP_HEADER_ALLOW` | `${SERVICE_NAME}-grpA-<RUN_ID>` | 共用一条白名单规则 `Header user=vip` |
| 用例 6/7 | `SERVICE_GROUP_HEADER_BLOCK` | `${SERVICE_NAME}-grpB-<RUN_ID>` | 共用一条黑名单规则 `Header user=bad` |
| 用例 8/9 | `SERVICE_GROUP_CALLER_ALLOW` | `${SERVICE_NAME}-grpC-<RUN_ID>` | 共用一条白名单规则 `CALLER_SERVICE default/trusted` |

脚本提供以下 OpenAPI 调用封装：

| 函数 | 作用 | 对应 OpenAPI |
| --- | --- | --- |
| `probe_open_api` | 启动时探测端点是否可达，否则告警提示用 `--rule-api-path` 覆盖 | `GET ${RULE_API_PATH}?namespace=&service=&offset=0&limit=1` |
| `rule_exists_by_name <name>` | 精确按 name 查询，返回 0=存在 / 1=不存在 | `GET ${RULE_API_PATH}?namespace=&service=&name=&offset=0&limit=1`（通过 `"amount": N` 判断） |
| `create_rule <rule_json>` | 创建规则，body 为 `[<rule_json>]` 数组 | `POST ${RULE_API_PATH}` |
| `ensure_rule <name> <rule_json>` | 同名规则存在则复用，不存在则 `create_rule` | 组合 `rule_exists_by_name` + `create_rule` |
| `list_rules_for_service`（仅调试） | 列举本服务下所有规则 id | `GET ${RULE_API_PATH}?namespace=&service=&offset=0&limit=100` |
| `rule_with_header_match` | 构造一条 Header EXACT 匹配的规则（白/黑名单） | — |
| `rule_with_caller_service_match` | 构造一条 CALLER_SERVICE EXACT 匹配的规则（白/黑名单） | — |

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
> 本脚本只使用 **GET（列举/查询）+ POST（创建）**，不使用删除/更新；因此即便不传 `--polaris-token`，也能完整跑完 9 个用例（服务端对匿名 GET/POST 通常放行）。
>
> 若服务端实际端点不同，用 `--rule-api-path <路径>` 覆盖即可。
>
> **每用例独立运行**：用例 4-9 严格按"切换到组内独立 service → ensure_rule（检查不存在才创建）→ 重启 provider（重置 SDK LocalRegistry）→ 等待 SDK 拉缓存 → 发请求"流程。**用例 4+5 / 6+7 / 8+9 共用同一条规则**，只是改请求参数来测命中/不命中，无需每个用例都建新规则。
>
> **退出清理**：`trap cleanup EXIT` 只关停 provider，不删除规则；历史规则保留在服务端不影响下次跑测（因为下次 RUN_ID 不同，service 名也变）。

---

## 五、用例矩阵

下表中"规则内容"列同时给出 **JSON 体 + 产生的效果**。每个用例运行前脚本会先清理本服务下所有规则，再单独创建本用例需要的规则。

| 编号 | 鉴权配置 | 创建的规则 | 请求条件 | 预期 | 产生的效果 |
| --- | --- | --- | --- | --- | --- |
| **用例 1** | `enable=false` | — | `GET /echo` | `200`，`ok` | 鉴权链未加载，`SyncAuthenticate` 直接返回 Ok（零开销路径） |
| **用例 2** | `enable=true`, `chain=[blockAllowList]` | 无规则 | `GET /echo` | `200`，`ok` | 鉴权链加载，但 `getBlockAllowListRules` 返回空，按通过处理 |
| **用例 3** | `enable=true`, `chain=[]` | 无规则 | `GET /echo` | `200`，`ok` | `loadAuthenticators` 因 `chain` 为空直接 return，`authenticators` 切片仍为空，等同未加载 |
| **用例 4** | `enable=true` | `auth-it-allow-vip` ALLOW_LIST，`HEADER user EXACT vip` | `GET /echo` Header `user: vip` | `200`，`ok` | 仅一条白名单规则。请求 Header 命中 → `containsAllowList=true` 且 `matchArguments=true` → 返回 ALLOW |
| **用例 5** | `enable=true` | 同上 `auth-it-allow-vip` | `GET /echo`（不带 user） | `403`，`forbidden` | 同上规则，请求未命中 → 循环结束后 `containsAllowList=true` 触发 `return !containsAllowList = false` → 拒绝 |
| **用例 6** | `enable=true` | `auth-it-block-bad` BLOCK_LIST，`HEADER user EXACT bad` | `GET /echo` Header `user: bad` | `403`，`forbidden` | 仅一条黑名单规则。请求 Header 命中 → 直接返回 `BlockAllowPolicy != ALLOW_LIST` → 拒绝 |
| **用例 7** | `enable=true` | 同上 `auth-it-block-bad` | `GET /echo` Header `user: normal` | `200`，`ok` | 同上规则，请求未命中 → 循环结束后 `containsAllowList=false` 触发 `return !containsAllowList = true` → 通过 |
| **用例 8** | `enable=true` | `auth-it-allow-trusted` ALLOW_LIST，`CALLER_SERVICE key=default value EXACT trusted` | `GET /echo` Header `X-Polaris-Caller-Namespace: default` + `X-Polaris-Caller-Service: trusted` | `200`，`ok` | provider 解析 `X-Polaris-Caller-*` 构造 `SourceService`；`getLabelValue(CALLER_SERVICE)` 返回 `trusted`；EXACT 匹配命中 |
| **用例 9** | `enable=true` | 同上 `auth-it-allow-trusted` | `GET /echo` Header `X-Polaris-Caller-Service: other` | `403`，`forbidden` | `getLabelValue(CALLER_SERVICE)` 返回 `other`，不等于 `trusted`；白名单不命中 → 拒绝 |

> **`checkAllow` 三段式语义**（参考 polaris-java，本 SDK 实现位于 `plugin/authenticator/blockallowlist/process.go::checkAllow`）：
> 1. **全部为白名单**：任一匹配即通过；都不匹配则拒绝（`containsAllowList=true → return false`）；
> 2. **全部为黑名单**：任一匹配即拒绝；都不匹配则通过；
> 3. **混合**：任一白名单匹配即通过；都不匹配且存在白名单则拒绝。
>
> 用例 4-9 都只创建**单条规则**，对应"全部为白名单"或"全部为黑名单"的简单分支；混合分支由单元测试 `process_test.go` 中的用例 7/8/9 覆盖。

---

## 六、规则 JSON 模板

> **字段名采用 `specification` proto 中明确指定的 `json_name`，全部为 snake_case**（如 `block_allow_config` / `block_allow_policy` / `value_type`）。如果你手工拼写请勿用驼峰 `blockAllowConfig`，polaris-server 的 `jsonpb.UnmarshalNext` 默认严格校验，未知字段会被拒绝。

`rule_with_header_match`：
```json
{
  "name": "<rule-name>",
  "namespace": "default",
  "service": "AuthEchoServer",
  "enable": true,
  "block_allow_config": [{
    "block_allow_policy": "ALLOW_LIST",        // 或 BLOCK_LIST
    "arguments": [{
      "type": "HEADER",
      "key": "user",
      "value": {"type": "EXACT", "value_type": "TEXT", "value": "vip"}
    }]
  }]
}
```

`rule_with_caller_service_match`：
```json
{
  "name": "<rule-name>",
  "namespace": "default",
  "service": "AuthEchoServer",
  "enable": true,
  "block_allow_config": [{
    "block_allow_policy": "ALLOW_LIST",
    "arguments": [{
      "type": "CALLER_SERVICE",
      "key": "default",                       // 主调命名空间，"*" 表示匹配所有
      "value": {"type": "EXACT", "value_type": "TEXT", "value": "trusted"}
    }]
  }]
}
```

POST 提交时 body 为 `[<rule_json>]` 数组，与北极星 OpenAPI 批量 API 习惯一致。

---

## 七、执行与结果

```bash
# 推荐：传入 token 启用全部 9 条用例
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
1. 杀掉残留的 `auth_provider` / `auth_consumer` 进程；
2. 询问后清理 `./.build` 与 `./.logs` 目录。

> verify_auth.sh 自身已通过 `trap` 兜底删除规则，所以 `cleanup.sh` 不再额外调用 OpenAPI；如担心残留，重跑一次 `verify_auth.sh` 即可（首步会再清理）。

---

## 九、常见问题排查

| 症状 | 可能原因 | 处置 |
| --- | --- | --- |
| `Provider 启动失败` | `polaris.yaml` 中 `serverConnector.addresses` 不可达 | 检查 `--polaris-server` 与 8091 端口连通性 |
| OpenAPI 探测告警 HTTP=404 | 服务端实际端点不同 | 用 `--rule-api-path` 覆盖（参考 polaris-server `apiserver/httpserver/discover/v1/server.go::addBlockAllowAccess`） |
| `create_rule 返回非成功码 400201 existed resource` | 规则已由本次脚本的前一个用例创建 | 正常现象：`ensure_rule` 会先用 `rule_exists_by_name` 探测；若还触发，说明历史残留同名规则（极罕见，因 service 名带 `RUN_ID` 后缀） |
| 用例 4-9 创建规则成功但请求结果不符 | SDK 缓存还没拉到 | 加大 `--rule-cache-wait`（默认 5s）；或检查 `consumer.localCache.serviceRefreshInterval` |
| 用例 6/7 期望 403 但拿到 200 | provider 上报的 Header key 与规则 key 大小写不一致（Go `http.Header` 自动 canonicalize 成 `User`） | 已修复：`provider/main.go::authenticate` 同时上报 canonical 与小写 key |
| 用例 9 期望 403 但拿到 200 | provider 没有透传 `X-Polaris-Caller-*` Header | 确认 main.go 解析逻辑（位于 `authenticate()` 函数） |
| 希望退出后彻底清理规则 | 脚本不主动删除规则（避免依赖管理员 token） | 用 Polaris 控制台手工删除 `auth-it-<RUN_ID>-*` 规则；或调用 `POST /naming/v1/blockallow/rules/delete`（需 token） |

---

## 十、参考实现

- 鉴权流程编排：`pkg/flow/auth_flow.go::SyncAuthenticate`
- 黑白名单插件：`plugin/authenticator/blockallowlist/process.go::checkAllow / matchArguments / matchMethod`
- AuthAPI：`api/auth.go` + `api_auth.go`
- 单元测试（21 用例）：`plugin/authenticator/blockallowlist/process_test.go`
- polaris-java 对照实现：`polaris-plugins/polaris-plugins-auth/auth-block-allow-list/.../BlockAllowListAuthenticator.java`
