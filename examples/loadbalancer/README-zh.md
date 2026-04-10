# Polaris Go

[English](./README.md) | 中文

## 使用负载均衡功能

快速体验北极星的负载均衡能力。本示例为每种负载均衡算法提供了独立的 consumer 目录。

## 目录结构

```
loadbalancer/
├── provider/          # 公共的 Provider（服务提供者）
├── hash/              # 普通哈希负载均衡 (hash)
├── l5cst/             # L5一致性哈希负载均衡 (l5cst)
├── maglev/            # Maglev哈希负载均衡 (maglev)
├── ringhash/          # 一致性哈希环负载均衡 (ringHash)
├── weightedRandom/    # 权重随机负载均衡 (weightedRandom)
├── verify_weight.sh   # 权重验证脚本
└── cleanup.sh         # 清理脚本
```

## 如何使用

### 构建可执行文件

构建 provider

```bash
cd ./provider
go build -o provider
```

构建某种算法的 consumer（以 weightedRandom 为例）

```bash
cd ./weightedRandom
go build -o consumer
```

### 进入控制台

预先通过北极星控制台创建对应的服务，如果是通过本地一键安装包的方式安装，直接在浏览器通过127.0.0.1:8080打开控制台

### 修改配置

指定北极星服务端地址，需编辑各目录下的 polaris.yaml 文件，填入服务端地址。

每个算法目录的 polaris.yaml 已预配置了对应的负载均衡策略，例如 `hash/polaris.yaml`：

```yaml
consumer:
  loadbalancer:
    type: hash
```

### 执行程序

运行构建出的 **provider** 可执行文件

在不同的节点运行多个 provider，或通过 `--port` 参数指定不同端口，在同一个节点运行多个 provider

```bash
./provider/provider
```

运行构建出的 **consumer** 可执行文件（以 weightedRandom 为例）

```bash
./weightedRandom/consumer
```

各算法目录的 consumer 默认使用对应的负载均衡策略，也可通过命令行参数覆盖：

```bash
# 哈希类算法需要指定 hashKey
./hash/consumer --lbPolicy hash --hashKey "my-key"

# 权重随机算法
./weightedRandom/consumer --lbPolicy weightedRandom
```

### 支持的负载均衡算法

| 目录 | 算法名称 | 说明 | 是否需要 hashKey |
|------|---------|------|-----------------|
| `hash/` | hash | 普通哈希 | ✅ 是 |
| `l5cst/` | l5cst | L5一致性哈希 | ✅ 是 |
| `maglev/` | maglev | Maglev哈希 | ✅ 是 |
| `ringhash/` | ringHash | 一致性哈希环 | ✅ 是 |
| `weightedRandom/` | weightedRandom | 权重随机 | ❌ 否 |

### 使用验证脚本

可以使用 `verify_weight.sh` 脚本自动化验证负载均衡效果：

```bash
# 验证 weightedRandom 算法（默认）
./verify_weight.sh

# 验证指定算法
./verify_weight.sh --lb-policy hash
./verify_weight.sh --lb-policy ringHash
./verify_weight.sh --lb-policy maglev
```

### 验证

请求被负载均衡到各个 provider

```
curl http://127.0.0.1:17080/echo
Hello, I'm LoadBalanceEchoServer Provider, My host : 10.10.0.10:32451

curl http://127.0.0.1:17080/echo
Hello, I'm LoadBalanceEchoServer Provider, My host : 10.10.0.11:31102

curl http://127.0.0.1:17080/echo
Hello, I'm LoadBalanceEchoServer Provider, My host : 10.10.0.10:32451
```
