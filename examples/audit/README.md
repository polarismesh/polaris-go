# callAuditLog 远程验证（连接真实 Polaris 服务端，被调 provider + 主调 consumer）

本目录连接**远程真实 Polaris 服务端**，验证服务调用审计日志插件 `callAuditLog` 是否正常写盘。

验证采用**主调/被调分离**架构（参考 `examples/quickstart`）：

- `provider/`：被调服务，注册实例到远程服务端并对外提供 HTTP echo，进程长驻
- `consumer/`：主调服务，发现被调实例 → 真实调用 → 上报调用结果 → 校验审计日志

> 无需外部服务端、自带 mock Polaris 的自包含集成测试版本见子目录 [`local/`](./local/)。

## 验证内容

1. provider 从 `polaris.yaml` 加载配置（命令行 `-server` 覆盖服务端地址），注册被调实例（`RegisterInstance` 自动心跳保活）并启动 HTTP echo
2. consumer 从含 `callAuditLog` 的 `polaris.yaml` 加载配置，`GetOneInstance` 发现被调实例（带重试，容忍注册到发现的服务端传播延迟）
3. consumer **持续请求**：复用同一 `ConsumerAPI`，按 `-interval` 周期发起「发现→真实 HTTP 调用 `/echo`→`UpdateServiceCallResult` 上报」，持续 `-duration`（默认每 5s 一次、共 60s，约 12 次），每次上报触发 `callAuditLog` 写一条审计日志
4. 等待审计日志异步刷盘后，读取审计日志文件，验证记录已生成并对比行数与上报次数

## 前提条件

- 有一个可访问的远程 Polaris 服务端（`<host>:<port>`，gRPC 端口，通常为 `8091`）
- 服务端允许客户端注册实例（即 provider 可直接 `RegisterInstance` 注册 `-service` 指定的服务，无需预先在控制台创建）；若服务端开启鉴权，需通过 `TOKEN` 传入服务访问 Token

## 运行方式

脚本一键运行：自动起 provider、等注册完成、跑 consumer、收尾 kill provider；consumer 默认持续请求 60s、每 5s 一次（约 12 条审计日志）。支持**命令行参数**（优先）与**环境变量**两种方式，`callee` 指被调服务：

```bash
cd examples/audit

# 方式一：命令行参数（--polaris-server 必填）
bash verify.sh --polaris-server 127.0.0.1:8091 --callee-namespace default --callee-service LogAuditCallee

# 服务端开鉴权时补 --token
bash verify.sh --polaris-server 127.0.0.1:8091 --callee-service LogAuditCallee --token <service-token>

# 自定义持续请求时长/间隔（如 30s 内每 3s 一次）
bash verify.sh --polaris-server 127.0.0.1:8091 --duration 30s --interval 3s

# 查看全部参数
bash verify.sh --help

# 方式二：环境变量（等价）
POLARIS_SERVER=127.0.0.1:8091 NAMESPACE=default SERVICE=LogAuditCallee bash verify.sh
DURATION=30s INTERVAL=3s POLARIS_SERVER=127.0.0.1:8091 SERVICE=LogAuditCallee TOKEN=<service-token> bash verify.sh
```

手动分步运行（两个终端；SDK 日志与审计日志落到各自工作目录的 `./polaris/log/`）：

```bash
# 终端 1：起 provider（长驻，注册被调实例 + HTTP echo）
cd examples/audit
go build -o audit_provider ./provider
./audit_provider -server 127.0.0.1:8091 -namespace default -service LogAuditCallee

# 终端 2：跑 consumer（发现 → 调用 → 上报 → 验证审计日志）
cd examples/audit
go build -o audit_consumer ./consumer
./audit_consumer -server 127.0.0.1:8091 -namespace default -service LogAuditCallee
```

> 手动运行时进程工作目录为 `examples/audit`，SDK 日志与审计日志写到 `examples/audit/polaris/log/`；脚本运行（`verify.sh`）则统一汇总到 `.logs/`，见下文「日志汇总」。

开启 SDK debug 日志：provider/consumer 均加 `-debug`。

## 脚本参数（verify.sh）

命令行参数优先于环境变量；二者等价，任选其一。`callee` 指被调服务。

| 命令行参数 | 等价环境变量 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `--polaris-server` | `POLARIS_SERVER` | （必填） | 远程 Polaris 服务端地址 `<host>:<port>` |
| `--callee-namespace` | `NAMESPACE` | `default` | 被调服务所在命名空间 |
| `--callee-service` | `SERVICE` | `LogAuditCallee` | 被调服务名（provider 注册、consumer 发现） |
| `--token` | `TOKEN` | （空） | 服务访问 Token，服务端开鉴权时必填 |
| `--duration` | `DURATION` | `60s` | consumer 持续请求总时长 |
| `--interval` | `INTERVAL` | `5s` | consumer 请求间隔（每隔该时长调用+上报一次） |
| `-h` / `--help` | — | — | 显示用法帮助 |

## 命令行参数

