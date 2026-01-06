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
	apiservice "github.com/polarismesh/specification/source/go/api/v1/service_manage"
	apitraffic "github.com/polarismesh/specification/source/go/api/v1/traffic_manage"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
)

// LosslessRuleWrapper 无损上下线规则包装器，用于将规则数组包装成 proto.Message
type LosslessRuleWrapper struct {
	Namespace *wrapperspb.StringValue
	Service   *wrapperspb.StringValue
	Rules     []*apitraffic.LosslessRule
	Revision  *wrapperspb.StringValue
}

// Reset 实现 proto.Message 接口
func (w *LosslessRuleWrapper) Reset() {}

// String 实现 proto.Message 接口
func (w *LosslessRuleWrapper) String() string { return "" }

// ProtoMessage 实现 proto.Message 接口
func (w *LosslessRuleWrapper) ProtoMessage() {}

// LossLessAssistant 无损上下线规则解析助手
type LossLessAssistant struct {
}

// ParseRuleValue 解析出具体的规则值
func (l *LossLessAssistant) ParseRuleValue(resp *apiservice.DiscoverResponse) (proto.Message, string) {
	var revision string
	serviceKey := ""
	if resp.Service != nil {
		serviceKey = resp.Service.GetNamespace().GetValue() + "/" + resp.Service.GetName().GetValue()
	}

	// 使用服务级别的 revision
	revision = resp.GetService().GetRevision().GetValue()

	// 返回 LosslessRules 数组
	losslessRules := resp.LosslessRules
	if len(losslessRules) == 0 {
		log.GetBaseLogger().Debugf("LossLessAssistant.ParseRuleValue: service=%s, no lossless rules found, using service revision=%s",
			serviceKey, revision)
		return nil, revision
	}

	log.GetBaseLogger().Debugf("LossLessAssistant.ParseRuleValue: service=%s, revision=%s, rules count=%d",
		serviceKey, revision, len(losslessRules))

	// 将规则数组包装到结构体中并返回
	wrapper := &LosslessRuleWrapper{
		Namespace: resp.Service.Namespace,
		Service:   resp.Service.Name,
		Rules:     losslessRules,
		Revision:  wrapperspb.String(revision),
	}

	return wrapper, revision
}

// Validate 规则校验
func (l *LossLessAssistant) Validate(message proto.Message, ruleCache model.RuleCache) error {
	if reflect2.IsNil(message) {
		return nil
	}
	// 无损上下线规则暂不需要特殊校验
	return nil
}

// SetDefault 设置默认值
func (l *LossLessAssistant) SetDefault(message proto.Message) {
	// do nothing
}
