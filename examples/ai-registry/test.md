# AI Registry 真实 E2E

本测试需要连接真实 Polaris 服务端。本地 mock 和单元测试不替代真实服务端验证。

## 前置条件

- Go 和 Bash；依赖与根模块一致，不新增 module。清理用 `ps` 验证进程完整命令。
- Polaris 服务端支持 `AI_AGENT` 服务投影、测试数据 extended metadata、MCP Service
  Contract、Skill Registry 多文件发布/下载、配置分组订阅。
- 端口默认 Discover `8091`、Config `8093`、Skill `8094`、Console `8090`，可通过 `--discover`、`--config-address`、`--skill`、`--console` 显式覆盖。
- `--namespace` 默认 `go-sdk-integration`，拒绝 `default`；默认运行 ID 为
  `yyyyMMddHHmmssSSS`，服务名为 `go-sdk-e2e-<run-id>-<label>`，owner 为
  `polaris-go-ai-registry`，服务/实例附带 `e2e.run.id`。
- 可复用测试数据：`polaris-go-ai-e2e-a-weather`（`1.0.0`、`2.0.0`），`polaris-go-ai-e2e-z-currency`（`1.0.0`），`polaris-go-ai-e2e-draft`（草稿）。同名但不属于本示例的数据会被拒绝。Weather ZIP 有两个 640 KiB 不可压缩附件，总量约 1.25 MiB；每个文件低于服务端 1 MiB 上限。
- Console token fallback：`POLARIS_CONSOLE_TOKEN` → `POLARIS_TOKEN` → `POLARIS_E2E_SERVICE_TOKEN` → `POLARIS_SERVICE_TOKEN`；Skill 为 `POLARIS_SKILL_TOKEN` → `POLARIS_TOKEN`，Config 为 `POLARIS_CONFIG_TOKEN` → `POLARIS_TOKEN`，Provider 为 `POLARIS_E2E_SERVICE_TOKEN` → `POLARIS_SERVICE_TOKEN`。无默认 token。`--config-file` 可加载本地配置，环境变量优先；状态和报告不含 token。
- 云端验证只需本地有 Go；云端 Linux 机器不需要 Go 或仓库源码，但需能访问
  Discover `8091`、Console `8090`、Config `8093`、Skill `8094`。部署包、日志包和安全
  边界见 [cloud/README.md](cloud/README.md)。

## 运行流程

```bash
bash examples/ai-registry/verify_e2e.sh --server <polaris-host>
bash examples/ai-registry/agent/verify.sh --server <polaris-host>
bash examples/ai-registry/mcp/verify.sh --server <polaris-host>
bash examples/ai-registry/skill/verify.sh --server <polaris-host> --timeout 60s
bash examples/ai-registry/verify_e2e.sh --server <polaris-host> --services --processes
bash examples/ai-registry/cleanup.sh --state .logs/<run-id>/state.json --services --processes --dry-run
bash examples/ai-registry/cleanup.sh --state .logs/<run-id>/state.json --services --processes --apply
```

根入口默认执行 `all` suite；子目录入口只执行对应 suite。suite、namespace、运行 ID 和测试
结果均写入 `result.json`，清理仍以 state 中的 owner/run（归属标记和运行 ID）为准。
也可在根入口显式使用 `--suite agent|mcp|skill`；`POLARIS_E2E_SUITE` 可提供相同选择。

`--timeout` 默认 10s，完整 `--run-timeout` 默认 12m，须大于单次超时。每个 eventual 场景以 200ms 间隔在其 deadline 内重试，失败仍记录为 FAIL，不静默 skip。输出 `.logs/<run-id>/state.json`、`result.json` 和独立 Provider 日志；清理之后才写最终 success。

低带宽远端可显式用 `--timeout 2m --run-timeout 35m`。该参数会同时设置该次 E2E 的 Skill 消息与 ZIP 下载预算，不修改 SDK 或 `polaris.yaml` 的默认值，也不减少测试数据；Provider ready 预算为 timeout + 35s。多帧 ZIP 与内容检查保持不变。

默认保留进程和服务。选择 `--services` 时同时选择 `--processes`，先停止 Provider 防止重新注册，再按 owner/run 验证并删除服务及实例；配置组验证 comment 的 owner/run 后删除。Skill 默认保留。`--apply`、`--force` 或 `-f` 才执行 cleanup 删除；`--dry-run` 优先，保留并展示资源选择，禁止写入和停止。`--dry-run` 仅允许 cleanup，verify 提前拒绝此参数。日志和结果默认保留，便于排查失败。

## 云端验证（打包与日志回传）

云端验证使用与本地完全相同的 27 项用例和断言，不改变 suite、namespace、owner/run 或
清理规则。流程是本地打包、上传、云端执行、云端打包日志、`scp` 回本地分析。

本地打包：

```bash
bash examples/ai-registry/cloud/build-cloud-package.sh
# arm64 云端：
bash examples/ai-registry/cloud/build-cloud-package.sh --goarch arm64
```

云端执行：

