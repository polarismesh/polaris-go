# 实现新功能

## 使用场景
当需要为 polaris-go SDK 新增一个功能模块或插件类型时使用。

## 输入参数
- `$FEATURE_NAME`：功能名称（英文，如 lossless、warmup）
- `$DESCRIPTION`：功能描述（一句话说明该功能的作用）
- `$PLUGIN_TYPE`：插件类型（如 provider、consumer，决定挂载到哪个配置段）

## 执行步骤

### 1. 需求理解
- 确认功能的业务目标和技术边界
- 确认是否需要新增插件类型（参考 `pkg/plugin/common/plugin.go` 中已有的 Type 常量）
- 确认是否需要新增 API 方法

### 2. 阅读开发规范
- 阅读 `skills/dev/SKILL.md` 了解完整的分层开发流程
- 阅读 `rules/01-go-standard-company.md` 了解编码规范

### 3. 按分层架构开发（严格按顺序）

#### 第一层：模型层
- 在 `pkg/model/` 下创建 `$FEATURE_NAME.go`
- 定义常量、结构体、辅助方法
- 如有事件模型，在 `pkg/model/event/` 下创建对应文件

#### 第二层：配置层
- 在 `pkg/config/` 下创建 `$FEATURE_NAME.go`
- 实现配置接口（`Verify()`、`SetDefault()`、`Init()`）
- 在 `pkg/config/api.go` 中添加 Getter
- 在 `pkg/config/impl.go` 中添加字段和初始化

#### 第三层：插件接口层
- 在 `pkg/plugin/$FEATURE_NAME/` 下创建接口定义和 Proxy
- 通过 `init()` 注册接口和 Proxy

#### 第四层：插件实现层
- 在 `plugin/$FEATURE_NAME/<pluginName>/` 下实现插件
- 拆分为 `base.go`、`config.go`、`process.go` 等文件

#### 第五层：流程编排层
- 在 `pkg/flow/` 下创建 `$FEATURE_NAME_flow.go`
- 在 `pkg/flow/impl.go` 中集成

#### 第六层：SDK 引擎接口层
- 在 `pkg/sdk/engine.go` 中添加方法声明

#### 第七层：API 层
- 内部 API：`api/` 目录
- 外部 API：根目录 `api.go` + `api_$PLUGIN_TYPE.go`

#### 第八层：插件注册
- 在 `pkg/plugin/register/plugins.go` 中添加 import

#### 第九层：日志扩展（如需要）
- 在 `pkg/log/` 下扩展日志

#### 第十层：配置文件
- 在 `polaris.yaml` 中添加默认配置段

### 4. 编写单元测试
- 在插件实现目录下创建 `_test.go` 文件
- 覆盖正常流程、边界情况、异常处理

### 5. 编写使用示例
- 在 `examples/$FEATURE_NAME/` 下创建示例代码

### 6. 验证
```bash
# 编译检查
go build ./...

# 单元测试
go test -gcflags=all=-l -v ./plugin/$FEATURE_NAME/...

# Lint 检查
git fetch origin main
golangci-lint run --timeout=30m --new-from-rev=origin/main

# 格式化
go fmt ./...
bash import-format.sh
```

### 7. 提交
- 提交信息格式：`feat: $FEATURE_NAME`
- 大功能可以一次提交，确保所有文件完整

## 完成标准
- [ ] 所有分层文件已创建
- [ ] 编译通过（`go build ./...`）
- [ ] 单元测试通过
- [ ] Lint 检查通过
- [ ] 每个 `.go` 文件（排除 `*.pb.go`）包含 `//` 许可证头（`Copyright (C) 2021 Tencent. All rights reserved.`）
- [ ] 导出的类型和方法都有注释
