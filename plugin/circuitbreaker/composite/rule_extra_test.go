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
	"time"

	"github.com/polarismesh/specification/source/go/api/v1/fault_tolerance"
	apimodel "github.com/polarismesh/specification/source/go/api/v1/model"
	"github.com/stretchr/testify/assert"

	"github.com/polarismesh/polaris-go/pkg/model"
)

// ============================================================
// 第1组：接口路径匹配方式——全匹配、正则、不等于、包含、不包含
// ============================================================

// TestMatchMethodWithAPI_PathNotEquals 测试场景：NOT_EQUALS 路径匹配——仅排除指定路径
// 前置条件：api.path.type=NOT_EQUALS，value="/admin"
// 预期结果：路径="/admin" 返回 false；路径="/api/echo" 返回 true
func TestMatchMethodWithAPI_PathNotEquals(t *testing.T) {
	svcKey := &model.ServiceKey{Namespace: "default", Service: "order"}
	resAdmin, _ := model.NewMethodResourceWithAPI(svcKey, nil, "http", "GET", "/admin")
	resApi, _ := model.NewMethodResourceWithAPI(svcKey, nil, "http", "GET", "/api/echo")

	api := &apimodel.API{Protocol: "http", Method: "GET",
		Path: newMatchString(apimodel.MatchString_NOT_EQUALS, "/admin")}

	assert.False(t, matchMethodWithAPI(resAdmin, api, nopRegex))
	assert.True(t, matchMethodWithAPI(resApi, api, nopRegex))
}

// TestMatchMethodWithAPI_PathIn 测试场景：IN 路径匹配——路径在逗号分隔列表中时命中
// 前置条件：api.path.type=IN，value="/echo,/order,/slow"
// 预期结果：路径="/echo"、"/order"、"/slow" 返回 true；路径="/info" 返回 false
func TestMatchMethodWithAPI_PathIn(t *testing.T) {
	svcKey := &model.ServiceKey{Namespace: "default", Service: "order"}
	resEcho, _ := model.NewMethodResourceWithAPI(svcKey, nil, "http", "GET", "/echo")
	resOrder, _ := model.NewMethodResourceWithAPI(svcKey, nil, "http", "GET", "/order")
	resSlow, _ := model.NewMethodResourceWithAPI(svcKey, nil, "http", "GET", "/slow")
	resInfo, _ := model.NewMethodResourceWithAPI(svcKey, nil, "http", "GET", "/info")

	api := &apimodel.API{Protocol: "http", Method: "GET",
		Path: newMatchString(apimodel.MatchString_IN, "/echo,/order,/slow")}

	assert.True(t, matchMethodWithAPI(resEcho, api, nopRegex))
	assert.True(t, matchMethodWithAPI(resOrder, api, nopRegex))
	assert.True(t, matchMethodWithAPI(resSlow, api, nopRegex))
	assert.False(t, matchMethodWithAPI(resInfo, api, nopRegex))
}

// TestMatchMethodWithAPI_PathNotIn 测试场景：NOT_IN 路径匹配——排除逗号分隔列表中的路径
// 前置条件：api.path.type=NOT_IN，value="/admin,/health,/metrics"
// 预期结果：路径="/admin" 返回 false；路径="/api/echo" 返回 true
func TestMatchMethodWithAPI_PathNotIn(t *testing.T) {
	svcKey := &model.ServiceKey{Namespace: "default", Service: "order"}
	resAdmin, _ := model.NewMethodResourceWithAPI(svcKey, nil, "http", "GET", "/admin")
	resHealth, _ := model.NewMethodResourceWithAPI(svcKey, nil, "http", "GET", "/health")
	resApi, _ := model.NewMethodResourceWithAPI(svcKey, nil, "http", "GET", "/api/echo")

	api := &apimodel.API{Protocol: "http", Method: "GET",
		Path: newMatchString(apimodel.MatchString_NOT_IN, "/admin,/health,/metrics")}

	assert.False(t, matchMethodWithAPI(resAdmin, api, nopRegex))
	assert.False(t, matchMethodWithAPI(resHealth, api, nopRegex))
	assert.True(t, matchMethodWithAPI(resApi, api, nopRegex))
}

