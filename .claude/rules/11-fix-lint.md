# 修复 Lint 问题

## 使用场景
当 CI 流水线 lint 检查失败，或本地需要修复 lint 问题时使用。

## 输入参数
- `$TARGET`：（可选）指定要检查的包路径，如 `./pkg/flow/...`，默认检查全部

## 执行步骤

### 1. 确认 lint 配置
- 阅读 `.golangci.yml` 了解启用的 linter 和规则
- 注意：项目使用 golangci-lint v2，配置格式与 v1 不同

### 2. 运行 lint 检查
```bash
# 获取 main 分支最新代码（用于增量检查）
git fetch origin main

# 运行 lint（只检查相对于 main 的变更）
golangci-lint run --timeout=30m --new-from-rev=origin/main $TARGET

# 如需检查全部代码（不推荐，仅特殊情况）
golangci-lint run --timeout=30m $TARGET
```

### 3. 逐类修复
按以下优先级修复：

#### 高优先级（编译/逻辑相关）
- `errcheck`：未检查的错误返回值
- `govet`：可疑的代码结构
- `staticcheck`：静态分析问题
- `typecheck`：类型检查错误

#### 中优先级（代码质量）
- `funlen`：函数超过 330 行，需要拆分
- `goconst`：重复字符串应提取为常量
- `revive`：各类代码规范问题
  - `file-header`：缺少许可证头
  - `function-result-limit`：返回值超过 4 个
  - `line-length-limit`：单行超过 480 字符

#### 低优先级（格式/风格）
- `gofmt`：格式化问题，运行 `go fmt ./...`
- `gci`：import 分组顺序，运行 `bash import-format.sh`
- `misspell`：拼写错误
- `whitespace`：多余空行

### 4. 验证修复
```bash
# 重新运行 lint 确认全部通过
golangci-lint run --timeout=30m --new-from-rev=origin/main

# 确保编译通过
go build ./...

# 确保测试通过
go test ./...
```

### 5. 提交
- 提交信息格式：`fix: lint issues` 或 `chore: fix lint warnings`
- lint 修复应独立提交，不要混入功能代码

## 常见问题

### golangci-lint v2 不支持的配置项
以下属性在 v2 中已移除，不能放在配置文件中：
- `linters.exclusions.new-from-rev`（改用命令行 `--new-from-rev`）
- `misspell.ignore-words`（改为 `misspell.extra-words`）
- `revive.ignore-generated-header`（已移除）
- `goconst.ignore-tests`（已移除）
- `gci.skip-generated`（已移除）

### 许可证头模板

使用 `//` 行注释。版权行固定为 `Copyright (C) 2021 Tencent. All rights reserved.`。缺少整段头时，`license-checker.yml`（`.licenserc.yaml`）会失败；revive `file-header` 只检查开篇句。`*.pb.go` 不需要添加。

```go
// Tencent is pleased to support the open source community by making polaris-go available.
//
// Copyright (C) 2021 Tencent. All rights reserved.
//
// Licensed under the BSD 3-Clause License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// https://opensource.org/licenses/BSD-3-Clause
//
// Unless required by applicable law or agreed to in writing, software distributed
// under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
// CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
```
