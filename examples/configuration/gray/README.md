# 配置灰度发布验证示例

## 功能说明

本示例验证 polaris-go 客户端参与北极星配置灰度发布的能力。

灰度发布指在修改配置发布时，按一定规则选择一小部分机器，将修改后的配置先下发到这些机器小范围验证，确认无问题后再全量下发。

**关键结论**：polaris-go 侧无需任何代码改动。灰度规则的匹配与灰度内容的下发完全由服务端承担，客户端只需在 `global.client.labels` 中上报标签，SDK 会在拉取配置时将标签携带到 `GetConfigFile` 请求中：

| 环节 | 责任方 | polaris-go 现状 |
|------|--------|----------------|
| 上报客户端标签 | 客户端 | 已实现(`global.client.labels` → `Tags`) |
| 判定是否命中灰度规则 | 服务端 | 不参与 |
| 决定返回灰度还是全量内容 | 服务端 | 不参与 |
| 消费返回的配置内容 | 客户端 | 已实现(协议无新增字段) |

## 目录结构

```
gray/
├── main.go          # demo 主程序(setup/run 两种模式)
├── gray-test.sh     # 端到端验证脚本
├── cleanup.sh       # 残留进程与构建产物清理
├── test.md          # 验证用例文档(与 gray-test.sh 同步)
├── polaris.yaml     # 客户端配置(默认携带 env: pre 标签)
├── Makefile
└── go.mod
```

## demo 程序模式

`main.go` 提供两种运行模式：

| 模式 | 说明 | 关键参数 |
|------|------|----------|
| `setup` | 创建并发布一份全量基线配置后退出 | `-content` 指定写入内容 |
| `run`(默认) | 作为配置客户端常驻运行，拉取配置并订阅变更 | `-port` 指定 HTTP 监听地址 |

`run` 模式暴露的 HTTP 接口(供验证脚本观察当前生效内容)：

| 接口 | 说明 |
|------|------|
| `GET /health` | 健康检查，初始拉取完成后返回 200 |
| `GET /config` | 返回当前生效配置快照(JSON): content/version/md5/labels/localIP |

## 使用方法

### 一键验证(推荐)

```bash
cd examples/configuration/gray
chmod +x gray-test.sh cleanup.sh

# 完整验证(自定义标签灰度 + IP 维度灰度)
./gray-test.sh --polaris-server 127.0.0.1

# 测试后清理
./cleanup.sh -f
```

脚本会自动完成：编译 → 发布全量基线 → 启动两个客户端(一个带 `env=pre` 标签，一个无标签) → 引导你在控制台发布/停止灰度 → 自动校验各客户端生效内容。详细用例见 [test.md](./test.md)。

### 手动运行单个客户端

```bash
# 1. 修改 polaris.yaml 中的服务端地址与 token
# 2. 发布全量基线
go run . -action=setup -content="normal-content-v1"

# 3. 作为客户端运行(默认携带 env: pre 标签)
go run . -action=run -port=:18081

# 4. 在控制台发布灰度后，观察生效内容
curl http://127.0.0.1:18081/config
```

## 验证用例概览

| 用例 | 场景 | 预期 |
|------|------|------|
| 1.1 | 自定义标签灰度(规则 `env EXACT pre`) | 带 `env=pre` 标签的客户端获取灰度内容 |
| 1.2 | 同上 | 无标签客户端继续使用全量内容 |
| 1.3 | 停止灰度 | 客户端回落到全量内容 |
| 2.1 | IP 维度灰度(规则 `CLIENT_IP EXACT <ip>`) | 命中客户端通过 watch 实时推送获取灰度内容 |
| 2.2 | 同上 | 带自定义标签的客户端因服务端不注入 `CLIENT_IP` 不命中 |
| 2.3 | 停止 IP 灰度 | 客户端通过 watch 推送回落到全量内容 |

## 已知限制

详见 [test.md](./test.md) 的「已知限制」章节：

- 自定义标签灰度不触发 watch 实时推送(需客户端重新拉取生效)
- 客户端配置自定义标签后，服务端不再兜底注入 `CLIENT_IP`
- `ConfigGroupAPI` 文件列表不感知灰度
