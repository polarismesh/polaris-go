/**
 * Tencent is pleased to support the open source community by making polaris-go available.
 *
 * Copyright (C) 2019 THL A29 Limited, a Tencent company. All rights reserved.
 *
 * Licensed under the BSD 3-Clause License (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * https://opensource.org/licenses/BSD-3-Clause
 *
 * Unless required by applicable law or agreed to in writing, software distributed
 * under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
 * CONDITIONS OF ANY KIND, either express or implied. See the License for the
 * specific language governing permissions and limitations under the License.
 */

package composite

import (
	"testing"

	regexp "github.com/dlclark/regexp2"
	apimodel "github.com/polarismesh/specification/source/go/api/v1/model"
	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/polarismesh/polaris-go/pkg/model"
	// blank import 触发熔断插件接口注册，保证本包 init() 中的 RegisterConfigurablePlugin 不会因
	// 找不到接口而 panic。无此 import 时单包跑测试会 fail。
	_ "github.com/polarismesh/polaris-go/pkg/plugin/circuitbreaker"
)

// nopRegex 用于测试中替代真实的正则缓存查询，按需现编译
// 测试场景不涉及缓存命中率，只需保证返回一个可用的正则对象
func nopRegex(s string) *regexp.Regexp {
	return regexp.MustCompile(s, regexp.RE2)
}

// noopLogger 是一个空实现 log.Logger，用于测试中避免对全局 logger 容器的依赖
// 所有方法都是无操作，IsLevelEnabled 永远返回 false 以跳过实际日志格式化
type noopLogger struct{}

func (noopLogger) Tracef(string, ...interface{}) {}
func (noopLogger) Debugf(string, ...interface{}) {}
func (noopLogger) Infof(string, ...interface{})  {}
func (noopLogger) Warnf(string, ...interface{})  {}
func (noopLogger) Errorf(string, ...interface{}) {}
func (noopLogger) Fatalf(string, ...interface{}) {}
func (noopLogger) IsLevelEnabled(int) bool       { return false }
func (noopLogger) SetLogLevel(int) error         { return nil }

// newMatchString 构造 MatchString，简化测试代码
func newMatchString(t apimodel.MatchString_MatchStringType, value string) *apimodel.MatchString {
	return &apimodel.MatchString{
		Type:  t,
		Value: &wrapperspb.StringValue{Value: value},
	}
}

// TestMatchProtocolOrMethod 测试场景：protocol/HTTP method 单字段匹配规则
// 前置条件：构造各种 source 与 target 的组合
// 预期结果：target 为空或 "*" 时永远匹配；否则按忽略大小写比较
func TestMatchProtocolOrMethod(t *testing.T) {
	cases := []struct {
		name   string
		source string
		target string
		want   bool
	}{
		{name: "target_empty", source: "http", target: "", want: true},
		{name: "target_wildcard", source: "http", target: "*", want: true},
		{name: "exact_match", source: "http", target: "http", want: true},
		{name: "case_insensitive", source: "HTTP", target: "http", want: true},
		{name: "mismatch", source: "http", target: "grpc", want: false},
		{name: "source_empty_target_value", source: "", target: "http", want: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := matchProtocolOrMethod(c.source, c.target)
			assert.Equal(t, c.want, got)
		})
	}
}

// TestMatchMethodWithAPI_NonMethodLevel 测试场景：非 METHOD 级资源直接放行
// 前置条件：传入 ServiceResource
// 预期结果：永远返回 true，不参与接口级匹配
func TestMatchMethodWithAPI_NonMethodLevel(t *testing.T) {
	svc, _ := model.NewServiceResource(&model.ServiceKey{Namespace: "ns", Service: "s"}, nil)
	api := &apimodel.API{Protocol: "http", Method: "GET",
		Path: newMatchString(apimodel.MatchString_EXACT, "/a")}
	assert.True(t, matchMethodWithAPI(svc, api, nopRegex))
}

// TestMatchMethodWithAPI_NilApi 测试场景：BlockConfig.Api 为 nil 时通配
// 前置条件：METHOD 级资源 + nil api
// 预期结果：返回 true（视为匹配全部接口）
func TestMatchMethodWithAPI_NilApi(t *testing.T) {
	svcKey := &model.ServiceKey{Namespace: "ns", Service: "s"}
	res, _ := model.NewMethodResourceWithAPI(svcKey, nil, "http", "GET", "/a")
	assert.True(t, matchMethodWithAPI(res, nil, nopRegex))
}

