# 配置生效查询验证示例

验证 polaris-go 客户端通过 `WatchClientEvents` gRPC 双向流响应服务端「配置生效查询」的端到端能力。

## 背景

服务端可通过 maintain 接口 `GET /maintain/v1/clients/event` 向指定客户端下发一条配置生效查询（PUSH），客户端经 `WatchClientEvents` 长连接回 ACK（含本地生效配置的 version/md5/是否已应用），服务端原样透传给查询方。本示例验证该链路在 polaris-go 客户端的实现正确性。

## 验证原理

```
                ┌─────────────────┐   ReportClient(clientID)    ┌──────────────┐
                │  config-effect  │ ──────────────────────────► │              │
                │     客户端      │                             │              │
                │  (polaris-go)   │   WatchClientEvents(WATCH)  │              │
                │                 │ ◄──────────────────────────► │  Polaris     │
                │  订阅配置文件    │   PUSH ──────────────────►  │  服务端      │
                │                 │   ◄──────────────── ACK     │  (商业版)    │
                └────────┬────────┘                             └──────┬───────┘
                         │ /config /clientid                          │
                         ▼                                            ▼
                ┌──────────────────────────────────────────────────────────┐
                │              config-effect-test.sh                       │
                │  1. 读客户端 /clientid 与 /config (version/md5)          │
                │  2. 调服务端 maintain 接口 PUSH 配置生效查询              │
                │  3. 解析返回的 ACK content                              │
                │  4. 断言 applied=true 且 version/md5 与客户端一致        │
                └──────────────────────────────────────────────────────────┘
```

## 加密配置（第 1 个文件）

派生的第 1 个文件（`-1.yaml`）作为**加密配置**参与验证，覆盖「密文下发 → SDK crypto filter 解密 → 明文生效 → ACK 应答」的完整链路。

- **创建方式**：polaris-go SDK 的 `CreateConfigFile`/`UpdateConfigFile` 不携带 `Encrypted`/`Tags`（`transferToConfigFile` 仅映射 `namespace/group/name/content`），无法创建加密配置。因此脚本改用服务端 console HTTP 接口 `POST /config/v1/configfiles`（body 带 `encrypted:true, encrypt_algo:"AES"`）创建，再 `POST /config/v1/configfiles/release` 发布。
- **客户端解密**：crypto/aes filter 默认启用（非 agent 模式），`run` 模式订阅到加密的 `-1.yaml` 会自动解密，`GetContent()` 返回明文。
- **一致性**：生效查询校验只比对 `applied/version/md5`。ACK 回带的 `content` 为**源内容（密文）**、`md5` 为源内容摘要，与客户端 `/config` 快照的 `md5` 同源（同为服务端密文摘要），因此加密与非加密文件的校验逻辑一致，脚本无需特判。

## 前置条件

1. 北极星服务端（Polaris Server **商业版**）已启动，且已更新含 `WatchClientEvents` 逻辑的版本
2. Go 环境已安装
3. 服务端 maintain HTTP 端口可达（默认 `8090`，可用 `--maintain-port` 指定）
4. 配置文件组 `polaris-config-example`（默认）已存在，或客户端有创建权限
5. 服务端 console 配置接口（`/config/v1/configfiles`，与 maintain 同端口）可达，且 `--polaris-token` 具备配置写权限（用于创建/发布加密的第 1 个文件）

## 使用方法

```bash
chmod +x config-effect-test.sh

# 基本用法（服务端在本机、maintain 端口 8090）
./config-effect-test.sh

# 指定服务端地址与鉴权 token
./config-effect-test.sh --polaris-server 10.0.0.1 --polaris-token <token>

# 指定 maintain 端口与配置文件 base name
./config-effect-test.sh --maintain-port 8090 --namespace default \
  --group polaris-config-example --file config-effect-example

# 启用 SDK debug 日志排查
./config-effect-test.sh --debug
```

### 参数说明

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `--polaris-server` | `127.0.0.1` | 北极星服务端地址 |
| `--polaris-token` | 空 | 北极星鉴权令牌 |
| `--maintain-port` | `8090` | 服务端 maintain HTTP 端口 |
| `--namespace` | `default` | 命名空间 |
| `--group` | `polaris-config-example` | 配置文件组 |
| `--file` | `config-effect-example` | 配置文件 base name，派生 -1/-2/-3.yaml |
| `--port` | `18091` | 客户端 HTTP 观察端口 |
| `--debug` | 关 | 启用 SDK debug 日志 |

## 验证用例

| 编号 | 用例 | 期望 |
|------|------|------|
| 0 | 客户端拉取 3 份基线配置 | `/config` files 数组中 3 个文件 version/md5 非空 |
| 1 | 获取 clientID | 客户端 `/clientid` 非空 |
| 2.x.1 | ACK applied=true | 每个配置文件 applied=true (x=1/2/3) |
| 2.x.2 | ACK version 一致 | 每个文件 ACK version == 客户端本地 version |
| 2.x.3 | ACK md5 一致 | 每个文件 ACK md5 == 客户端本地 md5 |
| 3 | 加密配置解密一致 | 第 1 个文件（加密）解密后 content == 明文基线 `effect-content-v1` |

## 客户端 HTTP 接口

客户端 `run` 模式常驻运行，暴露：

- `GET /health` — 健康检查，初始拉取完成后返回 200
- `GET /config` — 当前生效配置快照：`{clientId, files:[{namespace,fileGroup,fileName,version,md5,content,ready}]}`（3 个文件）
- `GET /clientid` — SDK 的 clientID（供验证脚本拼接服务端 maintain 查询 URL）

## 清理

```bash
./cleanup.sh          # 交互确认后清理进程与构建/日志目录
./cleanup.sh -f       # 强制清理
./cleanup.sh --dry-run # 仅展示待清理项
```

## 故障排查

| 现象 | 可能原因 |
|------|----------|
| `undefined: apiservice.ClientEvent` 编译错误 | example go.mod 未 `replace specification` 到 main 分支（见 go.mod 注释） |
| ACK 为空 / `NotFoundResource` | 客户端未通过 ReportClient 上报 clientID，或 WatchClientEvents 长连接未建立（查 client.log） |
| `applied=false` | 客户端未订阅该配置文件（检查 `/config` 的 version/md5 非空） |
| version/md5 不一致 | 客户端在 PUSH 时配置已变更但本地尚未 watch 到最新（重跑或延长等待） |
| maintain 接口鉴权失败 | `--polaris-token` 未传或无效 |
| 加密文件准备失败（步骤 2 报错退出） | console 配置接口（`/config/v1/configfiles`，与 maintain 同端口）不可达，或 token 无配置写权限 |
| 用例 3「加密配置解密一致」FAIL | 客户端 crypto/aes filter 未启用（确认非 agent 模式），或服务端下发的 `encrypt_algo` 不是 `AES` |
