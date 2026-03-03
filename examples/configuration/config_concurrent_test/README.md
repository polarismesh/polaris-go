# 配置中心分段锁优化验证测试

## 概述

本测试程序用于验证配置中心 `GetConfigFile` 方法中分段锁优化的效果。通过模拟高并发场景下获取不同FileName的配置，验证分段锁是否能有效提升并发性能。

## 优化背景

在优化前，配置中心使用全局锁 `fclock` 来保护所有配置文件的获取操作，导致不同FileName的配置获取会相互阻塞，成为性能瓶颈。

优化后，采用分段锁机制：
- 使用16个分段锁，根据配置文件名进行哈希分配
- 不同FileName的配置获取使用不同的锁，互不干扰
- 支持真正的并发获取不同文件的配置

## 测试程序功能

- 模拟多线程并发获取不同配置文件的场景
- 支持自定义线程数、文件数和测试时长
- 实时统计成功率和性能指标
- 自动分析优化效果

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

### 优化成功指标

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

### 分段锁优化验证

1. **并发性验证**：不同FileName的配置获取应该能够并发执行
2. **锁竞争消除**：不应该出现全局锁竞争导致的性能瓶颈
3. **性能提升**：相比优化前，并发性能应该有显著提升

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

## 性能对比

| 场景 | 优化前 | 优化后 |
|------|--------|--------|
| 单文件高并发 | 性能差（锁竞争） | 性能相同 |
| 多文件高并发 | 性能差（锁竞争） | 性能显著提升 |
| 缓存命中率 | 无影响 | 无影响 |

## 总结

通过本测试程序，可以验证分段锁优化是否有效解决了配置获取的并发性能问题。如果测试结果显示高成功率和高并发性能，说明优化已成功生效。