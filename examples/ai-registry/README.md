# AI Registry 真实集成示例

该示例属于根 Go module。一个共享 SDKContext 同时提供 SkillAPI、ConsumerAPI、
ProviderAPI 和配置 API；独立 Provider 进程分别注册 1.0 和 0.3 AgentCard、自动心跳，实例
metadata 使用 `ai-agent-name`、`ai-agent-card`、`ai-a2a-version` 三键，registration port 默认
`1` 且 protocol 为空；Consumer 从真实 Polaris 发现实例后按 `protocolBinding` 选择 interface
并发起 HTTP 请求。Skill Provider 下载并安装已发布的 Skill，把实际安装内容用于业务响应。
MCP 场景同时验证标准实例 metadata、Service Contract 上报和真实管理端持久化结果。

运行器按 Agent、MCP、Skill 拆分为三个 suite。服务读取通过 Consumer，Console
仅用于预置和清理隔离测试数据。

需要可访问的 Polaris Discover、Skill、Config gRPC 和 Console HTTP 服务。完整场景、前置条件和清理规则见 [test.md](test.md)。此处的 HTTP echo 验证注册、发现和已加载 Skill 的调用链，不实现 A2A 推理协议或调用外部 LLM。

```bash
bash examples/ai-registry/verify_e2e.sh --server <polaris-host>
```

## 云端验证与日志回传

需要把验证放到云端 Linux 测试机执行时，本地先生成自包含部署包，再上传运行：

```bash
# 本地：默认交叉编译 linux/amd64，arm64 机器加 --goarch arm64
bash examples/ai-registry/cloud/build-cloud-package.sh
scp examples/ai-registry/dist/ai-registry-cloud-*.tar.gz <user>@<cloud-host>:/tmp/
```

云端：

```bash
cd /tmp
tar xzf ai-registry-cloud-*.tar.gz
cd ai-registry-cloud-*
./run-cloud.sh --server <polaris-host> --suite all --timeout 2m --run-timeout 35m

# 云端生成 ai-registry-logs-<run-id>-<时间>.tar.gz，再 scp 回本地分析
```

云端包不包含 Go 工具链、仓库源码、token 或 SDK 配置文件。默认保留 Provider、服务和 Skill；
显式传 `--cleanup` 才停止 Provider 并清理本次 run 的服务/实例，传
`--cleanup --cleanup-skills` 才同时删除三个 Skill fixture。打包结构、云端运行参数、
日志包内容和本地分析步骤见 [cloud/README.md](cloud/README.md)。

回传后使用安全导入脚本生成统一摘要：

```bash
bash examples/ai-registry/cloud/import-logs.sh ai-registry-logs-*.tar.gz
```

默认使用独立 `go-sdk-integration` 命名空间，服务名携带本次运行的唯一 ID。用例按 Agent、MCP、Skill
拆分为三个子目录；默认入口运行全部 suite：

```bash
bash examples/ai-registry/verify_e2e.sh --server <polaris-host> --services --processes
bash examples/ai-registry/agent/verify.sh --server <polaris-host> --services --processes
bash examples/ai-registry/mcp/verify.sh --server <polaris-host>
bash examples/ai-registry/skill/verify.sh --server <polaris-host> --timeout 60s
```

脚本默认保留 Provider 进程、服务、Skill 和诊断日志；验证器的取消监听、反注册测试会停止其专用
Skill Provider，但保留 AgentCard Provider 供检查。显式自清理运行：

```bash
bash examples/ai-registry/cleanup.sh \
  --state examples/ai-registry/.logs/<run-id>/state.json --services --processes --apply
```

各 suite 的用例和边界见 [agent/test.md](agent/test.md)、[mcp/test.md](mcp/test.md) 和
[skill/test.md](skill/test.md)。
根 runner 也支持 `--suite all|agent|mcp|skill`，默认 `all`。

默认配置和部署包不会写入部署地址或 token。云端 `run.env` 只记录本次 `--server` 以便
分析，不记录 token。Token 只通过环境变量或本地 SDK 配置读取；Console 使用
`POLARIS_CONSOLE_TOKEN`、`POLARIS_TOKEN`、`POLARIS_E2E_SERVICE_TOKEN`、
`POLARIS_SERVICE_TOKEN` 的第一个非空值。Skill 使用 `POLARIS_SKILL_TOKEN` 或
`POLARIS_TOKEN`，配置使用 `POLARIS_CONFIG_TOKEN` 或 `POLARIS_TOKEN`，注册使用
`POLARIS_E2E_SERVICE_TOKEN` 或 `POLARIS_SERVICE_TOKEN`。环境变量未提供时保留
`--config-file` 中相应配置，不内置凭据。

清理要求明确状态文件；默认仅展示，不停止进程、不删除服务：

```bash
bash examples/ai-registry/cleanup.sh --state .logs/<run-id>/state.json
bash examples/ai-registry/cleanup.sh --state .logs/<run-id>/state.json --services --processes --apply
```

默认保留可复用 Skill。仅在明确希望删除本示例持有的三组测试数据时添加 `--skills`；所有删除都验证
owner/run（归属标记和运行 ID），`--force` 也不绕过归属检查。MCP suite 覆盖 metadata、发现、心跳、反注册、
Service Contract 上报和 feature/method 持久化；MCP Client、`tools/list` 和真实 Tool 调用仍不在本仓库范围。