// TestMatchMethodWithAPI_PathExact_MatchAndMismatch 测试场景：EXACT 路径全匹配
// 前置条件：api.path.type=EXACT，value="/api/v1/orders"
// 预期结果：完全一致返回 true，略有不同返回 false
func TestMatchMethodWithAPI_PathExact_MatchAndMismatch(t *testing.T) {
	svcKey := &model.ServiceKey{Namespace: "default", Service: "order"}
	resOk, _ := model.NewMethodResourceWithAPI(svcKey, nil, "http", "GET", "/api/v1/orders")
	resBad, _ := model.NewMethodResourceWithAPI(svcKey, nil, "http", "GET", "/api/v1/orders/")

	api := &apimodel.API{Protocol: "http", Method: "GET",
		Path: newMatchString(apimodel.MatchString_EXACT, "/api/v1/orders")}

	assert.True(t, matchMethodWithAPI(resOk, api, nopRegex))
	assert.False(t, matchMethodWithAPI(resBad, api, nopRegex))
}

// TestMatchMethodWithAPI_PathRegex_MatchAndMismatch 测试场景：REGEX 路径正则匹配
// 前置条件：api.path.type=REGEX，value="/api/v1/orders/\d+"
// 预期结果：匹配正则的路径返回 true，不匹配的返回 false
func TestMatchMethodWithAPI_PathRegex_MatchAndMismatch(t *testing.T) {
	svcKey := &model.ServiceKey{Namespace: "default", Service: "order"}
	resMatch, _ := model.NewMethodResourceWithAPI(svcKey, nil, "http", "GET", "/api/v1/orders/12345")
	resNoMatch, _ := model.NewMethodResourceWithAPI(svcKey, nil, "http", "GET", "/api/v1/orders/abc")

	api := &apimodel.API{Protocol: "http", Method: "GET",
		Path: newMatchString(apimodel.MatchString_REGEX, "/api/v1/orders/\\d+")}

	assert.True(t, matchMethodWithAPI(resMatch, api, nopRegex))
	assert.False(t, matchMethodWithAPI(resNoMatch, api, nopRegex))
}

// ============================================================
// 第2组：接口协议匹配——HTTP、Dubbo、gRPC、Thrift
// ============================================================

// TestMatchMethodWithAPI_Protocols 测试场景：四种协议精确匹配与不匹配
// 前置条件：资源 protocol 分别为 http/dubbo/grpc/thrift，api.protocol 逐一比对
// 预期结果：协议一致时返回 true，否则返回 false
func TestMatchMethodWithAPI_Protocols(t *testing.T) {
	svcKey := &model.ServiceKey{Namespace: "default", Service: "order"}
	path := "/api/echo"

	tests := []struct {
		name        string
		resProtocol string
		apiProtocol string
		want        bool
	}{
		{name: "http_match", resProtocol: "http", apiProtocol: "http", want: true},
		{name: "http_mismatch_dubbo", resProtocol: "http", apiProtocol: "dubbo", want: false},
		{name: "dubbo_match", resProtocol: "dubbo", apiProtocol: "dubbo", want: true},
		{name: "dubbo_mismatch_grpc", resProtocol: "dubbo", apiProtocol: "grpc", want: false},
		{name: "grpc_match", resProtocol: "grpc", apiProtocol: "grpc", want: true},
		{name: "grpc_mismatch_thrift", resProtocol: "grpc", apiProtocol: "thrift", want: false},
		{name: "thrift_match", resProtocol: "thrift", apiProtocol: "thrift", want: true},
		{name: "thrift_mismatch_http", resProtocol: "thrift", apiProtocol: "http", want: false},
		{name: "wildcard_http", resProtocol: "http", apiProtocol: "*", want: true},
		{name: "wildcard_dubbo", resProtocol: "dubbo", apiProtocol: "*", want: true},
		{name: "empty_http", resProtocol: "http", apiProtocol: "", want: true},
		{name: "empty_dubbo", resProtocol: "dubbo", apiProtocol: "", want: true},
		{name: "case_insensitive_HTTP", resProtocol: "HTTP", apiProtocol: "http", want: true},
		{name: "case_insensitive_DUBBO", resProtocol: "DUBBO", apiProtocol: "dubbo", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := model.NewMethodResourceWithAPI(svcKey, nil, tt.resProtocol, "GET", path)
			assert.NoError(t, err)
			api := &apimodel.API{Protocol: tt.apiProtocol, Method: "GET",
				Path: newMatchString(apimodel.MatchString_EXACT, path)}
			assert.Equal(t, tt.want, matchMethodWithAPI(res, api, nopRegex))
		})
	}
}

// ============================================================
// 第3组：接口方法匹配——GET/POST/PUT/PATCH/DELETE/HEAD/OPTIONS/TRACE/CONNECT
// ============================================================

