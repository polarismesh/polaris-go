---
name: "source-command-add-plugin"
description: "Migrated source command `add-plugin`"
---

# source-command-add-plugin

Use this skill when the user asks to run the migrated source command `add-plugin`.

## Command Template

# 新增插件实现

## 使用场景
当需要在已有的插件类型下新增一个插件实现时使用（如新增一个负载均衡算法、新增一个健康检查方式）。

## 输入参数
- `$PLUGIN_TYPE`：插件类型（如 loadbalancer、healthcheck、servicerouter）
- `$PLUGIN_NAME`：插件名称（英文，如 weightedrandom、maglev）
- `$DESCRIPTION`：插件描述

## 前置检查
1. 确认插件类型已存在：检查 `pkg/plugin/$PLUGIN_TYPE/` 目录
2. 确认插件接口定义：阅读 `pkg/plugin/$PLUGIN_TYPE/$PLUGIN_TYPE.go` 中的接口
3. 参考已有实现：查看 `plugin/$PLUGIN_TYPE/` 下的其他插件实现

## 执行步骤

### 1. 创建插件实现目录
```
plugin/$PLUGIN_TYPE/$PLUGIN_NAME/
├── $PLUGIN_NAME.go    # 主实现文件（Init、Type、Name + 核心方法）
├── config.go          # 插件配置（如需要）
└── $PLUGIN_NAME_test.go  # 单元测试
```

### 2. 实现插件接口
- 嵌入 `*plugin.PluginBase`
- 实现 `Type() common.Type`、`Name() string`、`Init(ctx *plugin.InitContext) error`
- 实现插件类型特有的接口方法
- 通过 `init()` 注册：`plugin.RegisterConfigurablePlugin(&MyPlugin{}, &Config{})`

### 3. 注册插件
- 在 `plugin.cfg` 中添加一行：`$PLUGIN_NAME : $PLUGIN_TYPE/$PLUGIN_NAME`
- 在 `pkg/plugin/register/plugins.go` 中添加 `_ "github.com/polarismesh/polaris-go/plugin/$PLUGIN_TYPE/$PLUGIN_NAME"`

### 4. 编写单元测试
- 参考 `skills/dev/SKILL.md` 中的测试编写模式
- 覆盖核心逻辑、边界情况、异常处理
- 如涉及概率分布，使用大量轮次 + 容忍度验证

### 5. 验证
```bash
# 编译
go build ./...

# 运行测试
go test -gcflags=all=-l -v ./plugin/$PLUGIN_TYPE/$PLUGIN_NAME/...

# Lint
golangci-lint run --timeout=30m --new-from-rev=origin/main
```

## 完成标准
- [ ] 插件接口完整实现
- [ ] `plugin.cfg` 已注册
- [ ] `plugins.go` 已添加 import
- [ ] 单元测试通过
- [ ] Lint 通过
- [ ] 许可证头为 `//` 模板，版权行是 `Copyright (C) 2021 Tencent. All rights reserved.`
