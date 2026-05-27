# ConfigGroupFlow 并发获取 E2E 测试

## 测试目的

验证 `ConfigGroupFlow` 的并发首次获取性能：当多个 goroutine 同时请求不同的配置分组时，应能真正并发执行，不会因 `doSync` 的全局读锁或 `GetConfigGroup` 的全局写锁导致串行阻塞。

## 背景

修复前存在双重串行瓶颈：

1. **`doSync` 在 `RLock` 内串行拉取所有已有分组**：当 `repos` 中有 N 个分组时，每次 `doSync` 持有 `fclock.RLock` 约 `N × 35ms`，期间所有新的 `GetConfigGroup` 写锁请求被阻塞。
2. **`GetConfigGroup` 在写锁内执行 gRPC**：即使 `doSync` 不阻塞，并发请求也只能逐个通过。

修复后：
- `doSync` 只在快照 `repos` 列表时短暂持有 `RLock`（亚毫秒），gRPC 拉取在锁外并发执行。
- `GetConfigGroup` 的 gRPC 调用移到锁外，使用 `inFlight` map 实现 singleflight 去重。

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

- Go 1.21+
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

- `pkg/flow/configuration/group_flow.go` — 修复的核心代码
- `examples/configuration/group/repro/` — 单元级复现测试（使用 mock connector，无需服务端）
- `slow-concurrent-fetching-of-config-group.md` — 问题根因分析文档