// TestMatchMethodWithAPI_AllHttpMethods 测试场景：九种 HTTP 方法各自精确匹配与不匹配
// 前置条件：资源 protocol=http，method 为 9 种之一；api.method 逐一比对
// 预期结果：method 一致时返回 true；不一致时返回 false；空串/∗通配放行
func TestMatchMethodWithAPI_AllHttpMethods(t *testing.T) {
	svcKey := &model.ServiceKey{Namespace: "default", Service: "order"}
	path := "/api/echo"
	allMethods := []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "TRACE", "CONNECT"}

	for _, method := range allMethods {
		t.Run("match_"+method, func(t *testing.T) {
			res, err := model.NewMethodResourceWithAPI(svcKey, nil, "http", method, path)
			assert.NoError(t, err)
			api := &apimodel.API{Protocol: "http", Method: method,
				Path: newMatchString(apimodel.MatchString_EXACT, path)}
			assert.True(t, matchMethodWithAPI(res, api, nopRegex))
		})
		t.Run("mismatch_"+method+"_vs_GET", func(t *testing.T) {
			if method == "GET" {
				return
			}
			res, err := model.NewMethodResourceWithAPI(svcKey, nil, "http", method, path)
			assert.NoError(t, err)
			api := &apimodel.API{Protocol: "http", Method: "GET",
				Path: newMatchString(apimodel.MatchString_EXACT, path)}
			assert.False(t, matchMethodWithAPI(res, api, nopRegex))
		})
		t.Run("wildcard_"+method, func(t *testing.T) {
			res, err := model.NewMethodResourceWithAPI(svcKey, nil, "http", method, path)
			assert.NoError(t, err)
			api := &apimodel.API{Protocol: "http", Method: "*",
				Path: newMatchString(apimodel.MatchString_EXACT, path)}
			assert.True(t, matchMethodWithAPI(res, api, nopRegex))
		})
		t.Run("empty_"+method, func(t *testing.T) {
			res, err := model.NewMethodResourceWithAPI(svcKey, nil, "http", method, path)
			assert.NoError(t, err)
			api := &apimodel.API{Protocol: "http", Method: "",
				Path: newMatchString(apimodel.MatchString_EXACT, path)}
			assert.True(t, matchMethodWithAPI(res, api, nopRegex))
		})
	}
}

// TestMatchMethodWithAPI_MethodCaseInsensitive 测试场景：HTTP 方法大小写不敏感
// 前置条件：资源 method="get"/"GET"，api.method="GET"/"get"（大小写交叉比对同一方法名）
// 预期结果：忽略大小写后均命中
func TestMatchMethodWithAPI_MethodCaseInsensitive(t *testing.T) {
	svcKey := &model.ServiceKey{Namespace: "default", Service: "order"}
	resLower, _ := model.NewMethodResourceWithAPI(svcKey, nil, "http", "get", "/a")
	resUpper, _ := model.NewMethodResourceWithAPI(svcKey, nil, "http", "GET", "/a")

	apiLower := &apimodel.API{Protocol: "http", Method: "get",
		Path: newMatchString(apimodel.MatchString_EXACT, "/a")}
	apiUpper := &apimodel.API{Protocol: "http", Method: "GET",
		Path: newMatchString(apimodel.MatchString_EXACT, "/a")}

	assert.True(t, matchMethodWithAPI(resLower, apiUpper, nopRegex))
	assert.True(t, matchMethodWithAPI(resUpper, apiLower, nopRegex))
}

// ============================================================
// 第4组：验证熔断来源服务和目标服务的命中逻辑
// ============================================================

// buildSimpleRuleResponse 构造含指定 source/destination 的 SERVICE 级规则响应
func buildSimpleRuleResponse(name string, destNs, destSvc string, srcNs, srcSvc string) *model.ServiceRuleResponse {
	return &model.ServiceRuleResponse{
		Value: &fault_tolerance.CircuitBreaker{
			Rules: []*fault_tolerance.CircuitBreakerRule{
				{
					Name:   name,
					Enable: true,
					Level:  fault_tolerance.Level_SERVICE,
					RuleMatcher: &fault_tolerance.RuleMatcher{
						Source: &fault_tolerance.RuleMatcher_SourceService{
							Namespace: srcNs, Service: srcSvc,
						},
						Destination: &fault_tolerance.RuleMatcher_DestinationService{
							Namespace: destNs, Service: destSvc,
						},
					},
				},
			},
		},
	}
}