provider：

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `-server` | （必填） | 远程 Polaris 服务端地址 `<host>:<port>` |
| `-config` | `./provider/polaris.yaml` | polaris.yaml 路径 |
| `-namespace` | `default` | 命名空间 |
| `-service` | `LogAuditCallee` | 被调服务名 |
| `-token` | （空） | 服务访问 Token |
| `-port` | `0` | HTTP echo 监听端口，0 为随机端口 |
| `-debug` | `false` | 开启 SDK debug 日志 |

consumer：

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `-server` | （必填） | 远程 Polaris 服务端地址 `<host>:<port>` |
| `-config` | `./consumer/polaris.yaml` | polaris.yaml 路径（需启用 callAuditLog；本 demo 同时启用 prometheus 上报） |
| `-namespace` | `default` | 命名空间 |
| `-service` | `LogAuditCallee` | 被调服务名 |
| `-caller-namespace` | （空） | 主调服务命名空间（写入审计日志 caller_namespace）；留空则与被调命名空间 `-namespace` 一致 |
| `-caller-service` | `caller-service` | 主调服务名（写入审计日志 caller_service） |
| `-caller-ip` | （空） | 主调方 IP（写入审计日志 caller_ip）；留空则自动用 SDK 本机 IP `GetBindIP()`（与 client_info.json 的 host 一致） |
| `-method` | `/api/demo/get` | 调用方法（写入审计日志） |
| `-duration` | `60s` | 持续请求总时长（`<=0` 表示只请求一次） |
| `-interval` | `5s` | 持续请求间隔（每隔该时长调用+上报一次） |
| `-audit-log` | `./polaris/log/audit/polaris-audit.log` | 审计日志路径，需与 polaris.yaml 一致 |
| `-debug` | `false` | 开启 SDK debug 日志 |

## 预期结果

consumer 启动时打印 SDK 本机客户端身份，形如：

```
[INFO] SDK client IP=127.0.0.1 ID=127.0.0.1-72768（IP 与 client_info.json 的 host 一致；ID 为 SDK 客户端唯一标识；审计日志 caller_ip/caller_id 取自此二者）
```

consumer 按 `-interval` 周期持续上报，审计日志逐条追加（每条时间戳约相差一个 `-interval`），形如：

```json
{"timestamp":"2026-07-23T20:30:12.151642+08:00","caller_service":"caller-service","caller_namespace":"default","caller_ip":"127.0.0.1","caller_id":"127.0.0.1-72768","callee_namespace":"default","callee_service":"LogAuditCallee","callee_host":"<provider-ip>:<port>","callee_id":"...","method":"/api/demo/get","delay_ms":0,"ret_code":200,"ret_status":"success"}
{"timestamp":"2026-07-23T20:30:17.153241+08:00","caller_service":"caller-service","caller_ip":"127.0.0.1","caller_id":"127.0.0.1-72768",...}
{"timestamp":"2026-07-23T20:30:22.153082+08:00","caller_service":"caller-service","caller_ip":"127.0.0.1","caller_id":"127.0.0.1-72768",...}
...
```

consumer 末尾打印 `审计日志共 N 行，本次成功上报 M 次`（正常 `N == M`；`callAuditLog` 为 best-effort，队列满时可能 `N < M`，此时打印 WARN 但不判失败），并以 `验证通过：审计日志已生成` 结尾。

### 主调身份字段（caller_ip / caller_id）

审计日志的两个主调身份字段取自 SDK 本机客户端，与 `.logs/consumer_run/polaris/backup/client_info.json` 记录的同一个 SDK 客户端对应：

| 审计字段 | 取值来源 | 与 client_info.json 的关系 |
| --- | --- | --- |
| `caller_ip` | 未显式 `-caller-ip` 时，回退到 SDK 本机 IP `GetBindIP()` | 等于 `client.host` |
| `caller_id` | SDK 客户端唯一标识 `GetClientId()`（由 SDK 提供，用户不可覆盖） | 标识同一个 SDK 客户端（该响应缓存文件本身不回显 id） |

- `-caller-ip` 默认留空 → `caller_ip` 自动等于 `GetBindIP()`（与 `client_info.json` 的 `host` 一致）；若显式传 `-caller-ip <ip>` 则以该值覆盖，用于透传上游真实主调 IP。
- `caller_id` 恒为 SDK 客户端 ID，无需也无法在调用侧设置。

## 日志汇总

`verify.sh` 参考 `examples/auth`，把 demo 的标准输出与 SDK 的文件日志统一汇总到脚本目录下的 `.logs/`，编译产物集中到 `.build/`（两者均已在 `.gitignore` 忽略，不入库）：

```
examples/audit/
├── .build/                              # 编译产物
│   ├── audit_provider
│   └── audit_consumer
└── .logs/                               # 统一日志目录
    ├── verify-audit-<时间戳>.log        # 脚本自身输出（双写，去 ANSI 颜色）
    ├── provider.log                     # provider 进程 stdout/stderr
    ├── consumer.log                     # consumer 进程 stdout/stderr
    ├── provider_run/polaris/log/        # provider 侧 SDK 文件日志
    │   └── base | network | lossless | auth | ...
    └── consumer_run/polaris/log/        # consumer 侧 SDK 文件日志
        ├── base | network | cache | ...
        └── audit/polaris-audit.log      # 审计日志（本 demo 的核心产物）
```

