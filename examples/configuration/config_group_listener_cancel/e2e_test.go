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

package main

import (
	"context"
	"os"
	"testing"
)

// TestConfigGroupListenerCancelE2E 使用显式提供的真实 Polaris 环境验证全部 access 生命周期场景。
// 未设置连接信息时跳过；指定环境后任何连接、发布、断言或清理失败都会使测试失败。
func TestConfigGroupListenerCancelE2E(t *testing.T) {
	o := defaultOptions()
	if o.server == "" && o.discoveryAddr == "" && o.configAddr == "" && o.openAPI == "" {
		t.Skip("设置 POLARIS_SERVER 或三个 POLARIS_* 地址变量后运行真实服务端 E2E")
	}
	if namespace := os.Getenv("POLARIS_NAMESPACE"); namespace != "" {
		o.namespace = namespace
	}
	if err := runE2E(context.Background(), o); err != nil {
		t.Fatal(err)
	}
}