// TestSelectCircuitBreakerRule_SourceServiceExactMatch 测试场景：主调服务精确匹配
// 前置条件：规则 source=Production/front，资源 caller=Production/front
// 预期结果：规则命中
func TestSelectCircuitBreakerRule_SourceServiceExactMatch(t *testing.T) {
	svcKey := &model.ServiceKey{Namespace: "Production", Service: "payment"}
	caller := &model.ServiceKey{Namespace: "Production", Service: "front"}
	res, _ := model.NewServiceResource(svcKey, caller)

	resp := buildSimpleRuleResponse("svc-rule", "Production", "payment", "Production", "front")
	got := selectCircuitBreakerRule(res, resp, nopRegex, noopLogger{})
	assert.NotNil(t, got)
	assert.Equal(t, "svc-rule", got.Name)
}

// TestSelectCircuitBreakerRule_SourceServiceMismatch 测试场景：主调服务不匹配
// 前置条件：规则 source=Production/front，资源 caller=Staging/gateway
// 预期结果：规则不命中
func TestSelectCircuitBreakerRule_SourceServiceMismatch(t *testing.T) {
	svcKey := &model.ServiceKey{Namespace: "Production", Service: "payment"}
	caller := &model.ServiceKey{Namespace: "Staging", Service: "gateway"}
	res, _ := model.NewServiceResource(svcKey, caller)

	resp := buildSimpleRuleResponse("svc-rule", "Production", "payment", "Production", "front")
	got := selectCircuitBreakerRule(res, resp, nopRegex, noopLogger{})
	assert.Nil(t, got)
}

// TestSelectCircuitBreakerRule_SourceWildcardMatchesAny 测试场景：主调服务通配符匹配任意 caller
// 前置条件：规则 source=∗/∗（通配），资源 caller 任意
// 预期结果：规则命中
func TestSelectCircuitBreakerRule_SourceWildcardMatchesAny(t *testing.T) {
	svcKey := &model.ServiceKey{Namespace: "Production", Service: "payment"}
	caller := &model.ServiceKey{Namespace: "Staging", Service: "gateway"}
	res, _ := model.NewServiceResource(svcKey, caller)

	resp := buildSimpleRuleResponse("svc-rule", "Production", "payment", "*", "*")
	got := selectCircuitBreakerRule(res, resp, nopRegex, noopLogger{})
	assert.NotNil(t, got)
	assert.Equal(t, "svc-rule", got.Name)
}

// TestSelectCircuitBreakerRule_DestExactSourceWildcard 测试场景：目标精确 + 来源通配
// 前置条件：规则 dest=Production/payment, source=∗/∗；资源匹配
// 预期结果：规则命中
func TestSelectCircuitBreakerRule_DestExactSourceWildcard(t *testing.T) {
	svcKey := &model.ServiceKey{Namespace: "Production", Service: "payment"}
	caller := &model.ServiceKey{Namespace: "Any", Service: "any"}
	res, _ := model.NewServiceResource(svcKey, caller)

	resp := buildSimpleRuleResponse("svc-rule", "Production", "payment", "*", "*")
	got := selectCircuitBreakerRule(res, resp, nopRegex, noopLogger{})
	assert.NotNil(t, got)
}

// TestSelectCircuitBreakerRule_DestWildcardSourceExact 测试场景：目标通配 + 来源精确
// 前置条件：规则 dest=∗/∗, source=Production/front；资源 caller=Production/front
// 预期结果：规则命中
func TestSelectCircuitBreakerRule_DestWildcardSourceExact(t *testing.T) {
	svcKey := &model.ServiceKey{Namespace: "Production", Service: "payment"}
	caller := &model.ServiceKey{Namespace: "Production", Service: "front"}
	res, _ := model.NewServiceResource(svcKey, caller)

	resp := buildSimpleRuleResponse("svc-rule", "*", "*", "Production", "front")
	got := selectCircuitBreakerRule(res, resp, nopRegex, noopLogger{})
	assert.NotNil(t, got)
}

// TestSelectCircuitBreakerRule_DestServiceMismatch 测试场景：目标服务不匹配
// 前置条件：规则 dest=Production/payment，资源 service=Production/order
// 预期结果：规则不命中
func TestSelectCircuitBreakerRule_DestServiceMismatch(t *testing.T) {
	svcKey := &model.ServiceKey{Namespace: "Production", Service: "order"}
	caller := &model.ServiceKey{Namespace: "Production", Service: "front"}
	res, _ := model.NewServiceResource(svcKey, caller)

	resp := buildSimpleRuleResponse("svc-rule", "Production", "payment", "*", "*")
	got := selectCircuitBreakerRule(res, resp, nopRegex, noopLogger{})
	assert.Nil(t, got)
}

// ============================================================
// 第5组：验证默认实例级熔断应当是无远程规则或全部远程规则关闭
// ============================================================

