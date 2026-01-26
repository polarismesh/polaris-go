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

package pb

import (
	"github.com/golang/protobuf/proto"
	"github.com/modern-go/reflect2"
	apisecurity "github.com/polarismesh/specification/source/go/api/v1/security"
	apiservice "github.com/polarismesh/specification/source/go/api/v1/service_manage"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
)

// BlockAllowRuleWrapper 鉴权规则包装器，用于将规则数组包装成 proto.Message
type BlockAllowRuleWrapper struct {
	Namespace *wrapperspb.StringValue
	Service   *wrapperspb.StringValue
	Rules     []*apisecurity.BlockAllowListRule
	Revision  *wrapperspb.StringValue
}

// Reset 实现 proto.Message 接口
func (w *BlockAllowRuleWrapper) Reset() {}

// String 实现 proto.Message 接口
func (w *BlockAllowRuleWrapper) String() string { return "" }

// ProtoMessage 实现 proto.Message 接口
func (w *BlockAllowRuleWrapper) ProtoMessage() {}

// BlockAllowAssistant 鉴权规则解析助手
type BlockAllowAssistant struct {
}

// ParseRuleValue 解析出具体的规则值
func (b *BlockAllowAssistant) ParseRuleValue(resp *apiservice.DiscoverResponse) (proto.Message, string) {
	var revision string
	serviceKey := ""
	if resp.Service != nil {
		serviceKey = resp.Service.GetNamespace().GetValue() + "/" + resp.Service.GetName().GetValue()
	}

	// 使用服务级别的 revision
	revision = resp.GetService().GetRevision().GetValue()

	// 返回 BlockAllowListRule 数组
	blockAllowListRule := resp.BlockAllowListRule
	if len(blockAllowListRule) == 0 {
		log.GetBaseLogger().Debugf("BlockAllowAssistant.ParseRuleValue: service=%s, no block allow rule found, using service revision=%s",
			serviceKey, revision)
		return nil, revision
	}

	log.GetBaseLogger().Debugf("BlockAllowAssistant.ParseRuleValue: service=%s, revision=%s, rules count=%d",
		serviceKey, revision, len(blockAllowListRule))

	// 将规则数组包装到结构体中并返回
	wrapper := &BlockAllowRuleWrapper{
		Namespace: resp.Service.Namespace,
		Service:   resp.Service.Name,
		Rules:     blockAllowListRule,
		Revision:  wrapperspb.String(revision),
	}

	return wrapper, revision
}

// Validate 规则校验
func (b *BlockAllowAssistant) Validate(message proto.Message, ruleCache model.RuleCache) error {
	if reflect2.IsNil(message) {
		return nil
	}
	// 鉴权规则暂不需要特殊校验
	return nil
}

// SetDefault 设置默认值
func (b *BlockAllowAssistant) SetDefault(message proto.Message) {
	// do nothing
}
