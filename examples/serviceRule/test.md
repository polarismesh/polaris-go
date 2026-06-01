# serviceRule Demo E2E 验证指南

## 概述

本目录下的 demo 与 verify.sh 脚本用于端到端验证 `ConsumerAPI` 的 **7 类规则查询接口**：

| 接口 | demo 路由 | 对应 OpenAPI 端点 |
| --- | --- | --- |
| `GetRouteRule` | `GET /route` | `GET /naming/v2/routings` |
| `GetCircuitBreakerRule` | `GET /circuitbreaker` | `GET /naming/v1/circuitbreaker/rules` |
| `GetRateLimitRule` | `GET /ratelimit` | `GET /naming/v1/ratelimits` |
| `GetNearbyRouteRule` | `GET /nearbyroute` | `GET /naming/v2/routings` (NearbyPolicy) |
| `GetLosslessRule` | `GET /lossless` | `GET /naming/v1/lossless/rules` |
| `GetBlockAllowRule` | `GET /auth` | `GET /naming/v1/blockallow/rules` |
| `GetLane` | `GET /lane` | `GET /naming/v1/lane/groups` |

同时包含 `*pb.BehaviorNotRegisteredError` 修复的专项验证（当限流规则 `action=tsf` 时，
SDK 日志降级为 WARN 而非 ERROR）。

---

## 前置条件

1. **北极星服务端**已部署并可达（需同时开放 gRPC 8091 与 HTTP OpenAPI 8090 端口）
2. **鉴权 Token** 具备规则的读写权限（建议管理员 token）
3. 本机已安装：`go` (≥1.18)、`curl`、`python3`、`diff`、`bash`
4. 如需验证 tsf 修复的 SDK 日志级别，建议带 `--debug` 参数（让 SDK 日志输出到 demo stdout）

---

## 快速开始

```bash
cd examples/serviceRule

# 最简运行（使用默认 127.0.0.1 服务端）
./verify.sh --polaris-token YOUR_TOKEN

# 指定远程服务端 + 自定义服务名
./verify.sh \
  --polaris-server 10.0.0.1 \
  --polaris-token YOUR_TOKEN \
  --service my-service \
  --namespace production \
  --debug
```

---

## 命令行参数

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `--polaris-server` | `127.0.0.1` | 北极星服务端地址（不含端口） |
| `--polaris-token` | _(空)_ | 鉴权 Token |
| `--polaris-grpc-port` | `8091` | gRPC 端口（demo 连接用） |
| `--polaris-http-port` | `8090` | OpenAPI HTTP 端口（脚本直查用） |
| `--service` | `provider-demo` | 目标服务名 |
| `--namespace` | `default` | 命名空间 |
| `--demo-port` | `38080` | demo HTTP 监听端口 |
| `--rule-cache-wait` | `8` | 创建规则后等待 SDK 缓存刷新的秒数 |
| `--skip-tsf-rule` | _(不跳过)_ | 跳过 `action=tsf` 规则的创建与验证 |
| `--debug` | _(关闭)_ | 开启 demo 与 OpenAPI 的详细日志 |

---

## 验证策略

### 全字段精确对比

脚本对 demo 返回与 OpenAPI 直查结果做**规范化后逐字段 diff**：

1. **提取规则列表**：从 demo 的 `rule.Value` 与 OpenAPI 的响应体中分别提取规则数组
2. **归一化**：
   - unwrap proto 的 `{value: x}` wrapper → 纯值 `x`
   - 丢弃易变字段（`ctime`, `mtime`, `id`, `revision`, `metadata`, `creator` 等）
   - 只保留脚本自管理的规则（`name` 以 `serviceRule-it-` 开头）
3. **排序**：按 `name` 字典序
4. **diff**：JSON 字符串级别对比；不一致时写入 `.logs/diff-<kind>.txt`

### 规则幂等管理

- 固定规则名（`serviceRule-it-<type>-<service>`），跨运行复用
- 「检查-存在则复用-否则创建」：不删除规则，不破坏环境
- 脚本退出只杀 demo 进程，不清理服务端数据

---

## BehaviorNotRegisteredError 修复验证

### 背景

当服务端下发限流规则中 `action` 字段引用了客户端未注册的 RateLimiter 插件（如 `tsf`）时：

