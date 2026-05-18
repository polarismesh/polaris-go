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

package blockallowlist

import (
	"testing"

	apimodel "github.com/polarismesh/specification/source/go/api/v1/model"
	apisecurity "github.com/polarismesh/specification/source/go/api/v1/security"
	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/authenticator"
	"github.com/polarismesh/polaris-go/pkg/sdk"
)

const (
	testNamespace = "testNamespace"
	testService   = "testService"
	testPath      = "/path"
	testProtocol  = "HTTP"
	testMethod    = "GET"
)

// noopLoggerForTest 测试用空日志
type noopLoggerForTest struct{}

func (n *noopLoggerForTest) Tracef(format string, args ...interface{}) {}
func (n *noopLoggerForTest) Debugf(format string, args ...interface{}) {}
func (n *noopLoggerForTest) Infof(format string, args ...interface{})  {}
func (n *noopLoggerForTest) Warnf(format string, args ...interface{})  {}
func (n *noopLoggerForTest) Errorf(format string, args ...interface{}) {}
func (n *noopLoggerForTest) Fatalf(format string, args ...interface{}) {}
func (n *noopLoggerForTest) IsLevelEnabled(l int) bool                 { return true }
func (n *noopLoggerForTest) SetLogLevel(l int) error                   { return nil }

// newTestAuthenticator 创建测试鉴权器，不挂接 engine（测试 checkAllow 不需要拉规则）
func newTestAuthenticator() *BlockAllowListAuthenticator {
	return &BlockAllowListAuthenticator{
		log: &noopLoggerForTest{},
	}
}

// newTestAuthInfo 构造与 Java 测试一致的 AuthInfo
func newTestAuthInfo(path string) *authenticator.AuthInfo {
	return &authenticator.AuthInfo{
		Namespace: testNamespace,
		Service:   testService,
		Path:      path,
		Protocol:  testProtocol,
		Method:    testMethod,
	}
}

// createBlockAllowListRule 与 Java 同名 helper：构造单个规则（path/protocol/method + 策略）
func createBlockAllowListRule(enable bool, path string,
	policy apisecurity.BlockAllowConfig_BlockAllowPolicy) *apisecurity.BlockAllowListRule {
	return &apisecurity.BlockAllowListRule{
		Enable: enable,
		BlockAllowConfig: []*apisecurity.BlockAllowConfig{
			{
				Api: &apimodel.API{
					Protocol: testProtocol,
					Method:   testMethod,
					Path: &apimodel.MatchString{
						Type:  apimodel.MatchString_EXACT,
						Value: wrapperspb.String(path),
					},
				},
				BlockAllowPolicy: policy,
			},
		},
	}
}

// 1) 空规则列表 → 通过
func TestCheckAllow_EmptyRuleList_ShouldPass(t *testing.T) {
	auth := newTestAuthenticator()
	info := newTestAuthInfo(testPath)
	rules := []*apisecurity.BlockAllowListRule{}

	allowed, _ := auth.checkAllow(info, rules)

	assert.True(t, allowed)
}

// 2) 规则禁用（enable=false）→ 通过
func TestCheckAllow_DisabledRule_ShouldPass(t *testing.T) {
	auth := newTestAuthenticator()
	info := newTestAuthInfo(testPath)
	rules := []*apisecurity.BlockAllowListRule{
		createBlockAllowListRule(false, testPath, apisecurity.BlockAllowConfig_ALLOW_LIST),
	}

	allowed, _ := auth.checkAllow(info, rules)

	assert.True(t, allowed)
}

// 3) 仅白名单且匹配 → 通过
func TestCheckAllow_OnlyAllowList_Match_ShouldPass(t *testing.T) {
	auth := newTestAuthenticator()
	info := newTestAuthInfo(testPath)
	rules := []*apisecurity.BlockAllowListRule{
		createBlockAllowListRule(true, testPath, apisecurity.BlockAllowConfig_ALLOW_LIST),
	}

	allowed, _ := auth.checkAllow(info, rules)

	assert.True(t, allowed)
}

