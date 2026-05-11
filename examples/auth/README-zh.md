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
# 推荐：传入 token，全部 9 条用例都跑（脚本会自动通过 OpenAPI 创建/删除规则）
./verify_auth.sh \
    --polaris-server 127.0.0.1 \
    --polaris-token <ADMIN_TOKEN>

# 服务端 OpenAPI 端点不同则覆盖：
./verify_auth.sh --polaris-server 127.0.0.1 --polaris-token <token> \
    --rule-api-path /naming/v1/blockallow/rules

# 仅跑用例 1-3（不依赖 token / OpenAPI）：
./verify_auth.sh --polaris-server 127.0.0.1
# （未传 token 时用例 4-9 会因创建规则失败而 SKIP，不影响 1-3 跑通）
```

> 脚本工作机制：每个用例独立运行，先清理本服务下所有规则，再创建本用例需要的规则，等待 SDK 缓存刷新后发请求；最后通过 `trap` 兜底再清理一遍。详见 [`test.md`](./test.md) 第四节。


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
| `CUSTOM` | 优先 Custom Argument，其次 SourceService.Metadata | 由业务代码 `AddArgument` 决定 |

> 取值规则与 polaris-java 的 `MetadataContainer.getHeader/getQuery/...` 一一对应；后端 `BlockAllowConfig.MatchArgument` 来自 `specification@v1.8.0+` 的 7 类 enum。

---

## 5. 进一步阅读

- 实现总览：`pkg/plugin/authenticator/`、`plugin/authenticator/blockallowlist/`
- 流程编排：`pkg/flow/auth_flow.go`、`pkg/flow/impl.go`（Engine.authenticators 字段）
- API 入口：`api_auth.go`、`api/auth.go`
- 单元测试：`plugin/authenticator/blockallowlist/process_test.go`（9 镜像 polaris-java + 12 个扩展用例）
- 测试方案：[`test.md`](./test.md)
- 父级 README：[`../README.md`](../README.md)