// TestHasAnyEnabledRule_NoRules 测试场景：服务无任何熔断规则
// 前置条件：object=nil 或 object.Value=nil 或 空 Rules
// 预期结果：返回 false
func TestHasAnyEnabledRule_NoRules(t *testing.T) {
	assert.False(t, hasAnyEnabledRule(nil, nil))
	assert.False(t, hasAnyEnabledRule(&model.ServiceRuleResponse{}, nil))
	assert.False(t, hasAnyEnabledRule(&model.ServiceRuleResponse{
		Value: &fault_tolerance.CircuitBreaker{},
	}, nil))
}

// TestHasAnyEnabledRule_ServiceEnabled 测试场景：存在 enabled 的 SERVICE 级规则
// 前置条件：一条 SERVICE 级规则 enable=true
// 预期结果：返回 true，拦截默认规则注入
func TestHasAnyEnabledRule_ServiceEnabled(t *testing.T) {
	object := &model.ServiceRuleResponse{
		Value: &fault_tolerance.CircuitBreaker{
			Rules: []*fault_tolerance.CircuitBreakerRule{
				{Name: "svc-rule", Enable: true, Level: fault_tolerance.Level_SERVICE},
			},
		},
	}
	assert.True(t, hasAnyEnabledRule(object, nil))
}

// TestHasAnyEnabledRule_MethodEnabled 测试场景：存在 enabled 的 METHOD 级规则
// 前置条件：一条 METHOD 级规则 enable=true
// 预期结果：返回 true，拦截默认规则注入
func TestHasAnyEnabledRule_MethodEnabled(t *testing.T) {
	object := &model.ServiceRuleResponse{
		Value: &fault_tolerance.CircuitBreaker{
			Rules: []*fault_tolerance.CircuitBreakerRule{
				{Name: "method-rule", Enable: true, Level: fault_tolerance.Level_METHOD},
			},
		},
	}
	assert.True(t, hasAnyEnabledRule(object, nil))
}

// TestHasAnyEnabledRule_InstanceEnabled 测试场景：存在 enabled 的 INSTANCE 级远程规则
// 前置条件：一条 INSTANCE 级规则 enable=true
// 预期结果：返回 true，远程规则优先，不注入默认
func TestHasAnyEnabledRule_InstanceEnabled(t *testing.T) {
	object := &model.ServiceRuleResponse{
		Value: &fault_tolerance.CircuitBreaker{
			Rules: []*fault_tolerance.CircuitBreakerRule{
				{Name: "ins-rule", Enable: true, Level: fault_tolerance.Level_INSTANCE},
			},
		},
	}
	assert.True(t, hasAnyEnabledRule(object, nil))
}

// TestHasAnyEnabledRule_AllDisabled 测试场景：全部远程规则 Enable=false
// 前置条件：SERVICE/METHOD/INSTANCE 各一条，全部 disable
// 预期结果：返回 false，允许注入默认规则
func TestHasAnyEnabledRule_AllDisabled(t *testing.T) {
	object := &model.ServiceRuleResponse{
		Value: &fault_tolerance.CircuitBreaker{
			Rules: []*fault_tolerance.CircuitBreakerRule{
				{Name: "svc-rule", Enable: false, Level: fault_tolerance.Level_SERVICE},
				{Name: "method-rule", Enable: false, Level: fault_tolerance.Level_METHOD},
				{Name: "ins-rule", Enable: false, Level: fault_tolerance.Level_INSTANCE},
			},
		},
	}
	assert.False(t, hasAnyEnabledRule(object, nil))
}

// TestHasAnyEnabledRule_MixedEnabledDisabled 测试场景：部分 enable 部分 disable
// 前置条件：一条 SERVICE disable，一条 METHOD enable
// 预期结果：返回 true（任一条 enabled 即拦截）
func TestHasAnyEnabledRule_MixedEnabledDisabled(t *testing.T) {
	object := &model.ServiceRuleResponse{
		Value: &fault_tolerance.CircuitBreaker{
			Rules: []*fault_tolerance.CircuitBreakerRule{
				{Name: "svc-rule", Enable: false, Level: fault_tolerance.Level_SERVICE},
				{Name: "method-rule", Enable: true, Level: fault_tolerance.Level_METHOD},
			},
		},
	}
	assert.True(t, hasAnyEnabledRule(object, nil))
}

// TestHasAnyEnabledRule_NilValue 测试场景：Value 为 nil 或类型不匹配
// 前置条件：object不为 nil，但 Value=nil 或 Value 不是 CircuitBreaker 类型
// 预期结果：返回 false
func TestHasAnyEnabledRule_NilValue(t *testing.T) {
	object := &model.ServiceRuleResponse{}
	assert.False(t, hasAnyEnabledRule(object, nil))
}

