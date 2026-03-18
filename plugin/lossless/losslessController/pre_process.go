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

func (p *LosslessController) PreProcess(instance *model.InstanceRegisterRequest,
	rule *model.ServiceRuleResponse) {
	defer func() {
		p.log.Infof("[LosslessController] PreProcess result: %v", p.losslessInfo.GetJsonString())
	}()
	p.engine = p.pluginCtx.ValueCtx.GetEngine()
	p.losslessInfo.Instance = instance
	// 远程配置优先级更高,如果远程配置不存在,则使用本地配置
	if rule == nil || rule.Value == nil {
		p.log.Infof("[LosslessController] LosslessRule value is nil, fallback to parse local "+
			"config, p.losslessInfo: %v", p.losslessInfo.GetJsonString())
		p.parseLocalConfig()
		return
	}
	p.log.Infof("[LosslessController] LosslessRule value is not nil, parse remote config, rule: %s", model.JsonString(
		rule.Value))
	lossLessRuleWrapper, ok := rule.Value.(*pb.LosslessRuleWrapper)
	if !ok {
		p.log.Infof("[LosslessController] rule is not LosslessRuleWrapper, fallback to parse local "+
			"config, p.losslessInfo: %v", p.losslessInfo)
		// 解析远程规则失败,使用本地配置
		p.parseLocalConfig()
		return
	}
	if len(lossLessRuleWrapper.Rules) == 0 {
		p.log.Infof("[LosslessController] LosslessRuleWrapper.Rules is empty, fallback to parse local "+
			"config, p.losslessInfo: %v", p.losslessInfo)
		p.parseLocalConfig()
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
		p.log.Infof("[LosslessController] parseRule, remote delayRegister is nil, fallback to parse local" +
			" config")
		// 如果远程配置不存在,则使用本地配置
		p.parseLocalDelayRegisterConfig()
		return
	}
	if !lossLessRule.GetLosslessOnline().GetDelayRegister().GetEnable() {
		p.log.Infof("[LosslessController] parseRule, remote delayRegister is not enable")
		// 远程配置不开启延迟注册,则关闭延迟注册
		p.losslessInfo.DelayRegisterConfig = nil
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
		healthCheckIntervalSec, err := strconv.ParseInt(lossLessRule.GetLosslessOnline().GetDelayRegister().
			GetHealthCheckIntervalSecond(), 10, 64)
		if err == nil {
			p.losslessInfo.DelayRegisterConfig = &model.DelayRegisterConfig{
				Strategy: model.LosslessDelayRegisterStrategyDelayByHealthCheck,
			}
			p.losslessInfo.DelayRegisterConfig.HealthCheckConfig = &model.LosslessHealthCheckConfig{
				HealthCheckInterval: time.Duration(healthCheckIntervalSec) * time.Second,
				HealthCheckPath:     p.pluginCfg.HealthCheckPath,
				HealthCheckProtocol: p.pluginCfg.HealthCheckProtocol,
				HealthCheckMethod:   p.pluginCfg.HealthCheckMethod,
			}
		} else {
			p.log.Errorf("[LosslessController] parseRule, parse healthCheckIntervalSecond:%v failed, "+
				"err: %v, fallback to parse local config", lossLessRule.GetLosslessOnline().GetDelayRegister().
				GetHealthCheckIntervalSecond(), err)
			p.parseLocalDelayRegisterConfig()
		}
	default:
		p.log.Errorf("[LosslessController] parseRule, remote delayRegister strategy is not supported, " +
			"fall back to parse local config")
		p.parseLocalDelayRegisterConfig()
	}
}

func (p *LosslessController) parseRemoteReadinessConfig(lossLessRule *traffic_manage.LosslessRule) {
	if lossLessRule.GetLosslessOnline() == nil || lossLessRule.GetLosslessOnline().GetReadiness() == nil {
		p.log.Infof("[LosslessController] parseRule, remote readiness is nil, fallback to parse local " +
			"config")
		// 如果远程配置不存在,则使用本地配置
		p.parseLocalReadinessConfig()
		return
	}
	if !lossLessRule.GetLosslessOnline().GetReadiness().GetEnable() {
		// 远程配置不开启健康检查,则关闭健康检查
		p.losslessInfo.ReadinessProbe = ""
		return
	}
	p.losslessInfo.ReadinessProbe = p.pluginCfg.ReadinessPath
}