实现方式（不改 Go 代码）：provider/consumer 分别在 `.logs/provider_run`、`.logs/consumer_run` 作为**工作目录**运行，SDK 文件日志与审计日志（相对工作目录的 `./polaris/log/`）自然落到各自目录下；进程 stdout/stderr 由脚本重定向到 `.logs/provider.log`、`.logs/consumer.log`；脚本自身诊断输出双写到 `verify-audit-<时间戳>.log`。

清理残留进程与 `.build`/`.logs` 目录（覆盖远程版与 `local/`）：

```bash
cd examples/audit
bash cleanup.sh            # 先展示再确认后清理
bash cleanup.sh -f         # 强制清理，不确认
bash cleanup.sh --dry-run  # 仅展示，不清理
```

## 配置说明

- `provider/polaris.yaml`：仅配置 `serverConnector`（地址运行时由 `-server` 覆盖）；provider 无需 `callAuditLog`。
- `consumer/polaris.yaml`：`serverConnector` 地址运行时由 `-server` 覆盖；`statReporter.chain` 同时启用 `callAuditLog`（审计日志）与 `prometheus`（监控数据上报），二者并列互不影响。
- 审计日志的 `rotateOutputPath` 为 `./polaris/log/audit/polaris-audit.log`（相对进程工作目录）。经 `verify.sh` 运行时，consumer 工作目录为 `.logs/consumer_run`，故审计日志实际落在 `.logs/consumer_run/polaris/log/audit/polaris-audit.log`（见上方「日志汇总」）。
- `callAuditLog` 采用异步缓冲写盘，consumer 在 `UpdateServiceCallResult` 后等待 2 秒再读取审计文件，确保后台刷盘完成。

### 监控数据上报（prometheus，参考 examples/quickstart/consumer）

`consumer/polaris.yaml` 的 `statReporter.plugin.prometheus` 开启监控上报：

- `type: push`：以 push 模式将指标推送到 pushgateway；`interval: 10s` 为上报周期。
- **pushgateway 地址走服务发现**：`address` 留空，SDK 通过 `pushGatewayNamespace`/`pushGatewayService`（默认 `Polaris`/`polaris.pushgateway`）经 Polaris 服务发现解析地址，复用 `-server` 指定的服务端连接——无需额外配置地址，契合本 demo「只用 `-server` 一处指定服务端」的设计。
- 如需显式指定 pushgateway 地址，取消 `# address: ${POLARIS_SERVER}:9091` 注释并填入实际地址（`${ENV}` 占位在 SDK 加载时由 `os.ExpandEnv` 替换，需确保该环境变量已 export 到 consumer 进程）。
- 若改用 `type: pull`，SDK 会暴露 HTTP 端口（`metricPort`，默认 28080）供 Prometheus 主动拉取，无需 pushgateway。
- **监控上报为 best-effort，不影响审计验证主流程**：若服务端未注册 `polaris.pushgateway` 或地址不可达，push 仅打印收敛告警（`[metrics][push]`）而不会中断 consumer 或使审计验证失败。

## 行为语义（重要）

- **启用方式（opt-in）**：仅当 `global.statReporter.enable: true` 且 `global.statReporter.chain` 中显式包含 `callAuditLog` 时，插件才会初始化并工作。未加入 chain 时插件不会创建任何后台 goroutine 或审计文件，对未启用审计的应用零副作用。
- **配置校验为 fail-fast**：若启用了本插件但配置非法（如 `format` 既非 `json` 也非 `kv`、`bufferSize`/`flushInterval` 为负数），SDK 初始化会直接失败并返回错误。请在启用前确保配置正确，避免应用启动失败。
- **审计为尽力而为（best-effort），不保证不丢**：
  - 写盘走异步缓冲队列（`bufferSize`，默认 4096）。当业务上报速率持续高于磁盘刷盘速率导致队列满时，新条目会被**丢弃**，仅按 `flushInterval` 周期在运行日志中打印累计丢弃条数的 WARN 汇总，审计文件本身不含缺失标记。
  - 进程退出（`Destroy`）时会尽力排空队列，但退出瞬间仍在入队的迟到条目可能被丢弃。
  - 若业务对审计完整性有强合规要求，请据此评估队列容量并结合丢弃告警监控，或改用同步落盘的外部审计方案。
- **KV 格式转义**：`format: kv` 时字符串字段统一以 `%q` 加引号并转义（含空格、换行、引号），保证「一行一条」的审计前提与下游按行解析不被字段内容破坏。
- **被调 Host 可达性**：provider 注册的 Host 为本机到服务端的出口 IP（`getLocalHost`）。`verify.sh` 在单机串行起 provider/consumer，主调访问该出口 IP 即为本机回环，HTTP 调用可通；若出口 IP 不可达（NAT/容器等），consumer 的 HTTP 调用会降级为构造的失败结果，但不影响审计日志验证（审计只依赖 `UpdateServiceCallResult`）。
