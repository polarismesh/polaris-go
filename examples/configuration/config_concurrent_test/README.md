# 配置中心并发获取验证测试

## 概述

本测试程序用于验证配置中心并发获取不同配置文件时的行为。通过模拟高并发场景，
检查不同 `FileName` 的请求能否并行执行，以及成功率、吞吐和锁竞争情况。

## 测试程序功能

- 模拟多线程并发获取不同配置文件的场景
- 支持自定义线程数、文件数和测试时长
- 实时统计成功率和性能指标
- 统计成功率、吞吐和锁竞争情况

## 使用方法

### 1. 快速验证

```bash
# 运行验证脚本（推荐）
chmod +x verify.sh
./verify.sh
```

### 2. 手动运行

```bash
# 编译程序
go build -o config_concurrent_test main.go

# 运行测试
./config_concurrent_test \
    -namespace=default \
    -service=config-service \
    -fileCount=100 \
    -threads=50 \
    -duration=30
```

### 3. 参数说明

| 参数 | 说明 | 默认值 |
|------|------|---------|
| `-namespace` | 命名空间 | `default` |
| `-service` | 服务名 | 必填 |
| `-fileCount` | 配置文件数量 | `100` |
| `-threads` | 并发线程数 | `50` |
| `-duration` | 测试时长（秒） | `30` |

## 预期结果

### 预期结果

1. **高成功率**：所有操作应该成功完成，错误数为0
2. **高并发性能**：每秒操作数应该较高，表示并发性能良好
3. **稳定运行**：测试期间不应该出现性能抖动或错误

### 典型输出示例

```
=== Test Results ===
Total operations: 15000
Successful operations: 15000
Failed operations: 0
Success rate: 100.00%
Operations per second: 500.00
✅ Test PASSED - All operations completed successfully
```

## 验证要点

### 并发行为验证

1. **并发性验证**：不同FileName的配置获取应该能够并发执行
2. **锁竞争消除**：不应该出现全局锁竞争导致的性能瓶颈
3. **结果稳定**：成功率和吞吐应在测试期间保持稳定

### 错误排查

如果测试失败，请检查：

1. **配置文件服务**：确保配置中心服务正常运行
2. **网络连接**：检查客户端与服务端的网络连接
3. **配置存在性**：确保测试的配置文件在服务端存在
4. **权限设置**：检查是否有足够的访问权限

## 技术实现

### 分段锁机制

```go
// 分段锁设计
shardLocks := make([]sync.RWMutex, 16)
shardLockCount := 16

// 根据文件名哈希选择锁
func (c *ConfigFileFlow) getShardIndex(cacheKey string) int {
    hash := fnv.New32a()
    hash.Write([]byte(cacheKey))
    return int(hash.Sum32()) % c.shardLockCount
}
```

### 锁粒度优化

- **细粒度锁**：每个配置文件使用独立的锁
- **读锁优化**：缓存检查使用读锁，支持并发读取
- **写锁保护**：缓存更新使用写锁，保证数据一致性

## 总结

如果测试结果显示高成功率、稳定吞吐和较低等待时间，说明不同配置文件的并发获取正常。