// TestHasAnyEnabledRule_UnknownLevelNotIntercepted 测试场景：UNKNOWN Level 不被视为拦截
// 前置条件：仅一条 UNKNOWN Level 的 enabled 规则
// 预期结果：返回 false（UNKNOWN 不在合法 Level 集合内）
func TestHasAnyEnabledRule_UnknownLevelNotIntercepted(t *testing.T) {
	object := &model.ServiceRuleResponse{
		Value: &fault_tolerance.CircuitBreaker{
			Rules: []*fault_tolerance.CircuitBreakerRule{
				{Name: "bad-rule", Enable: true, Level: fault_tolerance.Level_UNKNOWN},
			},
		},
	}
	assert.False(t, hasAnyEnabledRule(object, nil))
}

// ============================================================
// 第6组：验证熔断恢复条件——熔断时长和恢复成功请求数量
// ============================================================

// TestHalfOpenStatus_MaxRequestFromConsecutiveSuccess 测试场景：maxRequest 等于 consecutiveSuccess
// 前置条件：用不同 consecutiveSuccess 值构造 HalfOpenStatus
// 预期结果：maxRequest 与 consecutiveSuccess 一致；GetStatus 返回 HalfOpen
func TestHalfOpenStatus_MaxRequestFromConsecutiveSuccess(t *testing.T) {
	tests := []struct {
		name               string
		consecutiveSuccess int
		wantMaxRequest     int
	}{
		{name: "consecutive_1", consecutiveSuccess: 1, wantMaxRequest: 1},
		{name: "consecutive_3", consecutiveSuccess: 3, wantMaxRequest: 3},
		{name: "consecutive_5", consecutiveSuccess: 5, wantMaxRequest: 5},
		{name: "consecutive_10", consecutiveSuccess: 10, wantMaxRequest: 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := model.NewHalfOpenStatus("test-rule", time.Now(), tt.consecutiveSuccess)
			hs := status.(*model.HalfOpenStatus)
			assert.Equal(t, model.HalfOpen, hs.GetStatus())
			assert.Equal(t, "test-rule", hs.GetCircuitBreaker())
			assert.False(t, hs.GetStartTime().IsZero())
			assert.Equal(t, tt.wantMaxRequest, hs.MaxRequest())
		})
	}
}

// TestHalfOpenStatus_AcquirePermission_WithinQuota 测试场景：配额内 AcquirePermission 返回 true
// 前置条件：maxRequest=3，连续调用 3 次 AcquirePermission
// 预期结果：前 3 次返回 true，第 4 次返回 false
func TestHalfOpenStatus_AcquirePermission_WithinQuota(t *testing.T) {
	hs := model.NewHalfOpenStatus("test", time.Now(), 3).(*model.HalfOpenStatus)
	assert.True(t, hs.AcquirePermission())
	assert.True(t, hs.AcquirePermission())
	assert.True(t, hs.AcquirePermission())
	assert.False(t, hs.AcquirePermission(), "4th acquire should fail over quota")
	assert.Equal(t, int64(3), hs.AllocatedCount())
}

// TestHalfOpenStatus_AcquirePermission_ZeroMaxRequest 测试场景：maxRequest=0 直接拒绝
// 前置条件：maxRequest=0
// 预期结果：AcquirePermission 始终 false；allocated 不增长
func TestHalfOpenStatus_AcquirePermission_ZeroMaxRequest(t *testing.T) {
	hs := model.NewHalfOpenStatus("zero", time.Now(), 0).(*model.HalfOpenStatus)
	assert.False(t, hs.AcquirePermission())
	assert.Equal(t, int64(0), hs.AllocatedCount())
}

// TestHalfOpenStatus_Release_AllSuccessTriggersClose 测试场景：全部成功且达阈值 → Close
// 前置条件：maxRequest=2，连续 Release(true) 两次
// 预期结果：第二次 Release 返回 true；CalNextStatus 返回 Close
func TestHalfOpenStatus_Release_AllSuccessTriggersClose(t *testing.T) {
	hs := model.NewHalfOpenStatus("rule", time.Now(), 2).(*model.HalfOpenStatus)
	assert.False(t, hs.Release(true))
	assert.True(t, hs.Release(true))
	assert.Equal(t, int64(2), hs.FinishedCount())
	assert.Equal(t, model.Close, hs.CalNextStatus())
}

