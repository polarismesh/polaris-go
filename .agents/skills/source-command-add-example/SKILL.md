---
name: "source-command-add-example"
description: "Migrated source command `add-example`"
---

# source-command-add-example

Use this skill when the user asks to run the migrated source command `add-example`.

## Command Template

# 添加使用示例

## 使用场景
当需要为某个功能添加使用示例（example）时使用。

## 输入参数
- `$FEATURE_NAME`：功能名称（如 lossless、loadbalancer）
- `$SCENARIO`：示例场景（如 consumer、provider）

## 执行步骤

### 1. 参考已有示例
- 查看 `examples/` 目录下的已有示例结构
- 参考 `skills/dev/SKILL.md` 中的 Examples 编写模式

### 2. 创建目录结构
```
examples/$FEATURE_NAME/
├── $SCENARIO/
│   ├── Makefile
│   ├── go.mod          # 独立 module，使用 replace 指向本地
│   ├── go.sum
│   ├── main.go
│   └── polaris.yaml    # 示例配置
├── cleanup.sh          # 清理脚本（可选）
├── verify_xxx.sh       # 验证脚本（可选）
└── README.md           # 示例说明
```

### 3. 编写 main.go
- 使用 `flag` 解析命令行参数
- 定义 `PolarisClient` 结构体封装 API
- 包含 `initArgs()`、`main()` 函数
- 包含优雅退出逻辑（signal 监听）

### 4. 编写 go.mod
```go
module github.com/polarismesh/polaris-go/examples/$FEATURE_NAME/$SCENARIO

go 1.21

require github.com/polarismesh/polaris-go v1.7.1-snapshot

replace github.com/polarismesh/polaris-go => ../../../
```

### 5. 编写 polaris.yaml
- 包含该功能所需的最小配置
- 使用合理的默认值

### 6. 验证
```bash
# 编译示例
cd examples/$FEATURE_NAME/$SCENARIO
go build -o example .

# 确认可运行（可能需要 Polaris 服务端）
./example --help
```

## 完成标准
- [ ] 目录结构完整
- [ ] go.mod 使用 replace 指向本地
- [ ] main.go 可编译运行
- [ ] 包含必要的配置文件
- [ ] 代码包含 `//` 许可证头（`Copyright (C) 2021 Tencent. All rights reserved.`）
