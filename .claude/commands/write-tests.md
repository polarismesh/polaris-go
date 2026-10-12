# 编写单元测试

## 使用场景
当需要为指定的函数、方法或包编写单元测试时使用。

## 输入参数
- `$TARGET`：目标包路径或文件路径（如 `./plugin/loadbalancer/weightedrandom/`）
- `$FUNCTION`：（可选）指定要测试的函数名

## 执行步骤

### 1. 分析被测代码
- 阅读目标文件，理解函数签名、输入输出、依赖关系
- 识别需要 mock 的外部依赖（数据库、网络、第三方库）
- 确认是否已有测试文件，避免覆盖

### 2. 确认测试规范
- 阅读 `rules/04-table-driven-test.md`（表驱动测试）
- 阅读 `rules/05-suite-test.md`（Suite 测试）
- 根据场景选择合适的测试模式

### 3. 创建临时测试文件
```bash
# 在目标包下创建临时测试文件
touch $TARGET/<filename>_test_new.go
```

### 4. 编写测试代码
- 包含 `//` 许可证头（`Copyright (C) 2021 Tencent. All rights reserved.`）
- 使用表驱动测试模式（优先）或单个测试模式
- 测试函数命名：`Test<结构体名>_<方法名>` 或中文描述 `TestChooseInstance_基本选择`
- 覆盖场景：
  - ✅ 正常流程（happy path）
  - ✅ 边界值（空值、零值、最大值）
  - ✅ 异常输入（nil、非法参数）
  - ✅ 错误处理（依赖失败、超时）
  - ✅ 并发安全（如适用）

### 5. Mock 规则
- 使用 `gomonkey` 进行函数/方法 mock
- 如果已有 gomock 生成的 mock 对象，优先使用 gomock
- mock 后必须 `defer patches.Reset()`
- 不 mock 被测函数本身

### 6. 验证测试
```bash
# 运行临时测试文件中的测试（必须禁用内联优化以支持 gomonkey）
go test -gcflags=all=-l -v $TARGET -run TestXxx

# 运行整个包的测试
go test -gcflags=all=-l -v $TARGET

# 检查竞态条件
go test -gcflags=all=-l -race -v $TARGET
```

### 7. 合并到正式测试文件
- 测试通过后，将测试函数合并到正式的 `_test.go` 文件
- 删除临时测试文件
- 再次运行全部测试确认无冲突

### 8. 最终验证
```bash
# 验证合并后的测试
go test -gcflags=all=-l -v $TARGET

# 确认 lint 通过
golangci-lint run --timeout=30m --new-from-rev=origin/main $TARGET
```

## 完成标准
- [ ] 测试覆盖正常流程、边界情况、异常处理
- [ ] 所有测试通过
- [ ] 无竞态条件（`-race` 通过）
- [ ] 许可证头为 `//` 模板，版权行是 `Copyright (C) 2021 Tencent. All rights reserved.`
- [ ] 临时文件已清理
