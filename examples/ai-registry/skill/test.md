# Skill Suite

本目录验证 published/active Skill 查询、storage、分页 total、Markdown/ZIP 下载、payload 持久化和
网络错误 fallback、Go 扩展 Install、缓存修复、Force 和 active 切换。
默认 namespace 为 `go-sdk-integration`。

## 运行

```bash
bash examples/ai-registry/skill/verify.sh --server <polaris-host> --timeout 60s
bash examples/ai-registry/skill/cleanup.sh \
  --state examples/ai-registry/.logs/<run-id>/state.json --services --apply
```

## 用例

- `3.1` 至 `3.5`：published/active/pinned 查询、storage、错误语义、分页 total、Markdown、多帧 ZIP；
- `4.1` 至 `4.5`：缓存命中、篡改修复、Force/并发、active 切换和独立 Provider 加载；
- `5.3`：停止 Skill Provider 并确认反注册/心跳停止。

Go 测试数据使用 `polaris-go-ai-e2e-*`；不要复用其他示例的固定 Skill 名称、
测试数据或默认 namespace。Skill 默认保留，只有显式 `--skills` 才删除。

`3.1` 会等待 `active/` 持久化完成后，用不可达的 Skill 地址创建第二个 owned Context，验证网络错误
回退到同一 payload。列表 total 是服务端的 published 分页总数，不是页面切片长度。
