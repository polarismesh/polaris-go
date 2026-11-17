# Agent Suite

本目录验证普通 Consumer 服务读取、AgentCard、挂载 Skill 快照和注册生命周期。默认 namespace 为
`go-sdk-integration`，不得改为 `default`。

## 运行

```bash
bash examples/ai-registry/agent/verify.sh --server <polaris-host> --services --processes
bash examples/ai-registry/agent/cleanup.sh \
  --state examples/ai-registry/.logs/<run-id>/state.json --services --processes --apply
```

## 用例

- `1.1` 至 `1.8`：共享 SDKContext、测试服务字段读取、实际微服务类型、AgentCard-only 注册、
  实际 HTTP 请求、心跳、0.3 双业务端口单实例，以及显式 registration port/业务版本；
- `2.3`、`2.4`：挂载 Skill 发现快照和普通 SERVICES/INSTANCE 并发读取；
- `5.1`、`5.4`：实例过滤和 SDKContext 销毁；
- `5.2`：受影响配置中心订阅回归。

本 suite 覆盖服务读取、AgentCard 注册、发现和挂载 Skill 快照，不包含服务级
CRUD 或管理写入。

本 suite 会使用 `polaris-go-ai-e2e-*` 的 Agent/Skill 测试数据。Skill 默认保留，服务和进程
通过显式 `--services --processes` 清理。

Agent 实例契约：

- metadata 包含 `ai-agent-name`、`ai-agent-card`、`ai-a2a-version` 三个契约键；
  E2E runner 额外附加 owner/run 清理标签；
- `1.4`/`1.5` 使用 1.0 卡片，`supportedInterfaces[].protocolBinding` 选择 endpoint；
- `1.7` 使用 0.3 卡片，顶层 `url` + `additionalInterfaces.transport`，同一 IP 的
  两个不同业务端口只生成一个实例，两个 HTTP listener 都可调用；
- `1.8` 通过通用实例注册路径注册，显式 registration port=18881，业务 version 变化，
  schema 仍为 1.0；
- 实例 host 等于卡片 IP；默认 registration port 为 `1`，`1.8` 显式 `18881`；
  version 等于 `AgentCard.version`，protocol 为空；
- 发现后的 `ai-agent-card` 必须与注册前 JSON 完全一致，未知字段和签名不得被重写。