// 4) 仅黑名单且匹配 → 拒绝；reason 应指向命中的黑名单规则
func TestCheckAllow_OnlyBlockList_Match_ShouldReject(t *testing.T) {
	auth := newTestAuthenticator()
	info := newTestAuthInfo(testPath)
	rules := []*apisecurity.BlockAllowListRule{
		createBlockAllowListRule(true, testPath, apisecurity.BlockAllowConfig_BLOCK_LIST),
	}

	allowed, reason := auth.checkAllow(info, rules)

	assert.False(t, allowed)
	assert.Equal(t, 0, reason.ruleIndex)
	assert.Equal(t, 0, reason.cfgIndex)
	assert.Equal(t, apisecurity.BlockAllowConfig_BLOCK_LIST, reason.policy)
	assert.Contains(t, reason.summary, "BLOCK_LIST")
	assert.Contains(t, reason.summary, "rule[0]")
}

// 5) 仅白名单但不匹配 → 拒绝（containsAllowList=true 时未匹配返回 false）；
// 此时未命中任何规则，ruleIndex/cfgIndex 应为 -1，summary 说明兜底原因
func TestCheckAllow_OnlyAllowList_NotMatch_ShouldReject(t *testing.T) {
	auth := newTestAuthenticator()
	info := newTestAuthInfo("/no-test")
	rules := []*apisecurity.BlockAllowListRule{
		createBlockAllowListRule(true, testPath, apisecurity.BlockAllowConfig_ALLOW_LIST),
	}

	allowed, reason := auth.checkAllow(info, rules)

	assert.False(t, allowed)
	assert.Equal(t, -1, reason.ruleIndex)
	assert.Equal(t, -1, reason.cfgIndex)
	assert.Contains(t, reason.summary, "no allow-list rule matched")
}

// 6) 仅黑名单但不匹配 → 通过
func TestCheckAllow_OnlyBlockList_NotMatch_ShouldPass(t *testing.T) {
	auth := newTestAuthenticator()
	info := newTestAuthInfo("/no-test")
	rules := []*apisecurity.BlockAllowListRule{
		createBlockAllowListRule(true, testPath, apisecurity.BlockAllowConfig_BLOCK_LIST),
	}

	allowed, _ := auth.checkAllow(info, rules)

	assert.True(t, allowed)
}

// 7) 混合：白名单匹配 + 黑名单不匹配 → 通过
func TestCheckAllow_AllowMatch_BlockNotMatch_ShouldPass(t *testing.T) {
	auth := newTestAuthenticator()
	info := newTestAuthInfo(testPath)
	rules := []*apisecurity.BlockAllowListRule{
		createBlockAllowListRule(true, testPath, apisecurity.BlockAllowConfig_ALLOW_LIST),
		createBlockAllowListRule(true, "/no-test", apisecurity.BlockAllowConfig_BLOCK_LIST),
	}

	allowed, _ := auth.checkAllow(info, rules)

	assert.True(t, allowed)
}

// 8) 混合：白名单不匹配 + 黑名单匹配 → 拒绝；reason 应指向第二条黑名单规则
func TestCheckAllow_AllowNotMatch_BlockMatch_ShouldReject(t *testing.T) {
	auth := newTestAuthenticator()
	info := newTestAuthInfo("/no-test")
	rules := []*apisecurity.BlockAllowListRule{
		createBlockAllowListRule(true, testPath, apisecurity.BlockAllowConfig_ALLOW_LIST),
		createBlockAllowListRule(true, "/no-test", apisecurity.BlockAllowConfig_BLOCK_LIST),
	}

	allowed, reason := auth.checkAllow(info, rules)

	assert.False(t, allowed)
	assert.Equal(t, 1, reason.ruleIndex)
	assert.Equal(t, apisecurity.BlockAllowConfig_BLOCK_LIST, reason.policy)
}

// 9) 混合：白名单和黑名单都不匹配 → 拒绝（因为存在白名单规则）；reason 为兜底说明
func TestCheckAllow_BothNotMatch_ShouldReject(t *testing.T) {
	auth := newTestAuthenticator()
	info := newTestAuthInfo("/no-no-test")
	rules := []*apisecurity.BlockAllowListRule{
		createBlockAllowListRule(true, testPath, apisecurity.BlockAllowConfig_ALLOW_LIST),
		createBlockAllowListRule(true, "/no-test", apisecurity.BlockAllowConfig_BLOCK_LIST),
	}

	allowed, reason := auth.checkAllow(info, rules)

	assert.False(t, allowed)
	assert.Equal(t, -1, reason.ruleIndex)
	assert.Contains(t, reason.summary, "no allow-list rule matched")
}

// === 扩展：6 维 MatchArgument 取值测试 ===

