# examples/auth —— 服务鉴权（BlockAllowList Authenticator）示例

本示例演示 polaris-go 的 **服务鉴权（Authenticator）** 能力，重点是基于 `BlockAllowListRule`（黑白名单）的被调侧鉴权拦截。

| 内容 | 路径 |
| --- | --- |
| Provider 源码（接入 AuthAPI） | [`provider/main.go`](./provider/main.go) |
| Consumer 源码（普通调用方） | [`consumer/main.go`](./consumer/main.go) |
| 默认鉴权配置（`provider.auth`） | [`provider/polaris.yaml`](./provider/polaris.yaml) |
| 端到端验证脚本 | [`verify_auth.sh`](./verify_auth.sh) |
| 测试方案文档 | [`test.md`](./test.md) |
| 清理脚本（进程 + 日志/build 目录） | [`cleanup.sh`](./cleanup.sh) |

---

## 1. 关键 API 用法

```go
// 1) 创建 AuthAPI（推荐复用 ProviderAPI 的 SDKContext）
provider, _ := polaris.NewProviderAPI()
authAPI := polaris.NewAuthAPIByContext(provider.SDKContext())
defer authAPI.Destroy()

// 2) 在每个请求前发起鉴权
req := &polaris.AuthenticateRequest{}
req.Namespace = "default"
req.Service   = "AuthEchoServer"
req.Method    = r.Method
req.Path      = r.URL.Path
req.Protocol  = "HTTP"
// 可选：透传主调标识（用于 CALLER_SERVICE / CALLER_METADATA 维度）
req.SourceService = &model.ServiceInfo{
    Namespace: "default",
    Service:   "trusted",
    Metadata:  map[string]string{"env": "prod"},
}
// 透传流量标签（用于 HEADER / QUERY / COOKIE / CALLER_IP 等维度）
req.AddArgument(model.BuildHeaderArgument("user", "vip"))
req.AddArgument(model.BuildCallerIPArgument("127.0.0.1"))

resp, err := authAPI.Authenticate(req)
if err != nil { /* 处理异常 */ }
if !resp.IsAllowed() {
    // 鉴权拒绝：resp.GetCode() == model.AuthResultForbidden
    // resp.GetInfo() 给出拒绝原因（"blocked by block-allow-list rule"）
}
```

`provider/main.go` 在 `/echo` 入口里把上述模板做成了一个完整的拦截器（[main.go:78-127](./provider/main.go)），并提供 `/auth-info` 调试接口返回当前鉴权配置摘要。

---

## 2. polaris.yaml 关键配置

```yaml
provider:
  auth:
    # 默认 false。设为 false 时 SyncAuthenticate 直接返回 Ok，零开销。
    enable: true
    # 鉴权插件链：按顺序执行，任一返回 Forbidden 即短路拒绝。
    chain:
      - blockAllowList
    # 各鉴权插件的具体配置（blockAllowList 当前无可调参数）
    plugin:
      blockAllowList: {}
```

> 三种"等价放行"配置：
> 1. `enable: false`
> 2. `enable: true` + `chain: []`
> 3. `enable: true` + 控制台未配置 `BlockAllowListRule`（按通过处理，与 polaris-java 一致）

---

## 3. 快速开始

### 3.1 单独运行 provider

```bash
cd provider
go build -o ./bin/provider .
POLARIS_SERVER=127.0.0.1 ./bin/provider --namespace default --service AuthEchoServer --port 0
# 另一个终端用 curl 测试
curl -i http://127.0.0.1:<port>/echo
curl -s http://127.0.0.1:<port>/auth-info | jq .
```

### 3.2 端到端验证（自动建规则模式）

```bash
# 推荐：传入 token，全部 18 条用例都跑（脚本会自动通过 OpenAPI 创建并复用规则）
./verify_auth.sh \
    --polaris-server 127.0.0.1 \
    --polaris-token <ADMIN_TOKEN>

# 服务端 OpenAPI 端点不同则覆盖：
./verify_auth.sh --polaris-server 127.0.0.1 --polaris-token <token> \
    --rule-api-path /naming/v1/blockallow/rules

# 仅跑用例 1-3（不依赖 token / OpenAPI）：
./verify_auth.sh --polaris-server 127.0.0.1
# （未传 token 时用例 4-18 会因创建规则失败而 SKIP，不影响 1-3 跑通）
```

