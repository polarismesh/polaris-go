# ConfigGroupFlow 并发获取 E2E 测试

## 测试目的

验证 `ConfigGroupFlow` 的并发首次获取性能：当多个 goroutine 同时请求不同的配置分组时，应能真正并发执行，不会因 `doSync` 的全局读锁或 `GetConfigGroup` 的全局写锁导致串行阻塞。

## 测试范围

- 不同配置分组的首次 gRPC 拉取应并发执行，不应被全局锁串行化。
- 相同配置分组的并发首次获取应由 singleflight 合并，避免重复请求。
- 所有请求应成功完成，并在阈值内返回。

## 测试架构

```
verify.sh (Shell)                           main.go (Go SDK)
┌──────────────────────────┐               ┌──────────────────────────┐
│ 1. 检查配置分组是否已存在  │               │                          │
│    (OpenAPI 查询)         │               │  并发 GetConfigGroup()    │
│                          │               │  测量耗时 / 间隔          │
│ 2. 缺失则创建+发布        │  ──build──▶  │  判断 PASS/FAIL           │
│    (OpenAPI 创建)         │               │                          │
│                          │               └──────────────────────────┘
│ 3. 等待 5 秒             │
│                          │
│ 4. 编译并运行 Go 测试     │
└──────────────────────────┘
```

- **verify.sh**：负责数据准备（通过 OpenAPI 检查/创建配置分组），如果分组已存在则跳过。
- **main.go**：纯 SDK 测试逻辑，只负责并发获取和结果判定。

## 环境要求

- Go 1.24+
- bash, curl
- 可访问的 Polaris 服务端（gRPC 8091/8093 端口 + OpenAPI 8090 端口）

## 运行方式

### 方式一：verify.sh（推荐）

```bash
./verify.sh -s 127.0.0.1
```

带可选参数：

```bash
./verify.sh -s 127.0.0.1 -n default -c 20 -t 10
./verify.sh --server 10.0.0.1 --count 50 --threshold 15
./verify.sh --help
```

### 方式二：Make

```bash
make run SERVER=127.0.0.1
```

### 方式三：手动运行 Go 程序（需自行确保分组已存在）

```bash
go build -o group_concurrent_test .
./group_concurrent_test \
    -server 127.0.0.1 \
    -namespace default \
    -groups "e2e-concurrent-grp-000,e2e-concurrent-grp-001,...,e2e-concurrent-grp-019" \
    -threshold 10
```

## verify.sh 参数

| 参数 | 短选项 | 默认值 | 说明 |
|------|--------|--------|------|
| `--server` | `-s` | (必填) | Polaris 服务端 IP/主机名 |
| `--namespace` | `-n` | `default` | 使用的命名空间 |
| `--prefix` | `-p` | `e2e-concurrent-grp` | 配置分组名称前缀 |
| `--count` | `-c` | `20` | 创建/获取的配置分组数量 |
| `--threshold` | `-t` | `10` | 并发获取最大允许秒数 |
| `--help` | `-h` | — | 显示帮助信息 |

## Go 程序参数

| 参数 | 说明 |
|------|------|
| `-server` | Polaris 服务端地址 (也可通过 `POLARIS_SERVER` 环境变量设置) |
| `-namespace` | 命名空间 |
| `-groups` | 逗号分隔的配置分组名列表 |
| `-threshold` | 最大允许秒数 |

## 预期结果

```
=== E2E Test Results ===
  groups fetched             : 20
  total wall-clock           : 94.746ms
  per-call wait avg          : 92.4ms
  per-call wait max          : 94.2ms
  max gap between completions: 354µs
  errors                     : 0
  threshold                  : 10s

PASS: total 94.746ms <= threshold 10s — concurrent fetch is healthy
```

关键指标：
- **total wall-clock** ≈ 单次 gRPC 往返时间（~100ms），表明 20 个请求真正并发执行
- **max gap between completions** < 1ms，表明没有串行排队
- **errors** = 0

若出现 `FAIL: total > threshold`，说明并发获取存在串行化瓶颈。

## 关于配置分组复用

`verify.sh` 在执行前会先查询服务端是否已有对应的配置分组：
- 如果 20 个分组都已存在 → 直接跳过创建步骤
- 如果部分缺失 → 只补创建缺失的
- 如果全部缺失 → 全量创建

这意味着：
- 第一次运行会创建配置分组
- 后续运行直接复用，无需重复创建
- 如需全新测试数据，可修改 `GROUP_PREFIX` 环境变量

## 相关文件

- `verify.sh`：创建测试分组并运行验证。
- `main.go`：并发获取配置分组并输出结果。
