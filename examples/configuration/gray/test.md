# 配置灰度发布验证测试

> 本文档与 `gray-test.sh` 保持一致。脚本中每个 `run_case1`/`run_case2` 用例在此都有对应段落，用例编号、名称与脚本日志输出完全一致。

## 概述

验证 polaris-go 客户端在「不改动任何代码」的前提下，仅通过 `global.client.labels` 上报标签即可正确参与服务端配置灰度发布。

**核心结论**：灰度规则匹配与灰度内容下发完全由服务端承担，客户端只需上报标签，SDK 在拉取配置时将标签携带到 `GetConfigFile` 请求中，命中灰度即拿到灰度内容，未命中则拿到全量内容。

## 前置条件

| 项 | 说明 |
|----|------|
| 北极星服务端 | 已启动，且为含配置灰度逻辑的版本(`cache/gray` + `config/client.go` 灰度分支) |
| Go 环境 | 已安装(Go 1.19+) |
| 配置组 | `${FILE_GROUP}`(默认 `polaris-config-example`)需在服务端存在；脚本通过 `gray-demo -action=setup` 自动创建并发布配置文件 |
| 灰度发布操作 | 用例 1/2 涉及的「发布灰度」「停止灰度」需在北极星控制台手动完成(polaris-go 不提供灰度发布 API) |

## 端口与客户端

| 客户端 | 端口 | 标签 | 角色 |
|--------|------|------|------|
| 客户端 A | 18081 | `env=pre` | 常驻，用例 2 观察 IP 灰度未命中(限制 2) |
| 客户端 B | 18082 | 无标签 | 常驻，服务端兜底注入 `CLIENT_IP`，参与 IP 维度灰度 |
| 临时客户端 C | 18083 | `env=pre` | 用例 1 按需启动，验证初始拉取命中/回落 |

## 配置内容约定

| 内容 | 含义 |
|------|------|
| `normal-content-v1` | 全量基线内容 |
| `gray-content-v2` | 灰度版本内容 |

## 已知限制(验证时关注，非 SDK 缺陷)

- **限制 1**：自定义标签灰度不触发长轮询 watch 实时推送。常驻客户端不会收到变更通知，需新启动客户端(本脚本通过新启动临时客户端 C)才能验证命中。
- **限制 2**：客户端配置自定义标签后，服务端不再兜底注入 `CLIENT_IP`，按 IP 配置的灰度规则不会命中带自定义标签的客户端(用例 2.2 验证)。
- **限制 3**：`ConfigGroupAPI` 拉取的文件列表反映全量版本，与命中灰度客户端实际使用内容可能不一致(本测试不涉及分组接口)。
- **限制 4**：停止灰度不会向已灰度客户端推送回落通知，已灰度客户端保持灰度内容，需重新拉取才回落全量(用例 2.3 验证)。

## 运行流程

```bash
cd examples/configuration/gray
chmod +x gray-test.sh cleanup.sh

# 完整验证(用例 1 + 用例 2)
./gray-test.sh --polaris-server 127.0.0.1

# 仅运行自定义标签灰度用例
./gray-test.sh --polaris-server 127.0.0.1 --case 1

# debug 模式
./gray-test.sh --polaris-server 127.0.0.1 --debug

# 测试后清理
./cleanup.sh -f
```

脚本在每个灰度发布/停止操作前会暂停，提示在控制台完成对应操作后按 Enter 继续。

## 用例编号

### 基线检查(步骤 4/5)

- **操作**：启动常驻客户端 A、B，拉取配置文件
- **预期**：两客户端均获取到全量内容 `normal-content-v1`
- **判定**：`/config` 接口返回 content 为 `normal-content-v1`

### [用例 1.1] 灰度命中

- **操作**：控制台发布灰度(内容 `gray-content-v2`，规则 `env EXACT pre`)，新启动临时客户端 C(端口 18083，标签 `env=pre`)
- **预期**：临时客户端 C 初始拉取携带 `env=pre` 标签命中灰度，获取 `gray-content-v2`
- **判定**：临时客户端 C 的 `/config` 返回 content 为 `gray-content-v2`

### [用例 1.2] 灰度未命中

- **操作**：同用例 1.1(灰度已发布)，常驻客户端 A、B 不重启
- **预期**：客户端 B(无标签)未命中灰度，继续使用全量内容 `normal-content-v1`
- **判定**：客户端 B 的 `/config` 返回 content 为 `normal-content-v1`
- **说明**：因限制 1，自定义标签灰度不推送，常驻客户端 A、B 不会收到变更通知

### [用例 1.3] 停止灰度

- **操作**：控制台停止灰度发布，新启动临时客户端 C(端口 18083，标签 `env=pre`)
- **预期**：临时客户端 C 初始拉取不命中灰度，获取全量内容 `normal-content-v1`
- **判定**：临时客户端 C 的 `/config` 返回 content 为 `normal-content-v1`

### [用例 2.1] IP灰度推送

- **操作**：控制台发布灰度(内容 `gray-content-v2`，规则 `CLIENT_IP EXACT <服务端视角的客户端B连接IP>`)
- **预期**：客户端 B(无标签，服务端注入 `CLIENT_IP`)命中 IP 灰度，通过 watch 实时推送获取 `gray-content-v2`
- **判定**：客户端 B 的 `/config` 在 60s 内变为 `gray-content-v2`(无需重启)
- **说明**：CLIENT_IP 由服务端从 gRPC 连接对端解析(非客户端上报)；跨 NAT 时需用服务端视角 IP，非客户端自报 localIP

### [用例 2.2] IP灰度未命中

- **操作**：同用例 2.1(IP 灰度已发布)
- **预期**：客户端 A(带 `env=pre`)因限制 2 不被注入 `CLIENT_IP`，不命中 IP 灰度，保持全量内容
- **判定**：客户端 A 的 `/config` 返回 content 为 `normal-content-v1`(若非全量需人工确认)

### [用例 2.3] 停止IP灰度

- **操作**：控制台停止 IP 维度灰度发布
- **预期**：客户端 B 保持灰度内容 `gray-content-v2`（停止灰度只清理灰度规则，不向已灰度客户端推送回落通知）
- **判定**：客户端 B 的 `/config` 仍为 `gray-content-v2`
- **说明**：停止灰度不会推送回落，已灰度客户端保持灰度内容；需重新拉取（重启 B 或新启动客户端）才回落全量 `normal-content-v1`

## 验收标准

1. 用例 1.1、1.2、1.3、2.1、2.3 全部 PASS
2. 用例 2.2 实测结果与限制 2 分析一致(客户端 A 不命中 IP 灰度)
3. 全流程 polaris-go 无任何代码改动，仅通过 `polaris.yaml` 的 `global.client.labels` 完成

## 失败排查

| 现象 | 可能原因 |
|------|----------|
| 客户端 A 未命中自定义标签灰度 | 客户端标签未生效(检查 `polaris.yaml` 的 `global.client.labels`)；控制台规则 key/value/匹配类型配置错误 |
| 客户端 B 未收到 IP 灰度推送 | CLIENT_IP 规则值非服务端视角 IP(跨 NAT 时自报 localIP 不等于服务端 peer IP)；服务端 watch 通道未推送 |
| setup 阶段发布基线失败 | 配置组 `${FILE_GROUP}` 不存在；服务端不可达或鉴权失败 |
| 客户端启动后 `/health` 不就绪 | 初始拉取失败，检查 `${LOG_DIR}/client-*.log` |
