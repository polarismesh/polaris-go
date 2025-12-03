# Polaris Go SDK Examples

本目录包含了 Polaris Go SDK 的各种使用示例，帮助开发者快速了解和使用 Polaris 的各项功能。

## 📚 示例类型

| 示例目录 | 功能描述 | 包含内容 |
|---------|---------|---------|
| **quickstart** | 快速入门示例 | 基础的服务提供者(provider)和消费者(consumer)示例，以及K8s部署示例 |
| **configuration** | 配置管理示例 | 配置的增删改查(crud)、加密配置(encrypt)、配置回退(fallback)、配置分组(group)、普通配置(normal) |
| **circuitbreaker** | 熔断器示例 | 被调用方(callee)、实例级熔断(instance)、接口级熔断(interface)、服务级熔断(service) |
| **ratelimit** | 限流示例 | 限流消费者(consumer)和多个限流提供者(provider-a/provider-b)示例 |
| **loadbalancer** | 负载均衡示例 | 负载均衡消费者(consumer)和提供者(provider)示例 |
| **route** | 路由示例 | 基于元数据路由(metadata)、就近路由(nearby)、规则路由(rule) |
| **activehealthcheck** | 主动健康检查示例 | 主动健康检查的配置和使用 |
| **watch** | 监听示例 | 服务实例变化监听功能 |
| **watchinstance** | 实例监听示例 | 特定实例状态变化监听 |
| **mock** | 模拟示例 | 用于测试的模拟服务示例 |

## 🛠️ 管理脚本

本目录还提供了四个便捷的管理脚本，用于批量管理所有示例模块：

### 脚本功能对比

| 脚本名称 | 主要功能 | 使用场景 | 执行效果 |
|---------|---------|---------|---------| | **check_versions.sh** | 版本一致性检查 | 检查所有模块的依赖版本是否一致 | 显示版本分布统计，识别版本不一致问题 |
| **update_polaris_versions.sh** | 批量版本更新 | 统一更新所有模块的polaris-go和Go版本 | 更新require依赖版本，忽略replace指令 |
| **batch_mod_tidy.sh** | 批量依赖整理 | 清理和整理所有模块的依赖关系 | 执行go mod tidy，统计成功/失败数量 |
| **batch_make_clean.sh** | 批量清理构建 | 清理所有子目录的构建产物 | 递归执行make clean，统计成功/失败数量 |

### 详细使用说明

#### 1. check_versions.sh - 版本检查脚本

**功能**：检查项目中所有子模块的polaris-go版本和Go版本是否一致

**使用方法**：
```bash
# 给脚本执行权限
chmod +x check_versions.sh

# 执行版本检查
./check_versions.sh
```

**输出内容**：
- 📦 各模块的polaris-go和Go版本详情
- 📊 版本分布统计
- ✅/⚠️ 版本一致性检查结果

**特点**：
- 只识别 `require` 依赖的polaris-go版本
- 忽略 `replace` 本地替换指令
- 提供详细的版本分布统计

#### 2. update_polaris_versions.sh - 版本更新脚本

**功能**：批量更新所有子模块的polaris-go版本和Go版本

**使用方法**：
```bash
# 给脚本执行权限
chmod +x update_polaris_versions.sh

# 使用默认版本更新 (polaris-go v1.6.1, Go 1.24)
./update_polaris_versions.sh

# 指定版本更新
./update_polaris_versions.sh v1.7.0 1.25
```

**参数说明**：
- 第1个参数：polaris-go版本 (默认: v1.6.1)
- 第2个参数：Go版本 (默认: 1.24)

**执行流程**：
1. 更新Go版本
2. 更新polaris-go的require依赖版本
3. 执行 `go mod tidy` 和 `go mod download`
4. 验证更新结果

**特点**：
- 只更新 `require` 依赖，保留 `replace` 指令
- 自动执行依赖整理和下载
- 提供详细的执行日志

#### 3. batch_mod_tidy.sh - 批量依赖整理脚本

**功能**：批量执行 `go mod tidy` 整理所有子模块的依赖关系

**使用方法**：
```bash
# 给脚本执行权限
chmod +x batch_mod_tidy.sh

# 执行批量整理
./batch_mod_tidy.sh
```

**执行特点**：
- 🚀 自动发现所有包含go.mod的子目录
- 📁 跳过根目录的go.mod文件
- ✅/❌ 实时显示每个模块的执行状态
- 📊 提供执行结果统计

**输出信息**：
- 处理进度和状态
- 成功/失败统计
- 详细的使用说明

#### 4. batch_make_clean.sh - 批量清理构建脚本

**功能**：批量执行 `make clean` 清理所有子目录的构建产物

**使用方法**：
```bash
# 给脚本执行权限
chmod +x batch_make_clean.sh

# 执行批量清理
./batch_make_clean.sh
```

**执行特点**：
- 🚀 自动发现所有包含Makefile的子目录
- 📁 跳过根目录的Makefile文件
- 🧹 递归执行make clean清理构建产物
- ✅/❌ 实时显示每个模块的执行状态
- 📊 提供执行结果统计

**输出信息**：
- 处理进度和状态
- 成功/失败统计
- 详细的使用说明

## 🚀 快速开始

1. **选择示例**：根据需要的功能选择对应的示例目录
2. **查看文档**：每个示例目录都包含详细的README文档
3. **运行示例**：按照各示例的说明运行代码
4. **版本管理**：使用提供的脚本工具进行版本检查和更新

## 📝 注意事项

- 运行示例前请确保已正确配置Polaris服务端
- 建议先运行 `check_versions.sh` 检查版本一致性
- 如需更新版本，请使用 `update_polaris_versions.sh` 统一更新
- 定期使用 `batch_mod_tidy.sh` 整理依赖关系
- 使用 `batch_make_clean.sh` 可以快速清理所有子目录的构建产物

## 🔗 相关链接

- [Polaris 官方文档](https://polarismesh.cn/)
- [Polaris Go SDK 文档](https://github.com/polarismesh/polaris-go)