// matchArgWithExact 构造 EXACT 类型的 MatchArgument
func matchArgWithExact(typ apisecurity.BlockAllowConfig_MatchArgument_Type, key, value string) *apisecurity.BlockAllowConfig_MatchArgument {
	return &apisecurity.BlockAllowConfig_MatchArgument{
		Type: typ,
		Key:  key,
		Value: &apimodel.MatchString{
			Type:  apimodel.MatchString_EXACT,
			Value: wrapperspb.String(value),
		},
	}
}

func TestGetLabelValue_Header(t *testing.T) {
	auth := newTestAuthenticator()
	info := newTestAuthInfo(testPath)
	info.Arguments = []model.Argument{
		model.BuildHeaderArgument("X-User-Id", "u123"),
	}
	arg := matchArgWithExact(apisecurity.BlockAllowConfig_MatchArgument_HEADER, "X-User-Id", "u123")

	assert.Equal(t, "u123", auth.getLabelValue(info, arg))
}

func TestGetLabelValue_Query(t *testing.T) {
	auth := newTestAuthenticator()
	info := newTestAuthInfo(testPath)
	info.Arguments = []model.Argument{
		model.BuildQueryArgument("page", "10"),
	}
	arg := matchArgWithExact(apisecurity.BlockAllowConfig_MatchArgument_QUERY, "page", "10")

	assert.Equal(t, "10", auth.getLabelValue(info, arg))
}

func TestGetLabelValue_CallerService_Match(t *testing.T) {
	auth := newTestAuthenticator()
	info := newTestAuthInfo(testPath)
	info.SourceService = &model.ServiceInfo{
		Namespace: "ns-a",
		Service:   "svc-a",
	}
	arg := matchArgWithExact(apisecurity.BlockAllowConfig_MatchArgument_CALLER_SERVICE, "ns-a", "svc-a")

	assert.Equal(t, "svc-a", auth.getLabelValue(info, arg))
}

func TestGetLabelValue_CallerService_Wildcard(t *testing.T) {
	auth := newTestAuthenticator()
	info := newTestAuthInfo(testPath)
	info.SourceService = &model.ServiceInfo{
		Namespace: "any-ns",
		Service:   "svc-x",
	}
	arg := matchArgWithExact(apisecurity.BlockAllowConfig_MatchArgument_CALLER_SERVICE, "*", "svc-x")

	assert.Equal(t, "svc-x", auth.getLabelValue(info, arg))
}

func TestGetLabelValue_CallerIP(t *testing.T) {
	auth := newTestAuthenticator()
	info := newTestAuthInfo(testPath)
	info.Arguments = []model.Argument{
		model.BuildCallerIPArgument("1.2.3.4"),
	}
	arg := matchArgWithExact(apisecurity.BlockAllowConfig_MatchArgument_CALLER_IP, "", "1.2.3.4")

	assert.Equal(t, "1.2.3.4", auth.getLabelValue(info, arg))
}

func TestGetLabelValue_CallerMetadata(t *testing.T) {
	auth := newTestAuthenticator()
	info := newTestAuthInfo(testPath)
	info.SourceService = &model.ServiceInfo{
		Namespace: "ns",
		Service:   "svc",
		Metadata: map[string]string{
			"env": "prod",
		},
	}
	arg := matchArgWithExact(apisecurity.BlockAllowConfig_MatchArgument_CALLER_METADATA, "env", "prod")

	assert.Equal(t, "prod", auth.getLabelValue(info, arg))
}

func TestGetLabelValue_Custom_FromArguments(t *testing.T) {
	auth := newTestAuthenticator()
	info := newTestAuthInfo(testPath)
	info.Arguments = []model.Argument{
		model.BuildCustomArgument("biz", "v1"),
	}
	arg := matchArgWithExact(apisecurity.BlockAllowConfig_MatchArgument_CUSTOM, "biz", "v1")

	assert.Equal(t, "v1", auth.getLabelValue(info, arg))
}

func TestGetLabelValue_Custom_FallbackToMetadata(t *testing.T) {
	auth := newTestAuthenticator()
	info := newTestAuthInfo(testPath)
	info.SourceService = &model.ServiceInfo{
		Namespace: "ns",
		Service:   "svc",
		Metadata:  map[string]string{"biz": "fallback"},
	}
	arg := matchArgWithExact(apisecurity.BlockAllowConfig_MatchArgument_CUSTOM, "biz", "fallback")

	assert.Equal(t, "fallback", auth.getLabelValue(info, arg))
}

// === CALLEE_METADATA：从本端通过 Engine 缓存的实例 metadata 取值 ===

