# 配置灰度云上验证

> 本文档与 `build-materials.sh` / `verify-cloud.sh` / `templates/` 配套，用于在云上多节点环境验证 polaris-go 配置灰度发布能力。

## 测试环境部署情况

- 3 个客户端节点：client-a / client-b / client-c（各部署一份物料，参见下文拓扑）
- polaris-server 已云端部署（地址记为 `<POLARIS_SERVER>`，config gRPC 端口 8093，naming gRPC 端口 8091）

## 环境与拓扑

| 组件 | 部署 | 客户端标签 | 端口 | 角色 |
| --- | --- | --- | --- | --- |
| client-a | 节点 A | `env=pre` | 18081 | 常驻，观察自定义标签灰度不推送（限制 1）、IP 灰度未命中（限制 2） |
| client-b | 节点 B | 无 | 18082 | 常驻 + setup 全量基线，观察 IP 维度灰度 watch 推送 |
| client-c | 节点 C | `env=pre` | 18083 | 临时启动，验证初始拉取命中/回落自定义标签灰度 |

> 自定义标签灰度因 watch 通道不携带 Tags（限制 1），常驻客户端 A 收不到实时推送，需新启动客户端 C 走「初始拉取」路径验证命中。IP 维度灰度 watch 通道有 `CLIENT_IP`，常驻客户端 B 可实时收到推送。

## 物料生成

在开发机（需 Go 环境）执行 `build-materials.sh`，交叉编译 gray-demo 为 Linux x86_64 二进制，生成 `dist/` 下 3 个节点的部署物料：

```shell
cd examples/configuration/gray/cloud
chmod +x build-materials.sh
./build-materials.sh
```

生成物：

```
cloud/dist/
├── client-a/   x86-bin + polaris.yaml(env:pre) + client.sh + clean.sh
├── client-b/   x86-bin + polaris.yaml(无标签)  + client.sh + clean.sh
└── client-c/   x86-bin + polaris.yaml(env:pre) + client.sh + clean.sh
```

每个 `polaris.yaml` 用 `${POLARIS_SERVER}` / `${POLARIS_TOKEN}` 占位，运行时由 `client.sh` 注入环境变量展开（SDK 自动展开 `${VAR}`）。

`build-materials.sh` 已为每个节点生成 zip 包（`cloud/client-a.zip` 等），直接上传到对应节点：

```shell
scp cloud/dist/client-a.zip <A节点>:~/
scp cloud/dist/client-b.zip <B节点>:~/
scp cloud/dist/client-c.zip <C节点>:~/
# 节点上解压
unzip client-a.zip && cd client-a
```

## 节点操作

每个节点物料目录含 `client.sh`（启动脚本，子命令 `setup/start/stop/status/restart`）与 `clean.sh`（清理脚本）。

### 1. client-b 节点（发布全量基线 + 常驻）

```shell
cd client-b
# 发布全量基线（已存在且内容一致则跳过，避免服务端 409 conflict）
./client.sh setup --polaris-server <POLARIS_SERVER> --content normal-content-v1
# 启动常驻客户端 B（无标签，服务端兜底注入 CLIENT_IP）
./client.sh start --polaris-server <POLARIS_SERVER> --port 18082
./client.sh status --port 18082
```

鉴权：`POLARIS_TOKEN=xxx ./client.sh start --polaris-server <POLARIS_SERVER> --port 18082`
DEBUG：加 `--debug`

### 2. client-a 节点（常驻，带 env=pre）

```shell
cd client-a
./client.sh start --polaris-server <POLARIS_SERVER> --port 18081
./client.sh status --port 18081
```

### 3. client-c 节点（临时，用例 1 按需启动）

```shell
cd client-c
# 用例 1.1：发布自定义标签灰度后启动，验证初始拉取命中
./client.sh start --polaris-server <POLARIS_SERVER> --port 18083
./client.sh status --port 18083
# 用例 1.3：停止灰度后重启验证回落
./client.sh stop && ./client.sh start --polaris-server <POLARIS_SERVER> --port 18083
# 验证完停止
./client.sh stop
```