> 脚本工作机制（服务/规则复用 + path 隔离）：
> - **服务名固定**：用例 1-3 用 `${SERVICE_NAME}-norule`（不建规则），用例 4-18 用 `${SERVICE_NAME}`（12 条规则常驻：4-13 业务规则 + 14-18 MatchString 类型覆盖）；不带 RUN_ID，跨运行复用，不会不断生成新 service。
> - **规则名固定**：7 条 `auth-it-*`，首次跑由 `ensure_rule` 创建，之后复用，跨运行幂等。
> - **path 隔离**：每条规则带 EXACT `api.path` 限定（如 `/echo-allow-vip`），同 service 下多条规则按 path 互不干扰，无需删除/更新。
> - **provider 路由**：`http.HandleFunc("/", echoHandler)` 作为兜底，`/echo` 与所有 `/echo-*` 路径都走鉴权拦截。
>
> 详见 [`test.md`](./test.md) 第四节。


### 3.3 清理

```bash
./cleanup.sh        # 询问后清理
./cleanup.sh -f     # 强制清理
./cleanup.sh --dry-run  # 仅展示
```

---

## 4. 8 维流量标签的取值约定

`provider/main.go` 在构造 `AuthenticateRequest` 时按以下规则装填 `Arguments`：

| MatchArgument 类型 | 取值方式 | 示例 |
| --- | --- | --- |
| `HEADER` | HTTP Header | `curl -H "user: vip" /echo` |
| `QUERY` | URL Query | `curl /echo?env=prod` |
| `COOKIE` | HTTP Cookie | `curl -b "user=vip" /echo` |
| `CALLER_IP` | TCP 连接 RemoteAddr | curl 自身 IP |
| `CALLER_SERVICE` | `X-Polaris-Caller-Namespace`/`Service` Header | `curl -H "X-Polaris-Caller-Service: trusted"` |
| `CALLER_METADATA` | `X-Caller-Meta-<Key>: <Value>` Header | `curl -H "X-Caller-Meta-Env: prod"` |
| `CALLEE_METADATA` | polaris-go SDK 本端通过 `RegisterInstance` 登记的实例 metadata（由 Engine 在注册成功时缓存，反注册时清理） | 示例 provider 通过 `--register-metadata env=prod` 启动 |
| `CUSTOM` | 优先 Custom Argument（示例 provider 约定 `X-Custom-Arg-<Key>: <Value>` Header），其次 SourceService.Metadata | `curl -H "X-Custom-Arg-biz: v1" /echo` |

> 取值规则与 polaris-java 的 `MetadataContainer.getHeader/getQuery/...` 一一对应；后端 `BlockAllowConfig.MatchArgument` 来自 `specification@v1.8.0+` 的 7 类 enum。

### 4.1 MatchString 6 种类型

规则中 `api.path` 与 `arguments[].value` 都是 `MatchString` 类型，由 `pkg/algorithm/match/match.go::MatchString` 解释。spec 共定义 **6 种** `type`：

| type | 语义 | rule value 写法 | 示例 |
| --- | --- | --- | --- |
| `EXACT` | 全等 | 任意字符串 | `value="vip"`，请求 `vip` 命中 |
| `REGEX` | 正则匹配（PCRE，由 `regexp2`） | 正则表达式 | `value="^bad-.*$"`，请求 `bad-foo` 命中 |
| `NOT_EQUALS` | 不等于 | 非空非 `*` 字符串 | `value="admin"`，请求 `guest` 命中 |
| `IN` | 在集合中 | `,` 分隔的列表 | `value="bad1,bad2"`，请求 `bad1` 命中 |
| `NOT_IN` | 不在集合中 | `,` 分隔的列表 | `value="admin,operator"`，请求 `guest` 命中 |
| `RANGE` | 整数区间 `[left,right]` | `~` 分隔的两个整数 | `value="100~200"`，请求 `150` 命中 |

> **`IsMatchAll` 短路坑**：`MatchString` 第一行 `if rule_value=="" || rule_value=="*" { return true }`——意味着 `NOT_EQUALS`/`NOT_IN` 写 `*` 或空串时会被无条件放行（不是"不等于通配符 → 全部命中"）。需要"否定语义"时 value 必须写明具体值/集合。
>
> **RANGE 仅整数**：浮点、字符串、空串等都会让 `ParseInt` 失败 → 该规则视为不匹配。

`verify_auth.sh` 用例 4-13 覆盖 `EXACT`，用例 14-18 覆盖其余 5 种（HEADER 维度，BLOCK_LIST + 命中场景）。底层 `MatchString` 完整单测在 `pkg/algorithm/match/match_test.go`（共 14 用例）。

---

## 5. 进一步阅读

- 实现总览：`pkg/plugin/authenticator/`、`plugin/authenticator/blockallowlist/`
- 流程编排：`pkg/flow/auth_flow.go`、`pkg/flow/impl.go`（Engine.authenticators 字段）
- API 入口：`api_auth.go`、`api/auth.go`
- 单元测试：`plugin/authenticator/blockallowlist/process_test.go`（9 镜像 polaris-java + 16 个扩展用例，含 CALLEE_METADATA / CUSTOM 等维度）
- 测试方案：[`test.md`](./test.md)
- 父级 README：[`../README.md`](../README.md)