// TestHalfOpenStatus_Release_AnyFailureTriggersOpen 测试场景：任一次失败 → Open
// 前置条件：maxRequest=3，Release(true) 一次后 Release(false)
// 预期结果：failure 那次返回 true；CalNextStatus 返回 Open
func TestHalfOpenStatus_Release_AnyFailureTriggersOpen(t *testing.T) {
	hs := model.NewHalfOpenStatus("rule", time.Now(), 3).(*model.HalfOpenStatus)
	assert.False(t, hs.Release(true))
	assert.True(t, hs.Release(false))
	assert.Equal(t, model.Open, hs.CalNextStatus())
}

// TestHalfOpenStatus_Release_NotEnoughKeepsHalfOpen 测试场景：未达阈值且全成功 → 保持 HalfOpen
// 前置条件：maxRequest=5，仅 Release(true) 两次
// 预期结果：CalNextStatus 返回 HalfOpen
func TestHalfOpenStatus_Release_NotEnoughKeepsHalfOpen(t *testing.T) {
	hs := model.NewHalfOpenStatus("rule", time.Now(), 5).(*model.HalfOpenStatus)
	assert.False(t, hs.Release(true))
	assert.False(t, hs.Release(true))
	assert.Equal(t, model.HalfOpen, hs.CalNextStatus())
}

// TestHalfOpenStatus_Report_AllSuccess 测试场景：Report 路径全部成功达阈值
// 前置条件：maxRequest=2，Report(true) 两次
// 预期结果：第二次 Report 返回 true；CalNextStatus 返回 Close
func TestHalfOpenStatus_Report_AllSuccess(t *testing.T) {
	hs := model.NewHalfOpenStatus("rule", time.Now(), 2).(*model.HalfOpenStatus)
	assert.False(t, hs.Report(true))
	assert.True(t, hs.Report(true))
	assert.Equal(t, model.Close, hs.CalNextStatus())
}

// TestHalfOpenStatus_Report_AnyFailure 测试场景：Report 路径任一次失败
// 前置条件：maxRequest=3，Report(true) 一次后 Report(false)
// 预期结果：失败那次返回 true；CalNextStatus 返回 Open
func TestHalfOpenStatus_Report_AnyFailure(t *testing.T) {
	hs := model.NewHalfOpenStatus("rule", time.Now(), 3).(*model.HalfOpenStatus)
	assert.False(t, hs.Report(true))
	assert.True(t, hs.Report(false))
	assert.Equal(t, model.Open, hs.CalNextStatus())
}

// TestHalfOpenStatus_Schedule_PreventsDuplicate 测试场景：Schedule 防重复调度
// 前置条件：初始 schedule=false；连续两次 Schedule()
// 预期结果：第一次返回 true（首次调度）；第二次返回 false（已标记）
func TestHalfOpenStatus_Schedule_PreventsDuplicate(t *testing.T) {
	hs := model.NewHalfOpenStatus("rule", time.Now(), 3).(*model.HalfOpenStatus)
	assert.True(t, hs.Schedule())
	assert.False(t, hs.Schedule())
}

// TestHalfOpenStatus_GetFallbackInfo 测试场景：HalfOpenStatus 未设置降级信息时返回 nil
// 前置条件：通过 NewHalfOpenStatus 构造
// 预期结果：GetFallbackInfo 返回 nil
func TestHalfOpenStatus_GetFallbackInfo(t *testing.T) {
	hs := model.NewHalfOpenStatus("rule", time.Now(), 3)
	assert.Nil(t, hs.GetFallbackInfo())
}

// ============================================================
// 第7组：验证 sortCircuitBreakerRules 排序逻辑（对齐 Java 三级比较器）
// ============================================================

// buildSortedRule 构造一条用于排序验证的熔断规则
// priority 控制优先级（数值越小越高），destNs/destSvc 控制目标服务匹配精确度，
// ruleId 用于末级兜底排序。
func buildSortedRule(priority uint32, destNs, destSvc string, ruleId string) *fault_tolerance.CircuitBreakerRule {
	return &fault_tolerance.CircuitBreakerRule{
		Id:       ruleId,
		Priority: priority,
		Level:    fault_tolerance.Level_SERVICE,
		RuleMatcher: &fault_tolerance.RuleMatcher{
			Source: &fault_tolerance.RuleMatcher_SourceService{
				Namespace: "*", Service: "*",
			},
			Destination: &fault_tolerance.RuleMatcher_DestinationService{
				Namespace: destNs, Service: destSvc,
			},
		},
	}
}

