# MCP Suite

本目录验证 Go SDK 的 MCP 实例 metadata、普通实例注册/发现/心跳/反注册，以及
Provider/Consumer 的 Service Contract 上报和读取。实例注册仍由 ProviderAPI 完成。

默认 namespace 为 `go-sdk-integration`。

## 运行

```bash
bash examples/ai-registry/mcp/verify.sh --server <polaris-host>
bash examples/ai-registry/mcp/cleanup.sh \
  --state examples/ai-registry/.logs/<run-id>/state.json --services --apply
```

## 用例

- `6.1` `mcp-streamable-http`：校验 protocol、服务版本、MCP metadata、发现和 URL；通过
  ProviderAPI 上报 Tool/Resource/Prompt 和 request/notification method，ConsumerAPI 校验契约身份，管理端查询
  校验 feature `101/102/103` 及 method/path 已真实保存；
- `6.2` `mcp-sse`：校验 SSE metadata、路径和隔离的协议实例；
- `6.3` `mcp-heartbeat-and-deregister`：TTL 1 秒，等待心跳后确认健康，再反注册并等待消失。

固定 metadata：

- `mcp-server-name`
- `mcp-protocol-version`
- `mcp-endpoint-path`

不依赖其他示例的固定服务名或测试数据；服务名由 Go 运行 ID 生成。

`mcp-stdio` 由本地单测覆盖，只生成和校验 metadata，不注册网络实例。

若 `GetServiceContract` 暂时返回旧的 feature/interface 快照，suite 会校验契约身份，
并通过管理端接口确认上报内容已持久化。服务端缓存刷新后重试即可。