## 云上验证编排

在能访问三个客户端节点的机器上执行 `verify-cloud.sh`，curl 各节点 `/config` 对比生效内容，并引导在控制台发布/停止灰度、在各节点 `client.sh start/stop`：

```shell
cd examples/configuration/gray/cloud
./verify-cloud.sh \
    --a <A节点IP>:18081 \
    --b <B节点IP>:18082 \
    --c <C节点IP>:18083 \
    --polaris-server <POLARIS_SERVER>
```

可选：`--case 1` 仅自定义标签灰度；`--case 2` 仅 IP 维度灰度；默认 `all`。

执行流程：
1. 验证基线：A、B 均为 `normal-content-v1`
2. 用例 1（自定义标签灰度）：
   - 提示控制台发布灰度（`env EXACT pre`，内容 `gray-content-v2`）
   - 提示 C 节点 `client.sh start`
   - 验证 C 命中灰度（`gray-content-v2`）、A 保持全量（限制 1，不推送）
   - 提示控制台停止灰度
   - 提示 C 节点重启，验证回落全量
3. 用例 2（IP 维度灰度）：
   - 打印 B 自报 IP 作参考，提示从服务端日志/控制台获取实际连接 IP
   - 提示控制台发布灰度（`CLIENT_IP EXACT <服务端视角IP>`）
   - 验证 B 通过 watch 实时推送获取灰度（60s 内）、A 保持全量（限制 2）
   - 提示控制台停止灰度，验证 B 保持灰度内容（停止灰度不推送回落，限制 4；需重新拉取才回落）
4. 汇总结论

> `verify-cloud.sh` 不 ssh，只 curl `/config` 对比 + 引导提示；各节点 `start/stop` 由操作者手动执行。

## 清理

### 各节点清理

```shell
cd client-a  # 或 client-b / client-c
./clean.sh            # 默认: 展示后确认再清理
./clean.sh -f         # 强制直接清理
./clean.sh --dry-run  # 仅展示
```

清理内容：`client.pid` 进程（pidfile + ps 兜底搜 `x86-bin`）+ `client.log` + `polaris/` SDK 日志目录。

### 开发机物料清理

```shell
cd examples/configuration/gray/cloud
./build-materials.sh --clean   # 清理 dist/ 与临时 x86-bin
# 或
./clean-cloud.sh -f
```

## 已知限制（非 SDK 缺陷）

- **限制 1**：自定义标签灰度不触发 watch 实时推送（watch 通道不携带 Tags）。常驻客户端 A 收不到推送，需新启动客户端 C 走初始拉取验证命中。详见 [test.md](../test.md)。
- **限制 2**：客户端配置自定义标签后，服务端不再兜底注入 `CLIENT_IP`，按 IP 配置的灰度规则不会命中带自定义标签的客户端（用例 2.2 验证）。
- **CLIENT_IP 来源**：服务端从 gRPC 连接对端地址解析（`peer.FromContext`），**非客户端上报**；跨 NAT 时需用服务端视角 IP，非客户端自报 `localIP`。
- **限制 4**：停止灰度不会向已灰度客户端推送回落通知，已灰度客户端保持灰度内容，需重新拉取才回落全量（用例 2.3 验证）。

## 注意事项

- **基线冲突**：若服务端存在活跃灰度发布，`client.sh setup` 的 `PublishConfigFile` 会返回 409 `data is conflict`（灰度存在时禁止发全量）。需先在控制台停止灰度再 setup。
- **鉴权**：polaris-server 开启鉴权时，所有 `client.sh` 需 `POLARIS_TOKEN` 环境变量。
- **网络**：执行 `verify-cloud.sh` 的机器需能经 VPC 访问三个客户端节点的 18081/18082/18083 端口。
- **CLIENT_IP 规则值**：用例 2 的 IP 灰度规则值必须用服务端视角的连接 IP，不能用 `verify-cloud.sh` 打印的客户端自报 IP（除非同机/同局域网）。
