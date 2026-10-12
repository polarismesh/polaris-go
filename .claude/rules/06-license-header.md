# 许可证头规范

## 规则

`**/*.go`（包括测试文件）必须在文件开头包含下面的 Tencent BSD 3-Clause 许可证头。检查范围与 `.licenserc.yaml` 相同，排除 `*.pb.go`。非 Go 文件不加这段头。

CI 由 `.github/workflows/license-checker.yml` 使用 `apache/skywalking-eyes@v0.4.0` 读取根目录 `.licenserc.yaml`，按整段头检查。`.golangci.yml` 里 revive 的 `file-header` 另外检查首行是否包含 `Tencent is pleased to support the open source community by making polaris-go available.`。

## 许可证头模板

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

## 检查方式

范围与 `.licenserc.yaml` 一致：

```yaml
paths:
  - "**/*.go"
paths-ignore:
  # protoc 生成文件没有版权头
  - "**/*.pb.go"
```

skywalking-eyes 对 Go 使用 `//` 行注释。整段头必须与模板一致，版权行固定为 `Copyright (C) 2021 Tencent. All rights reserved.`。

revive `file-header`（`.golangci.yml`）只检查第一行注释是否包含开篇句，不能代替整段检查：

```yaml
- name: file-header
  arguments:
    ["Tencent is pleased to support the open source community by making polaris-go available."]
```

## 注意事项

1. 许可证头必须是文件的**第一个内容**（在 `package` 声明之前）
2. 使用 `//` 行注释，整段按模板书写
3. 版权行固定为 `Copyright (C) 2021 Tencent. All rights reserved.`
4. `*.pb.go` 已在 `.licenserc.yaml` 的 `paths-ignore` 中排除，无需手动添加