// fakeEngineForCallee 仅实现 GetLocalInstanceMetadata，其它方法 panic 提示误用。
// 鉴权 CALLEE_METADATA 取值只走 GetLocalInstanceMetadata，避免引入大量 mock 桩代码。
type fakeEngineForCallee struct {
	sdk.Engine // 嵌入 nil 接口，未实现的方法被调用时会 panic，能在测试中迅速暴露误用
	data       map[string][]map[string]string
}

func (f *fakeEngineForCallee) GetLocalInstanceMetadata(namespace, service string) []map[string]string {
	return f.data[namespace+"/"+service]
}

// TestGetLabelValue_CalleeMetadata_NoEngine engine 未注入时返回空
func TestGetLabelValue_CalleeMetadata_NoEngine(t *testing.T) {
	auth := newTestAuthenticator() // engine 字段为 nil
	info := newTestAuthInfo(testPath)
	arg := matchArgWithExact(apisecurity.BlockAllowConfig_MatchArgument_CALLEE_METADATA, "env", "prod")

	assert.Equal(t, "", auth.getLabelValue(info, arg))
}

// TestGetLabelValue_CalleeMetadata_Hit 命中本端登记的 metadata
func TestGetLabelValue_CalleeMetadata_Hit(t *testing.T) {
	auth := newTestAuthenticator()
	auth.engine = &fakeEngineForCallee{
		data: map[string][]map[string]string{
			testNamespace + "/" + testService: {
				{"env": "prod", "az": "az1"},
			},
		},
	}
	info := newTestAuthInfo(testPath)
	arg := matchArgWithExact(apisecurity.BlockAllowConfig_MatchArgument_CALLEE_METADATA, "env", "prod")

	assert.Equal(t, "prod", auth.getLabelValue(info, arg))
}

// TestGetLabelValue_CalleeMetadata_MultiInstanceAnyHit 多实例下任一命中即返回
func TestGetLabelValue_CalleeMetadata_MultiInstanceAnyHit(t *testing.T) {
	auth := newTestAuthenticator()
	auth.engine = &fakeEngineForCallee{
		data: map[string][]map[string]string{
			testNamespace + "/" + testService: {
				{"role": "primary"}, // 第一个实例不含 env
				{"env": "prod"},     // 第二个实例含目标 key
				{"role": "secondary"},
			},
		},
	}
	info := newTestAuthInfo(testPath)
	arg := matchArgWithExact(apisecurity.BlockAllowConfig_MatchArgument_CALLEE_METADATA, "env", "prod")

	assert.Equal(t, "prod", auth.getLabelValue(info, arg))
}

// TestGetLabelValue_CalleeMetadata_KeyMissing 所有实例都不含目标 key 时返回空
func TestGetLabelValue_CalleeMetadata_KeyMissing(t *testing.T) {
	auth := newTestAuthenticator()
	auth.engine = &fakeEngineForCallee{
		data: map[string][]map[string]string{
			testNamespace + "/" + testService: {
				{"az": "az1"},
				{"role": "primary"},
			},
		},
	}
	info := newTestAuthInfo(testPath)
	arg := matchArgWithExact(apisecurity.BlockAllowConfig_MatchArgument_CALLEE_METADATA, "env", "prod")

	assert.Equal(t, "", auth.getLabelValue(info, arg))
}

// === Authenticate 接口语义校验：未拉到规则时直接放行 ===
func TestAuthenticate_NoRule_ShouldPass(t *testing.T) {
	auth := newTestAuthenticator()
	// engine 未注入，getBlockAllowListRules 直接返回 nil
	info := newTestAuthInfo(testPath)

	result := auth.Authenticate(info)

	assert.NotNil(t, result)
	assert.Equal(t, authenticator.AuthResultOk, result.Code)
}

// === MatchMethod 行为校验 ===
func TestMatchMethod_NilApi_ShouldMatch(t *testing.T) {
	info := newTestAuthInfo(testPath)
	assert.True(t, matchMethod(info, nil))
}

func TestMatchMethod_ProtocolMismatch_ShouldNotMatch(t *testing.T) {
	info := newTestAuthInfo(testPath)
	api := &apimodel.API{Protocol: "GRPC", Method: testMethod}
	assert.False(t, matchMethod(info, api))
}

func TestMatchMethod_WildcardProtocolAndMethod_ShouldMatch(t *testing.T) {
	info := newTestAuthInfo(testPath)
	api := &apimodel.API{Protocol: "*", Method: ""}
	assert.True(t, matchMethod(info, api))
}