```bash
cd /tmp
tar xzf ai-registry-cloud-*.tar.gz
cd ai-registry-cloud-*
./run-cloud.sh --server <polaris-host> --suite all --timeout 2m --run-timeout 35m
```

云端默认超时为 `2m`/`35m`，默认保留资源。显式清理：

```bash
./run-cloud.sh --server <polaris-host> --cleanup
./run-cloud.sh --server <polaris-host> --cleanup --cleanup-skills
```

`run-cloud.sh` 将 stdout 写入 `.logs/<run-id>/run-cloud.log`，运行结束（包括失败）会
自动执行：

```bash
./pack-cloud-logs.sh --run-dir .logs/<run-id>
```

生成 `ai-registry-logs-<run-id>-<时间>.tar.gz` 和 `.sha256`，包含 `result.json`、
`analysis.md`、`analysis.json`、`state.json`、Provider/SDK 日志、Skill 持久化、
`run.env`、`package.env` 和云端诊断。
包内不含环境变量或 `--config-file` 指向的配置文件。最后按脚本打印的 `scp` 命令回传：

```bash
scp '<user>@<cloud-host>:<绝对路径>/ai-registry-logs-*.tar.gz*' .
```

本地安全导入：

```bash
bash examples/ai-registry/cloud/import-logs.sh ai-registry-logs-*.tar.gz
```

导入会校验 SHA256、拒绝不安全路径和链接，并重新生成每个 run 的 `analysis.md` /
`analysis.json`。

手动重复打包同一次运行或汇总全部运行：

```bash
./pack-cloud-logs.sh --run-dir .logs/<run-id>
./pack-cloud-logs.sh --all
```

## Agent 用例

### [用例 1.1 shared-sdk-context]
操作：创建多个公开 API。预期：Skill、Consumer、Provider 全部共享同一 SDKContext。

### [用例 1.2 console-fixture-consumer-readback]
操作：Console 预置隔离的 AI_AGENT 测试数据，普通 Consumer 读取服务字段与两个 Skill 引用。
预期：实际 AI_AGENT 类型、owner/run metadata 和挂载 Skill 均可见，不从实例伪造服务类型或 revision。

### [用例 1.3 ordinary-service-discovery-type]
操作：预置普通微服务，调用 Consumer。预期：实际类型保持微服务，不执行 Agent 类型推断。

### [用例 1.4 agentcard-only-auto-registration]
操作：独立 Provider 仅实例三键 metadata 注册，不预建 AI 服务。预期：自动创建 AI_AGENT、服务无挂载 Skill，真实发现成功。

### [用例 1.5 discovered-agentcard-real-request]
操作：GetInstances 并读取三键 metadata，请求 Provider ready 文件记录的业务 URL。
预期：卡片 JSON 与注册前完全一致，回复匹配 service 和请求内容，不使用实例 port 或硬编码发现结果。

### [用例 1.6 automatic-heartbeat-remains-healthy]
操作：TTL=1 自动注册后等 5s，再发现。预期：实例依然健康、非隔离。

### [用例 1.7 agentcard-v03-multi-business-port-single-instance]
操作：使用 0.3 卡片注册同一 IP、两个不同业务端口的 HTTP+JSON interface，发现后分别调用两个
HTTP listener。
预期：只产生一个实例，两个业务端口可调用且不相同，registration port=1、protocol 为空、
version 来自 AgentCard，卡片 JSON 原样保真。

### [用例 1.8 explicit-registration-port-and-business-version]
操作：Provider 通过通用实例注册路径注册，显式 registration port=18881、业务版本
`2026.11-explicit`，schema 保持 1.0。
预期：实例 host/port/version/protocol/metadata 与同一快照一致，发现和真实 HTTP 调用成功。

### [用例 2.3 discover-mounted-skills-and-clone-snapshot]
操作：从 Discover 读取两个挂载 Skill，修改返回对象再读取。预期：25s 内挂载可见，调用者修改不污染缓存。

### [用例 2.4 normal-services-and-agent-concurrent-query]
操作：相同 namespace/service 的普通 GetServices 与 Consumer INSTANCE 读取各并发 12 次。预期：24 次都成功，响应无错配。

## Skill 用例

### [用例 3.1 published-explicit-and-active-version]
操作：GetSkill 查询 weather 1.0.0、2.0.0 和省略版本，等待 payload 持久化，再用不可达 Skill
地址查询。预期：storage 存在，省略版本返回 active 1.0.0，网络错误命中本地 payload；YAML front
matter 的 name/description/examples 与资源元数据一致，正文逐字节匹配。

### [用例 3.2 draft-and-missing-skill-errors]
操作：查询草稿和不存在 Skill。预期：都返回 SkillNotFound。

### [用例 3.3 published-filter-and-pagination]
操作：在隔离 namespace 中使用 limit=1、offset=0/1/2 分页，再按 name 过滤，并直接校验返回资源的测试数据 tag。
预期：两个 published 资源各一次，无草稿泄露，最后页空，两个资源都带测试数据 tag；
`GetSkillListResult.Total` 保留服务端 total=2，而不是当前页 slice 长度。

