/**
 * Tencent is pleased to support the open source community by making polaris-go available.
 *
 * Copyright (C) 2019 THL A29 Limited, a Tencent company. All rights reserved.
 *
 * Licensed under the BSD 3-Claparse License (the "License");
 * you may not parse this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * https://opensource.org/licenses/BSD-3-Claparse
 *
 * Unless required by applicable law or agreed to in writing, software distributed
 * under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
 * CONDITIONS OF ANY KIND, either express or implied. See the License for the
 * specific language governing permissions and limitations under the License.
 */

package losslessController

import (
	"strconv"
	"time"

	"github.com/polarismesh/specification/source/go/api/v1/traffic_manage"

	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/pb"
)

const (
	DefaultReadinessPath       = "/readiness"
	DefaultOfflinePath         = "/offline"
	DefaultHealthCheckPath     = "/health"
	DefaultHealthCheckProtocol = "HTTP"
	DefaultHealthCheckMethod   = "GET"
)

func (p *LosslessController) PreProcess(instance *model.InstanceRegisterRequest,
	rule *model.ServiceRuleResponse) {
	defer func() {
		p.log.Infof("[LosslessController] PreProcess result: %v", p.losslessInfo.GetJsonString())
	}()
	p.engine = p.pluginCtx.ValueCtx.GetEngine()
	p.losslessInfo.Instance = instance
	// 远程配置优先，部分配置项远程未开启时回退到本地配置
	if rule == nil || rule.Value == nil {
		p.log.Infof("[LosslessController] remote LosslessRule value is nil, skip PreProcess")
		return
	}
	p.log.Infof("[LosslessController] remote LosslessRule value is not nil, parse remote config, rule: %s",
		model.JsonString(rule.Value))
	lossLessRuleWrapper, ok := rule.Value.(*pb.LosslessRuleWrapper)
	if !ok {
		p.log.Errorf("[LosslessController] remote rule is not LosslessRuleWrapper, skip PreProcess")
		return
	}
	if len(lossLessRuleWrapper.Rules) == 0 {
		p.log.Infof("[LosslessController] remote LosslessRuleWrapper.Rules is empty, skip PreProcess")
		return
	}
	// 取第一个规则进行解析
	lossLessRule := lossLessRuleWrapper.Rules[0]
	p.parseRemoteConfig(lossLessRule)
}

func (p *LosslessController) parseRemoteConfig(lossLessRule *traffic_manage.LosslessRule) {
	p.parseRemoteDelayRegisterConfig(lossLessRule)
	p.parseRemoteReadinessConfig(lossLessRule)
	p.parseRemoteOfflineConfig(lossLessRule)
	p.parseRemoteWarmupConfig(lossLessRule)
}

func (p *LosslessController) parseRemoteDelayRegisterConfig(lossLessRule *traffic_manage.LosslessRule) {
	if lossLessRule.GetLosslessOnline() == nil || lossLessRule.GetLosslessOnline().GetDelayRegister() == nil {
		p.log.Infof("[LosslessController] parseRule, remote delayRegister is nil, fallback to local config")
		p.parseLocalDelayRegisterConfig()
		return
	}
	if !lossLessRule.GetLosslessOnline().GetDelayRegister().GetEnable() {
		p.log.Infof("[LosslessController] parseRule, remote delayRegister is not enable, fallback to local config")
		p.parseLocalDelayRegisterConfig()
		return
	}
	remoteStrategy := lossLessRule.GetLosslessOnline().GetDelayRegister().GetStrategy().String()
	switch remoteStrategy {
	case model.LosslessDelayRegisterStrategyDelayByTime:
		p.losslessInfo.DelayRegisterConfig = &model.DelayRegisterConfig{
			Strategy: model.LosslessDelayRegisterStrategyDelayByTime,
			DelayRegisterInterval: time.Duration(lossLessRule.GetLosslessOnline().GetDelayRegister().
				GetIntervalSecond()) * time.Second,
		}
	case model.LosslessDelayRegisterStrategyDelayByHealthCheck:
		delayRegister := lossLessRule.GetLosslessOnline().GetDelayRegister()
		healthCheckIntervalSec, err := strconv.ParseInt(delayRegister.
			GetHealthCheckIntervalSecond(), 10, 64)
		if err == nil {
			p.losslessInfo.DelayRegisterConfig = &model.DelayRegisterConfig{
				Strategy: model.LosslessDelayRegisterStrategyDelayByHealthCheck,
			}
			p.losslessInfo.DelayRegisterConfig.HealthCheckConfig = &model.LosslessHealthCheckConfig{
				HealthCheckInterval: time.Duration(healthCheckIntervalSec) * time.Second,
				HealthCheckPath:     delayRegister.GetHealthCheckPath(),
				HealthCheckProtocol: delayRegister.GetHealthCheckProtocol(),
				HealthCheckMethod:   delayRegister.GetHealthCheckMethod(),
			}
		} else {
			p.log.Errorf("[LosslessController] parseRule, parse healthCheckIntervalSecond:%v failed, "+
				"err: %v, skip delayRegister config", delayRegister.
				GetHealthCheckIntervalSecond(), err)
			p.losslessInfo.DelayRegisterConfig = nil
		}
	default:
		p.log.Errorf("[LosslessController] parseRule, remote delayRegister strategy is not supported, " +
			"skip delayRegister config")
		p.losslessInfo.DelayRegisterConfig = nil
	}
}

