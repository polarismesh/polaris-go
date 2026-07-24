# callAuditLog 集成测试（本地 mock 自包含版）

本目录是服务调用审计日志插件 `callAuditLog` 的端到端集成测试，**自包含**：程序内置 mock Polaris 服务端，无需外部 Polaris 部署即可运行验证。

> 需连接远程真实 Polaris 服务端做审计验证的版本见上级目录 `examples/audit/`（远程验证版）。

## 测试内容

1. 启动本地 mock Polaris gRPC 服务端（`test/mock`），注册命名空间、服务与测试实例
2. 通过 `polaris.yaml` 创建 `ConsumerAPI`（已启用 `callAuditLog` 插件）
3. `GetOneInstance` 从 mock 获取一个实例
4. `UpdateServiceCallResult` 上报服务调用结果（含主调服务/IP、方法、时间戳、耗时、返回码）
5. 读取审计日志文件，验证逐条审计记录已生成

## 运行方式

```bash
cd examples/audit/local
bash verify.sh
```

或手动运行：

```bash
cd examples/audit/local
go build -o audit_test main.go && ./audit_test
```

开启 SDK debug 日志：

```bash
./audit_test -debug
```

## 预期结果

程序输出审计日志内容，形如：

```json
{"timestamp":"2026-07-17T15:30:00.123456789+08:00","caller_service":"caller-service","caller_namespace":"Production","caller_ip":"10.0.1.5","callee_namespace":"Production","callee_service":"DemoAuditService","callee_host":"127.0.0.1:xxx","callee_id":"...","method":"/api/demo/get","delay_ms":35,"ret_code":0,"ret_status":"success"}
```

并以 `集成测试通过：审计日志已生成` 结尾。

## 日志汇总

`verify.sh` 参考 `examples/auth`，把 demo 标准输出与 SDK 文件日志统一汇总到脚本目录下的 `.logs/`，编译产物集中到 `.build/`（均已被 `.gitignore` 忽略）：

```
examples/audit/local/
├── .build/audit_test                    # 编译产物
└── .logs/
    ├── verify-audit-local-<时间戳>.log  # 脚本自身输出（双写，去 ANSI 颜色）
    ├── audit_test.log                   # demo 进程 stdout/stderr（含 mock 服务端 + SDK 日志）
    └── run/polaris/log/                 # SDK 文件日志
        ├── base | network | cache | ...
        └── audit/polaris-audit.log      # 审计日志（本 demo 的核心产物）
```

实现方式（不改 Go 代码）：demo 在 `.logs/run` 作为工作目录运行（脚本先把 `polaris.yaml` 复制过去），SDK 与审计日志相对工作目录落到 `run/polaris/log/`；stdout 用 `tee` 同时输出到终端与 `audit_test.log`。

清理由上级目录的 `cleanup.sh` 统一处理（覆盖 `local/.build`、`local/.logs`）：

```bash
cd examples/audit
bash cleanup.sh -f
```

## 配置说明

- `polaris.yaml`：`global.serverConnector.addresses` 指向本地 mock 端口（`127.0.0.1:18091`）；`global.statReporter` 启用 `callAuditLog` 插件并配置日志路径/格式/轮转。
- 审计日志的 `rotateOutputPath` 为 `./polaris/log/audit/polaris-audit.log`（相对进程工作目录）。经 `verify.sh` 运行时工作目录为 `.logs/run`，审计日志实际落在 `.logs/run/polaris/log/audit/polaris-audit.log`；直接手动运行则落在 `examples/audit/local/polaris/log/audit/polaris-audit.log`。

## 注意事项

- mock 服务端监听端口 `18091`，需与 `polaris.yaml` 保持一致；如端口被占用，请同步修改 `main.go` 中 `mockPort` 与 `polaris.yaml`。
- `callAuditLog` 采用异步缓冲写盘，程序在 `UpdateServiceCallResult` 后等待 2 秒再读取审计文件，确保后台刷盘完成。
