# 配置生效查询云上验证

云上部署与验证物料，把 config-effect-demo 打包为单个 zip，上传到云节点直接运行二进制并验证配置生效查询。

## 目录结构

```
cloud/
├── build-materials.sh    生成部署物料(交叉编译 + 组装 + 打 zip)
├── cloud-clean.sh        清理物料产物(dist/、zip、临时二进制)
├── README.md             本文件
└── templates/            物料模板
    ├── polaris.yaml      配置模板(${POLARIS_SERVER}/${POLARIS_TOKEN} 占位)
    ├── client.sh         节点启动脚本(setup/start/stop/status/restart)
    ├── verify-cloud.sh   配置生效查询验证脚本(调服务端 maintain 接口)
    └── clean.sh          节点清理脚本
```

## 生成物料

```bash
cd cloud
./build-materials.sh            # 生成 dist/client/ + dist/client.zip
./build-materials.sh --clean    # 清理
```

生成 `dist/client.zip`(约 13MB)，内含:

| 文件 | 说明 |
|------|------|
| `x86-bin` | config-effect-demo 预编译 Linux x86_64 静态二进制 |
| `polaris.yaml` | 配置模板(`POLARIS_SERVER`/`POLARIS_TOKEN` 环境变量占位) |
| `client.sh` | 启动/停止/状态/基线发布脚本 |
| `verify-cloud.sh` | 配置生效查询验证脚本 |
| `clean.sh` | 节点清理脚本 |

## 云上部署与验证

上传 `client.zip` 到云节点后:

```bash
unzip client.zip && cd client

# 1. 发布 3 份全量基线配置(base name 派生 -1/-2/-3.yaml，已存在则跳过)
POLARIS_TOKEN=xxx ./client.sh setup --polaris-server <服务端地址> --content effect-content-v

# 2. 启动常驻客户端(自动订阅配置 + 建 WatchClientEvents 长连接 + 暴露 HTTP)
POLARIS_TOKEN=xxx ./client.sh start --polaris-server <服务端地址> --port 18091

# 3. 查看状态(显示 clientID 与本地生效配置)
./client.sh status --port 18091

# 4. 执行配置生效查询验证(调服务端 maintain 接口，校验 ACK)
POLARIS_TOKEN=xxx ./verify-cloud.sh --polaris-server <服务端地址> \
    --maintain-port 8090 --client-port 18091

# 5. 停止与清理
./client.sh stop
./clean.sh -f
```

## 验证原理

```
                ┌───────────────┐   ReportClient(clientID)    ┌──────────────┐
                │   client      │ ──────────────────────────► │              │
                │ (config-effect│   WatchClientEvents(WATCH)  │              │
                │   -demo)      │ ◄──────────────────────────► │  Polaris     │
                │               │   PUSH ──────────────────►  │  服务端      │
                │ 订阅配置文件   │   ◄──────────────── ACK     │  (商业版)    │
                └────┬──────────┘                             └──────┬───────┘
                     │ /clientid /config                             │
                     ▼                                               ▼
                ┌──────────────────────────────────────────────────────────┐
                │              verify-cloud.sh                            │
                │  1. 读客户端 /clientid 与 /config (version/md5/content) │
                │  2. 调服务端 maintain 接口 PUSH 配置生效查询              │
                │  3. 解析返回的 ACK content                              │
                │  4. 断言 applied=true 且 version/md5/content 一致        │
                └──────────────────────────────────────────────────────────┘
```

## 前置条件

1. 北极星服务端(商业版)已启动，且已实现 `WatchClientEvents` 接口
2. 服务端 maintain HTTP 端口可达(默认 `8090`，可用 `--maintain-port` 指定)
3. 云节点有执行权限与 `curl`/`python3`(`verify-cloud.sh` 依赖)
4. 配置文件组 `polaris-config-example` 已存在或客户端有创建权限

## 故障排查

| 现象 | 可能原因 |
|------|----------|
| `client.sh start` 进程立即退出 | polaris.yaml 地址/token 错误；查 `client.log` |
| `verify-cloud.sh` 报"无 clientEvent.content" | WatchClientEvents 长连接未建立，查 `client.log` 是否有 `stream established` |
| `applied=false` | 客户端未订阅该配置文件(检查 `client.sh status` 的 version/md5 非空) |
| version/md5 不一致 | 客户端在 PUSH 时配置刚变更，重跑 `verify-cloud.sh` |
| maintain 接口鉴权失败 | `--polaris-token` 未传或无效 |
| `client_info.json` 残留 | 升级后持久化文件按 clientID 分文件，旧文件需 `clean.sh -f` 清理 |