- **修复前**：`plugin/localregistry/inmemory/inmemory.go:783` 以 **ERROR** 级别记录
- **修复后**：同位置以 **WARN** 级别记录，错误类型为 `*pb.BehaviorNotRegisteredError`

### 验证方式

脚本会自动创建一条 `action=tsf` 的限流规则（名为 `serviceRule-it-rl-tsf-<service>`），然后：

1. **HTTP 响应验证**：
   ```json
   {
     "validateError": {
       "message": "behavior plugin tsf not registered",
       "isBehaviorNotRegistered": true,
       "unregisteredBehavior": "tsf"
     }
   }
   ```
   脚本断言 `isBehaviorNotRegistered == true` 且 `unregisteredBehavior == "tsf"`

2. **SDK 日志验证**（需 `--debug` 参数）：
   - ✅ 出现 WARN 关键字：`references unregistered behavior plugin`
   - ❌ 不出现旧版 ERROR 文案：`fail to validate service rule.*behavior plugin .* not registered`

### 手动复现

```bash
# 1. 启动 demo
cd examples/serviceRule && go build -o bin . && ./bin \
  -polaris-server-address 127.0.0.1:8091 \
  -polaris-token YOUR_TOKEN \
  -service provider-demo \
  -debug

# 2. 确保服务端有 action=tsf 的限流规则（脚本会自动创建，也可手动）

# 3. 查询限流规则
curl -s http://127.0.0.1:38080/ratelimit \
  -H 'Content-Type: application/json' \
  -d '{"namespace":"default","service":"provider-demo"}' | python3 -m json.tool

# 4. 预期输出中含：
#    "validateError": {
#      "isBehaviorNotRegistered": true,
#      "unregisteredBehavior": "tsf"
#    }

# 5. 同时观察 demo stdout：
#    应有 "[RateLimit] [VERIFY-FIX] 命中 *pb.BehaviorNotRegisteredError"
#    SDK 日志中 base 模块应为 WARN（不再是 ERROR）
```

---

## 清理

测试完成后可通过 `cleanup.sh` 清理残留进程和构建产物：

```bash
# 交互模式（先展示后确认）
./cleanup.sh

# 强制模式（直接清理，适合 CI）
./cleanup.sh -f

# 仅展示匹配的进程和目录（不清理）
./cleanup.sh --dry-run
```

清理范围：
- 残留的 demo 进程（匹配 `examples/serviceRule` 路径下的二进制）
- `.build/` 目录（编译产物）
- `.logs/` 目录（运行日志）
- `bin`、`x86-bin`（Makefile 产物）
- `polaris/`（SDK 本地缓存目录）

> 注意：cleanup.sh **不会删除服务端上的规则**。脚本创建的规则（`serviceRule-it-*`）
> 会跨运行复用，如需清理请到 Polaris 控制台手动删除。

---

## 输出文件

| 文件 | 说明 |
| --- | --- |
| `.build/serviceRule` | 编译后的 demo 二进制 |
| `.logs/demo.log` | demo 运行日志（含 SDK 日志） |
| `.logs/verify-YYYYMMDD_HHMMSS.log` | 本次测试执行日志 |
| `.logs/diff-<kind>.txt` | 对比失败时的详细 diff（仅失败时生成） |

---

## 常见问题

### 规则创建报 401/403

Token 权限不足，请使用管理员 Token 或确认 Token 对目标 namespace 有规则 CRUD 权限。

### diff 报不一致但规则确实存在

1. **SDK 缓存未刷新**：增大 `--rule-cache-wait`（默认 8 秒）
2. **存在非脚本管理的规则**：本脚本只对比 `name` 以 `serviceRule-it-` 开头的规则；
   如果控制台有其他规则，不影响对比结果
3. **proto JSON 序列化差异**：不同版本的 jsonpb 可能对 wrapper 类型输出不一致；
   归一化逻辑已尽力覆盖，如仍有差异请检查 `.logs/diff-<kind>.txt` 中的具体字段

### demo 启动失败

- 检查 demo 端口 38080 是否被占用
- 检查 gRPC 8091 端口是否可达
- 查看 `.logs/demo.log` 中的具体错误

### action=tsf 验证未通过

- 确认本地 polaris-go 代码包含 `BehaviorNotRegisteredError` 修复（`go.mod` 中有 `replace` 指向 `../../`）
- 确认 tsf 规则已成功创建到服务端（通过 OpenAPI 确认）
- 加 `--debug` 重跑，查看完整 SDK 日志输出