// TestMatchMethodWithAPI_ProtocolMismatch 测试场景：protocol 不一致
// 前置条件：资源 protocol=http，api.protocol=grpc
// 预期结果：返回 false
func TestMatchMethodWithAPI_ProtocolMismatch(t *testing.T) {
	svcKey := &model.ServiceKey{Namespace: "ns", Service: "s"}
	res, _ := model.NewMethodResourceWithAPI(svcKey, nil, "http", "GET", "/a")
	api := &apimodel.API{Protocol: "grpc", Method: "GET",
		Path: newMatchString(apimodel.MatchString_EXACT, "/a")}
	assert.False(t, matchMethodWithAPI(res, api, nopRegex))
}

// TestMatchMethodWithAPI_MethodMismatch 测试场景：HTTP method 不一致
// 前置条件：资源 method=GET，api.method=POST
// 预期结果：返回 false
func TestMatchMethodWithAPI_MethodMismatch(t *testing.T) {
	svcKey := &model.ServiceKey{Namespace: "ns", Service: "s"}
	res, _ := model.NewMethodResourceWithAPI(svcKey, nil, "http", "GET", "/a")
	api := &apimodel.API{Protocol: "http", Method: "POST",
		Path: newMatchString(apimodel.MatchString_EXACT, "/a")}
	assert.False(t, matchMethodWithAPI(res, api, nopRegex))
}

// TestMatchMethodWithAPI_PathExact 测试场景：path 精确匹配
// 前置条件：api.path 为 EXACT 模式
// 预期结果：path 相同返回 true，不同返回 false
func TestMatchMethodWithAPI_PathExact(t *testing.T) {
	svcKey := &model.ServiceKey{Namespace: "ns", Service: "s"}
	resOk, _ := model.NewMethodResourceWithAPI(svcKey, nil, "http", "GET", "/api/v1/orders")
	resBad, _ := model.NewMethodResourceWithAPI(svcKey, nil, "http", "GET", "/api/v1/users")
	api := &apimodel.API{Protocol: "http", Method: "GET",
		Path: newMatchString(apimodel.MatchString_EXACT, "/api/v1/orders")}
	assert.True(t, matchMethodWithAPI(resOk, api, nopRegex))
	assert.False(t, matchMethodWithAPI(resBad, api, nopRegex))
}

// TestMatchMethodWithAPI_PathRegex 测试场景：path 正则匹配
// 前置条件：api.path 为 REGEX 模式
// 预期结果：匹配正则的路径返回 true
func TestMatchMethodWithAPI_PathRegex(t *testing.T) {
	svcKey := &model.ServiceKey{Namespace: "ns", Service: "s"}
	res, _ := model.NewMethodResourceWithAPI(svcKey, nil, "http", "GET", "/api/v1/orders/123")
	api := &apimodel.API{Protocol: "http", Method: "GET",
		Path: newMatchString(apimodel.MatchString_REGEX, "/api/v1/orders/\\d+")}
	assert.True(t, matchMethodWithAPI(res, api, nopRegex))
}

// TestMatchMethodWithAPI_NilPathFieldWildcard 测试场景：api.Path 字段缺失视作通配
// 前置条件：api.Protocol/Method 匹配，但 api.Path 为 nil
// 预期结果：放行（path 维度通配）
func TestMatchMethodWithAPI_NilPathFieldWildcard(t *testing.T) {
	svcKey := &model.ServiceKey{Namespace: "ns", Service: "s"}
	res, _ := model.NewMethodResourceWithAPI(svcKey, nil, "http", "GET", "/any/path")
	api := &apimodel.API{Protocol: "*", Method: "*", Path: nil}
	assert.True(t, matchMethodWithAPI(res, api, nopRegex))
}

// TestMatchMethod_Compat 测试场景：旧 matchMethod 仍按 path 维度匹配
// 前置条件：传入 MethodResource 与单个 MatchString（destination.Method 字段）
// 预期结果：与 path 比较，确保探测规则匹配与老规则回退路径行为不变
func TestMatchMethod_Compat(t *testing.T) {
	svcKey := &model.ServiceKey{Namespace: "ns", Service: "s"}
	res, _ := model.NewMethodResource(svcKey, nil, "/legacy/path")
	matchVal := newMatchString(apimodel.MatchString_EXACT, "/legacy/path")
	assert.True(t, matchMethod(res, matchVal, nopRegex))

	badVal := newMatchString(apimodel.MatchString_EXACT, "/other")
	assert.False(t, matchMethod(res, badVal, nopRegex))
}