### [用例 3.4 markdown-download-and-shared-input]
操作：8 goroutine 共享省略 format 的 request 下载 Markdown。预期：front matter 元数据和正文校验、version/默认 format 正确，原 request 不被修改。

### [用例 3.5 multiframe-zip-and-file-integrity]
操作：真实下载大于 1 MiB ZIP，解包逐文件比对。预期：多帧重组完整，SKILL.md front matter 与资源元数据一致、正文和附件逐字节相同，无丢失或额外文件。

### [用例 4.1 install-and-cache-hit]
操作：同 pinned 版本安装两次。预期：首次 miss，第二次验证过的 cache hit，Dir/Digest 相同。

### [用例 4.2 tampered-entry-and-nested-file-recovery]
操作：依次篡改 SKILL.md 和嵌套 `.polaris-skill.json` 再安装。预期：均不是 cache hit，重新安装所有正确文件。

### [用例 4.3 forced-and-concurrent-install]
操作：Force 重装，再并发 12 次安装。预期：Force 不是 hit，所有安装结果完整。

### [用例 4.4 active-version-change-and-pinned-cache]
操作：管理端切 active 为 2.0.0，省略版本 Install，再 pinned 1.0.0。预期：25s 内新 active 内容可见，旧 pinned 缓存仍可用且路径不同。

### [用例 4.5 provider-loads-published-skill-and-invokes]
操作：独立 Provider pinned Install 1.0.0，以安装 Dir 拼接包内相对 EntryFile 读取指令，发现后发业务请求。预期：回复含实际安装的 1.0.0 指令。

### [用例 5.3 provider-deregister-and-stop-heartbeat]
操作：SIGTERM Skill Provider，等发现移除，再等 3s。预期：25s 内实例消失，停止心跳后不重新注册。

## Agent 公共回归

### [用例 5.1 healthy-and-isolated-instance-filtering]
操作：同服务注册隔离实例，再查 GetAllInstances 和 GetInstances。预期：all 能见隔离实例，filtered 不能见；随后反注册。

### [用例 5.2 configuration-read-watch-and-cancel]
操作：独立配置组发布 generation 0 并记录版本、订阅、发布并获取 generation 1、取消两次、发布 generation 2 并获取。预期：第一轮内容正确且版本递增，callback 的 namespace/group/name/version 与 generation 1 发布完全匹配，提供 MD5 时必须相等，旧版本或无关事件不能通过；分组列表省略 MD5 时，以相同文件/版本关联已实际读取并校验的发布内容。取消后第二轮版本递增、内容可读且不再回调。

### [用例 5.4 sdk-destroy-rejects-consumer-and-skill]
操作：独立 Context 先真实查询 Skill，再 Destroy，调用 Consumer/Skill API。预期：都返回 InvalidStateError。

## MCP 用例

### [用例 6.1 mcp-streamable-http-metadata-and-discovery]
操作：注册 `mcp-streamable-http` 普通实例，写入 `mcp-server-name`、`mcp-protocol-version`、
`mcp-endpoint-path`，并通过 ProviderAPI 上报三代 MCP feature 和两个 method，再由 ConsumerAPI
查询。预期：protocol、version、metadata、发现和 endpoint 一致；Contract 响应身份正确，
管理端确认 feature/method 已持久化。

### [用例 6.2 mcp-sse-endpoint-and-metadata]
操作：注册 `mcp-sse` 实例。预期：SSE metadata、protocol 和 `/sse` endpoint 正确。

### [用例 6.3 mcp-heartbeat-and-deregister]
操作：TTL 1 秒注册，等待超过 TTL 后复查健康状态，再反注册。预期：心跳保持实例健康，
反注册后实例在 deadline 内消失。

MCP suite 不实现 MCP Client 或 `tools/list` 调用。若 Contract 查询暂时返回旧快照，
以管理端已持久化的上报结果为准，并等待服务端缓存刷新。

## 失败排查

- 管理接口鉴权失败：使用上述 token 变量或本地配置；不在命令行传 token，不打印响应中的 access token。
- `Consumer did not return the actual AI_AGENT fixture metadata`：检查 Discover 返回的
  服务类型和 extended metadata。
- `published fixture list total=1 size=1`：逐名查询 weather 2.0.0 和 currency 1.0.0；
  若 Console 均显示 published 而 List 仍为 1，请检查服务端的列表投影和缓存刷新。
- 测试数据归属不匹配或内容不同：使用另一个隔离 namespace，不覆盖已有资源。
- 清理拒绝 service/instance/group/PID：检查 state 和远端 owner/run。
- 本地端口监听失败：检查 localhost 网络权限和端口占用。
- 云端 `exec format error`：部署包架构与云端不一致；按实际机器使用
  `--goarch amd64` 或 `--goarch arm64` 重新打包。
- 云端没有日志包：检查 `.logs/<run-id>/` 是否存在，再手动执行
  `./pack-cloud-logs.sh --run-dir .logs/<run-id>`；日志包生成失败不影响
  `result.json` 和 `state.json` 的保留。