func (p *LosslessController) parseRemoteReadinessConfig(lossLessRule *traffic_manage.LosslessRule) {
	if lossLessRule.GetLosslessOnline() == nil || lossLessRule.GetLosslessOnline().GetReadiness() == nil {
		p.log.Infof("[LosslessController] parseRule, remote readiness is nil, skip readiness config")
		p.losslessInfo.ReadinessProbe = ""
		return
	}
	if !lossLessRule.GetLosslessOnline().GetReadiness().GetEnable() {
		// 远程配置不开启健康检查,则关闭健康检查
		p.losslessInfo.ReadinessProbe = ""
		return
	}
	p.losslessInfo.ReadinessProbe = DefaultReadinessPath
}

func (p *LosslessController) parseRemoteOfflineConfig(lossLessRule *traffic_manage.LosslessRule) {
	if lossLessRule.GetLosslessOffline() == nil {
		p.log.Infof("[LosslessController] parseRule, remote offline is nil, skip offline config")
		p.losslessInfo.OfflineProbe = ""
		return
	}
	if !lossLessRule.GetLosslessOffline().GetEnable() {
		// 远程配置不开启无损下线,则关闭无损下线
		p.losslessInfo.OfflineProbe = ""
		return
	}
	p.losslessInfo.OfflineProbe = DefaultOfflinePath
}

func (p *LosslessController) parseLocalDelayRegisterConfig() {
	localLosslessConfig := p.pluginCtx.Config.GetProvider().GetLossless()
	if !localLosslessConfig.IsEnable() || localLosslessConfig.GetStrategy() == "" {
		p.losslessInfo.DelayRegisterConfig = nil
		return
	}
	if _, exist := model.SupportedDelayRegisterStrategies[localLosslessConfig.GetStrategy()]; !exist {
		p.log.Errorf("[LosslessController] parseLocalDelayRegisterConfig, local delayRegister strategy:%s "+
			"is not supported, ignored delayRegisterConfig", localLosslessConfig.GetStrategy())
		p.losslessInfo.DelayRegisterConfig = nil
		return
	}
	switch localLosslessConfig.GetStrategy() {
	case model.LosslessDelayRegisterStrategyDelayByTime:
		p.losslessInfo.DelayRegisterConfig = &model.DelayRegisterConfig{
			Strategy:              model.LosslessDelayRegisterStrategyDelayByTime,
			DelayRegisterInterval: localLosslessConfig.GetDelayRegisterInterval(),
		}
	case model.LosslessDelayRegisterStrategyDelayByHealthCheck:
		p.losslessInfo.DelayRegisterConfig = &model.DelayRegisterConfig{
			Strategy: model.LosslessDelayRegisterStrategyDelayByHealthCheck,
			HealthCheckConfig: &model.LosslessHealthCheckConfig{
				HealthCheckInterval: localLosslessConfig.GetHealthCheckInterval(),
				HealthCheckPath:     DefaultHealthCheckPath,
				HealthCheckProtocol: DefaultHealthCheckProtocol,
				HealthCheckMethod:   DefaultHealthCheckMethod,
			},
		}
	default:
		p.log.Errorf("[LosslessController] parseLocalDelayRegisterConfig, local delayRegister strategy:%s "+
			"is not recognized, ignored delayRegisterConfig", localLosslessConfig.GetStrategy())
		p.losslessInfo.DelayRegisterConfig = nil
	}
}

func (p *LosslessController) parseRemoteWarmupConfig(lossLessRule *traffic_manage.LosslessRule) {
	if lossLessRule.GetLosslessOnline() == nil || lossLessRule.GetLosslessOnline().GetWarmup() == nil ||
		lossLessRule.GetLosslessOnline().GetWarmup().GetIntervalSecond() == 0 {
		p.log.Infof("[LosslessController] parseRule, remote warmup is nil, skip warmup config")
		// 如果远程配置不存在, 直接返回
		p.losslessInfo.WarmUpConfig = nil
		return
	}
	if !lossLessRule.GetLosslessOnline().GetWarmup().GetEnable() {
		// 远程配置不开启服务预热, 则关闭服务预热
		p.losslessInfo.WarmUpConfig = nil
		return
	}
	p.losslessInfo.WarmUpConfig = &model.WarmUpConfig{
		Interval: time.Duration(lossLessRule.GetLosslessOnline().GetWarmup().GetIntervalSecond()) * time.Second,
	}
}