// TestSortCircuitBreakerRules_ByPriority 测试场景：Priority 作为第一排序键
// 前置条件：规则 Priority 不同（10, 5, 20）
// 预期结果：按 Priority 升序排列（5, 10, 20），数值小的优先级更高
func TestSortCircuitBreakerRules_ByPriority(t *testing.T) {
	rules := []*fault_tolerance.CircuitBreakerRule{
		buildSortedRule(10, "default", "svc1", "r3"),
		buildSortedRule(5, "default", "svc1", "r1"),
		buildSortedRule(20, "default", "svc1", "r2"),
	}
	sorted := sortCircuitBreakerRules(rules)
	assert.Len(t, sorted, 3)
	assert.Equal(t, uint32(5), sorted[0].Priority)
	assert.Equal(t, uint32(10), sorted[1].Priority)
	assert.Equal(t, uint32(20), sorted[2].Priority)
}

// TestSortCircuitBreakerRules_PriorityZeroFirst 测试场景：Priority=0 的规则优先级最高
// 前置条件：Priority 分别为 0, 0, 100
// 预期结果：Priority=0 的两条排前，Priority=100 排后
func TestSortCircuitBreakerRules_PriorityZeroFirst(t *testing.T) {
	rules := []*fault_tolerance.CircuitBreakerRule{
		buildSortedRule(100, "default", "svc1", "r3"),
		buildSortedRule(0, "default", "svc1", "r1"),
		buildSortedRule(0, "default", "svc1", "r2"),
	}
	sorted := sortCircuitBreakerRules(rules)
	assert.Len(t, sorted, 3)
	assert.Equal(t, uint32(0), sorted[0].Priority)
	assert.Equal(t, uint32(0), sorted[1].Priority)
	assert.Equal(t, uint32(100), sorted[2].Priority)
}

// TestSortCircuitBreakerRules_PriorityThenDestinationService 测试场景：Priority 相同 → 按 destination service 排序
// 前置条件：Priority 均为 10，但 destination service 精确度不同（通配 vs 精确）
// 预期结果：精确匹配的服务排前，通配符 * 排后
func TestSortCircuitBreakerRules_PriorityThenDestinationService(t *testing.T) {
	rules := []*fault_tolerance.CircuitBreakerRule{
		buildSortedRule(10, "*", "*", "r3"),
		buildSortedRule(10, "Production", "payment", "r1"),
		buildSortedRule(10, "Production", "order", "r2"),
	}
	sorted := sortCircuitBreakerRules(rules)
	assert.Len(t, sorted, 3)
	// 精确匹配优先于通配：Production/order 和 Production/payment 在前，*/* 在后
	assert.Equal(t, "Production", sorted[0].RuleMatcher.GetDestination().GetNamespace())
	assert.Equal(t, "Production", sorted[1].RuleMatcher.GetDestination().GetNamespace())
	assert.Equal(t, "*", sorted[2].RuleMatcher.GetDestination().GetNamespace())
}

// TestSortCircuitBreakerRules_PriorityThenId 测试场景：Priority 和 destination service 都相同 → 按 ID 字典序
// 前置条件：Priority 相同、destination service 相同，ID 分别为 "r2", "r1", "r10"
// 预期结果：按 ID 字典序排列（"r1", "r10", "r2"）
func TestSortCircuitBreakerRules_PriorityThenId(t *testing.T) {
	rules := []*fault_tolerance.CircuitBreakerRule{
		buildSortedRule(10, "default", "svc1", "r2"),
		buildSortedRule(10, "default", "svc1", "r1"),
		buildSortedRule(10, "default", "svc1", "r10"),
	}
	sorted := sortCircuitBreakerRules(rules)
	assert.Len(t, sorted, 3)
	assert.Equal(t, "r1", sorted[0].Id)
	assert.Equal(t, "r10", sorted[1].Id)
	assert.Equal(t, "r2", sorted[2].Id)
}

// TestSortCircuitBreakerRules_EmptyInput 测试场景：空规则集不 panic
// 前置条件：输入空切片
// 预期结果：返回空切片，不 panic
func TestSortCircuitBreakerRules_EmptyInput(t *testing.T) {
	sorted := sortCircuitBreakerRules(nil)
	assert.Empty(t, sorted)

	sorted = sortCircuitBreakerRules([]*fault_tolerance.CircuitBreakerRule{})
	assert.Empty(t, sorted)
}

// TestSortCircuitBreakerRules_SingleRule 测试场景：单条规则排序后仍为单条
// 前置条件：仅一条规则
// 预期结果：返回该规则，不 panic
func TestSortCircuitBreakerRules_SingleRule(t *testing.T) {
	rules := []*fault_tolerance.CircuitBreakerRule{
		buildSortedRule(5, "Production", "payment", "rule1"),
	}
	sorted := sortCircuitBreakerRules(rules)
	assert.Len(t, sorted, 1)
	assert.Equal(t, "rule1", sorted[0].Id)
}