func (p *LosslessController) parseRemoteOfflineConfig(lossLessRule *traffic_manage.LosslessRule) {
	if lossLessRule.GetLosslessOffline() == nil {
		p.log.Infof("[LosslessController] parseRule, remote offline is nil, fallback to parse local config")
		// 如果远程配置不存在,则使用本地配置
		p.parseLocalOfflineConfig()
		return
	}
	if !lossLessRule.GetLosslessOffline().GetEnable() {
		// 远程配置不开启无损下线,则关闭无损下线
		p.losslessInfo.OfflineProbe = ""
		return
	}
	p.losslessInfo.OfflineProbe = p.pluginCfg.OfflinePath
}

func (p *LosslessController) parseRemoteWarmupConfig(lossLessRule *traffic_manage.LosslessRule) {
	if lossLessRule.GetLosslessOnline() == nil || lossLessRule.GetLosslessOnline().GetWarmup() == nil ||
		lossLessRule.GetLosslessOnline().GetWarmup().GetIntervalSecond() == 0 {
		p.log.Infof("[LosslessController] parseRule, remote warmup is nil, fallback to parse local config")
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

func (p *LosslessController) parseLocalConfig() {
	p.parseLocalDelayRegisterConfig()
	p.parseLocalReadinessConfig()
	p.parseLocalOfflineConfig()
}

func (p *LosslessController) parseLocalDelayRegisterConfig() {
	localLosslessConfig := p.pluginCtx.Config.GetProvider().GetLossless()
	if !localLosslessConfig.IsEnable() || localLosslessConfig.GetStrategy() == "" {
		p.losslessInfo.DelayRegisterConfig = nil
		return
	}
	if _, exist := model.SupportedDelayRegisterStrategies[localLosslessConfig.GetStrategy()]; exist {
		switch localLosslessConfig.GetStrategy() {
		case model.LosslessDelayRegisterStrategyDelayByTime:
			p.losslessInfo.DelayRegisterConfig = &model.DelayRegisterConfig{
				Strategy:              localLosslessConfig.GetStrategy(),
				DelayRegisterInterval: localLosslessConfig.GetDelayRegisterInterval(),
			}
		case model.LosslessDelayRegisterStrategyDelayByHealthCheck:
			p.losslessInfo.DelayRegisterConfig = &model.DelayRegisterConfig{
				Strategy: localLosslessConfig.GetStrategy(),
				HealthCheckConfig: &model.LosslessHealthCheckConfig{
					HealthCheckInterval: localLosslessConfig.GetHealthCheckInterval(),
					HealthCheckPath:     p.pluginCfg.HealthCheckPath,
					HealthCheckProtocol: p.pluginCfg.HealthCheckProtocol,
					HealthCheckMethod:   p.pluginCfg.HealthCheckMethod,
				},
			}
		default:
			p.log.Errorf("[LosslessController] local delayRegister strategy:%s is not recognized, "+
				"ignored delayRegisterConfig", localLosslessConfig.GetStrategy())
			p.losslessInfo.DelayRegisterConfig = nil
		}
	} else {
		p.log.Errorf("[LosslessController] parseRule failed, local delayRegister strategy is not " +
			"supported, ignored delayRegisterConfig")
		p.losslessInfo.DelayRegisterConfig = nil
	}
}

func (p *LosslessController) parseLocalReadinessConfig() {
	if p.pluginCfg.ReadinessProbeEnabled {
		p.losslessInfo.ReadinessProbe = p.pluginCfg.ReadinessPath

	}
}

func (p *LosslessController) parseLocalOfflineConfig() {
	if p.pluginCfg.OfflineProbeEnabled {
		p.losslessInfo.OfflineProbe = p.pluginCfg.OfflinePath
	}
}
