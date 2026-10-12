---
name: "source-command-code-review"
description: "Migrated source command `code-review`"
---

# source-command-code-review

Use this skill when the user asks to run the migrated source command `code-review`.

## Command Template

# 代码审查

## 使用场景
当需要对当前分支的代码变更进行审查时使用。

## 输入参数
- `$BASE_BRANCH`：（可选）对比的基准分支，默认 `origin/main`
- `$TARGET_FILES`：（可选）指定要审查的文件路径

## 执行步骤

### 1. 获取变更范围
```bash
# 获取最新的基准分支
git fetch origin main

# 查看变更文件列表
git diff --name-only origin/main

# 查看变更详情
git diff origin/main
```

### 2. 执行审查
- 按照 `skills/code-review/SKILL.md` 中定义的 12 节审查框架逐项检查
- 参考 `skills/code-review/examples.md` 中的输出格式

### 3. 审查要点（polaris-go 特有）
- **许可证头**：每个新增 `.go` 文件（排除 `*.pb.go`）是否使用 `//` 许可证头，且包含 `Copyright (C) 2021 Tencent. All rights reserved.`
- **插件注册**：新增插件是否在 `plugin.cfg` 和 `plugins.go` 中注册
- **配置校验**：新增配置是否实现了 `Verify()`、`SetDefault()`
- **日志规范**：日志前缀是否使用方括号标识模块，是否使用模块专属 Logger
- **错误处理**：error 是否被正确处理，是否使用 `fmt.Errorf` 包装
- **并发安全**：共享数据是否有锁保护，goroutine 是否有退出机制

### 4. 自动化检查
```bash
# Lint 检查
golangci-lint run --timeout=30m --new-from-rev=origin/main

# 编译检查
go build ./...

# 测试
go test ./...
```

### 5. 输出审查报告
按照 `skills/code-review/SKILL.md` 的输出结构，包含：
- 快速结论（Yes / No / Conditional）
- 变更意图核对
- 各维度审查结果
- 风险分级（P0 ~ P3）
- 修改建议
