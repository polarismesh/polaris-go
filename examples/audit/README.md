# callAuditLog 远程验证（连接真实 Polaris 服务端）

本目录的 Demo 连接**远程真实 Polaris 服务端**，验证服务调用审计日志插件 `callAuditLog` 是否正常写盘。

> 无需外部服务端、自带 mock Polaris 的自包含集成测试版本见子目录 [`local/`](./local/)。

## 验证内容

1. 从 `polaris.yaml` 加载配置（已启用 `callAuditLog` 插件），并用命令行 `-server` 覆盖服务端地址
2. `GetOneInstance` 从远程服务端获取一个实例
3. `UpdateServiceCallResult` 上报服务调用结果（含主调服务/IP、方法、时间戳、耗时、返回码）
4. 等待审计日志异步刷盘后，读取审计日志文件，验证审计记录已生成

## 前提条件

- 有一个可访问的远程 Polaris 服务端（`<host>:<port>`，gRPC 端口，通常为 `8091`）
- 远程服务端上**已注册** `-namespace` / `-service` 指定的服务，且该服务存在**健康实例**（否则 `GetOneInstance` 会失败）

## 运行方式

```bash
cd examples/audit
# 方式一：脚本运行（POLARIS_SERVER 必填）
POLARIS_SERVER=127.0.0.1:8091 NAMESPACE=default SERVICE=DemoService bash verify.sh

# 方式二：手动运行
go build -o audit_remote . && \
  ./audit_remote -server 127.0.0.1:8091 -namespace default -service DemoService
```

开启 SDK debug 日志：

```bash
./audit_remote -server 127.0.0.1:8091 -service DemoService -debug
```

## 命令行参数

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `-server` | （必填） | 远程 Polaris 服务端地址 `<host>:<port>`，覆盖 `polaris.yaml` 中的服务端地址 |
| `-namespace` | `default` | 被调服务所在命名空间 |
| `-service` | `DemoService` | 被调服务名 |
| `-caller-service` | `caller-service` | 主调服务名（写入审计日志主调方信息） |
| `-caller-ip` | `10.0.1.5` | 主调方 IP（写入审计日志主调方 IP） |
| `-method` | `/api/demo/get` | 本次调用的接口方法（写入审计日志） |
| `-audit-log` | `./polaris/log/audit/polaris-audit.log` | 审计日志路径，需与 `polaris.yaml` 一致 |
| `-debug` | `false` | 是否开启 Polaris SDK debug 日志 |

## 预期结果

程序输出审计日志内容，形如：

```json
{"timestamp":"2026-07-17T15:30:00.123456789+08:00","caller_service":"caller-service","caller_namespace":"default","caller_ip":"10.0.1.5","callee_namespace":"default","callee_service":"DemoService","callee_host":"127.0.0.1:xxx","callee_id":"...","method":"/api/demo/get","delay_ms":35,"ret_code":0,"ret_status":"success"}
```

并以 `验证通过：审计日志已生成` 结尾。

## 配置说明

- `polaris.yaml`：`global.serverConnector.addresses` 仅为占位，运行时被 `-server` 覆盖；`global.statReporter` 启用 `callAuditLog` 插件并配置日志路径/格式/轮转。
- 审计日志默认写入 `./polaris/log/audit/polaris-audit.log`。
- `callAuditLog` 采用异步缓冲写盘，程序在 `UpdateServiceCallResult` 后等待 2 秒再读取审计文件，确保后台刷盘完成。

## 行为语义（重要）

- **启用方式（opt-in）**：仅当 `global.statReporter.enable: true` 且 `global.statReporter.chain` 中显式包含 `callAuditLog` 时，插件才会初始化并工作。未加入 chain 时插件不会创建任何后台 goroutine 或审计文件，对未启用审计的应用零副作用。
- **配置校验为 fail-fast**：若启用了本插件但配置非法（如 `format` 既非 `json` 也非 `kv`、`bufferSize`/`flushInterval` 为负数），SDK 初始化会直接失败并返回错误。请在启用前确保配置正确，避免应用启动失败。
- **审计为尽力而为（best-effort），不保证不丢**：
  - 写盘走异步缓冲队列（`bufferSize`，默认 4096）。当业务上报速率持续高于磁盘刷盘速率导致队列满时，新条目会被**丢弃**，仅按 `flushInterval` 周期在运行日志中打印累计丢弃条数的 WARN 汇总，审计文件本身不含缺失标记。
  - 进程退出（`Destroy`）时会尽力排空队列，但退出瞬间仍在入队的迟到条目可能被丢弃。
  - 若业务对审计完整性有强合规要求，请据此评估队列容量并结合丢弃告警监控，或改用同步落盘的外部审计方案。
- **KV 格式转义**：`format: kv` 时字符串字段统一以 `%q` 加引号并转义（含空格、换行、引号），保证「一行一条」的审计前提与下游按行解析不被字段内容破坏。
