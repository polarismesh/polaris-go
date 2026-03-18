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

package losslessController

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/event"
	"github.com/polarismesh/polaris-go/pkg/plugin/admin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
)

func (p *LosslessController) Process() (*model.InstanceRegisterResponse, error) {
	if p.engine == nil {
		return nil, fmt.Errorf("failed to get engine from context")
	}
	if p.losslessInfo.Instance == nil {
		return nil, fmt.Errorf("instance is nil, PreProcess may not have been called")
	}
	if p.losslessInfo.IsDelayRegisterEnabled() {
		p.log.Infof("[LosslessController] Process, delay register enabled")
		p.genAndRunGraceProbe()
		p.reportEvent(event.GetLosslessEvent(event.LosslessOnlineStart, p.losslessInfo))
		if err := p.delayRegisterChecker(); err != nil {
			return nil, err
		}
	}
	resp, err := p.engine.SyncRegister(p.losslessInfo.Instance)
	if err != nil {
		p.log.Errorf("[LosslessController] Process, register failed, err: %v", err)
		return nil, err
	}
	p.reportEvent(event.GetLosslessEvent(event.LosslessOnlineEnd, p.losslessInfo))
	return resp, nil
}

func (p *LosslessController) genAndRunGraceProbe() {
	effectiveRule := p.losslessInfo
	if effectiveRule.IsReadinessProbeEnabled() || effectiveRule.IsOfflineProbeEnabled() {
		if effectiveRule.IsReadinessProbeEnabled() {
			p.log.Infof("[LosslessController] Process, readiness probe enabled")
			p.pluginCtx.Config.GetGlobal().GetAdmin().RegisterPath(model.AdminHandler{
				Path:        effectiveRule.ReadinessProbe,
				HandlerFunc: p.genReadinessProbe(),
			})
		}
		if effectiveRule.IsOfflineProbeEnabled() {
			p.log.Infof("[LosslessController] Process, offline probe enabled")
			p.pluginCtx.Config.GetGlobal().GetAdmin().RegisterPath(model.AdminHandler{
				Path:        effectiveRule.OfflineProbe,
				HandlerFunc: p.genPreStopProbe(),
			})
		}
		// 启动无损上下线接口
		p.serveOnAdmin()
	}
}

// registerToAdmin 将 metrics handler 注册到 admin 服务
func (p *LosslessController) serveOnAdmin() {
	adminType := p.pluginCtx.Config.GetGlobal().GetAdmin().GetType()
	targetPlugin, err := p.pluginCtx.Plugins.GetPlugin(common.TypeAdmin, adminType)
	if err != nil {
		log.GetBaseLogger().Errorf("[metrics][pull] get admin plugin fail: %v", err)
		return
	}
	adminPlugin := targetPlugin.(admin.Admin)
	adminPlugin.Run()
	p.log.Infof("[LosslessController] serveOnAdmin, admin plugin run success")
}

func (p *LosslessController) delayRegisterChecker() error {
	switch p.losslessInfo.DelayRegisterConfig.Strategy {
	case model.LosslessDelayRegisterStrategyDelayByTime:
		time.Sleep(p.losslessInfo.DelayRegisterConfig.DelayRegisterInterval)
		p.log.Infof("[LosslessController] DelayRegisterChecker, delay register checker finished by "+
			"time:%v(second)", p.losslessInfo.DelayRegisterConfig.DelayRegisterInterval)
		return nil
	case model.LosslessDelayRegisterStrategyDelayByHealthCheck:
		// 循环进行健康检查，直到成功
		times := 0
		for {
			if times > p.pluginCfg.HealthCheckMaxRetry {
				p.log.Errorf("[LosslessController] DelayRegisterChecker, health check retry times "+
					"exceeded: %v", times)
				return fmt.Errorf("health check retry times exceeded")
			}
			times++
			pass, err := p.doHealthCheck()
			if err != nil {
				p.log.Errorf("[LosslessController] DelayRegisterChecker, health check failed, err: %v", err)
				return err
			}
			if pass {
				p.log.Infof("[LosslessController] DelayRegisterChecker, health check success, " +
					"start to do register")
				return nil
			}
			p.log.Infof("[LosslessController] DelayRegisterChecker, health check failed, " +
				"wait for next check")
			// 健康检查失败，等待下一个检查间隔后重试
			time.Sleep(p.losslessInfo.DelayRegisterConfig.HealthCheckConfig.HealthCheckInterval)
		}
	default:
		p.log.Errorf("[LosslessController] DelayRegisterChecker, delay register strategy is not " +
			"supported, skip delay register checker")
		return fmt.Errorf("delay register strategy is not supported")
	}
}

// doHealthCheck 执行健康检查
func (p *LosslessController) doHealthCheck() (bool, error) {
	port := p.losslessInfo.Instance.Port
	config := p.losslessInfo.DelayRegisterConfig.HealthCheckConfig
	// 构建健康检查 URL
	protocol := strings.ToLower(config.HealthCheckProtocol)
	url := fmt.Sprintf("%s://localhost:%d%s", protocol, port, config.HealthCheckPath)

	p.log.Debugf("[LosslessController] doHealthCheck, url: %s, method: %s",
		url, config.HealthCheckMethod)

	// 创建 HTTP 请求
	req, err := http.NewRequest(config.HealthCheckMethod, url, nil)
	if err != nil {
		p.log.Errorf("[LosslessController] doHealthCheck, create request failed, err: %v", err)
		return false, err
	}

	// 设置超时时间（使用检查间隔的一半作为超时时间，但最少1秒）
	timeout := config.HealthCheckInterval / 2
	if timeout < time.Second {
		timeout = time.Second
	}
	client := &http.Client{
		Timeout: timeout,
	}

	// 执行请求
	resp, err := client.Do(req)
	if err != nil {
		p.log.Errorf("[LosslessController] doHealthCheck, request failed, err: %v", err)
		return false, err
	}
	defer resp.Body.Close()

	// 判断响应状态码，2xx 表示健康检查成功
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		p.log.Infof("[LosslessController] doHealthCheck, health check success, statusCode: %d",
			resp.StatusCode)
		return true, nil
	}
	p.log.Errorf("[LosslessController] doHealthCheck, health check failed, statusCode: %d, need retry",
		resp.StatusCode)
	return false, nil
}

func (p *LosslessController) genReadinessProbe() func(w http.
	ResponseWriter, r *http.Request) {
	HandlerFunc := func(w http.ResponseWriter, r *http.Request) {
		if p.engine.GetRegisterState().IsRegistered(p.losslessInfo.Instance) {
			p.log.Infof("[Lossless Event] losslessReadinessCheck is registered")
			w.WriteHeader(http.StatusOK)
			_, err := w.Write([]byte("REGISTERED"))
			if err != nil {
				p.log.Errorf("[Lossless Event] losslessReadinessCheck write error: %v", err)
			}
		} else {
			p.log.Infof("[Lossless Event] losslessReadinessCheck is not registered")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, err := w.Write([]byte("UNREGISTERED"))
			if err != nil {
				p.log.Errorf("[Lossless Event] losslessReadinessCheck write error: %v", err)
			}
		}
	}
	return HandlerFunc
}

func (p *LosslessController) genPreStopProbe() func(w http.
	ResponseWriter, r *http.Request) {
	HandlerFunc := func(w http.ResponseWriter, r *http.Request) {
		deregisterReq := registerToDeregister(p.losslessInfo.Instance)
		p.reportEvent(event.GetLosslessEvent(event.LosslessOfflineStart, p.losslessInfo))
		if err := p.engine.SyncDeregister(deregisterReq); err == nil {
			p.log.Infof("[Lossless Event] losslessOfflineProcess SyncDeregister success")
			w.WriteHeader(http.StatusOK)
			_, err = w.Write([]byte("DEREGISTERED SUCCESS"))
			if err != nil {
				p.log.Errorf("[Lossless Event] losslessReadinessCheck write error: %v", err)
			}
		} else {
			p.log.Errorf("[Lossless Event] losslessOfflineProcess SyncDeregister error: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			_, err := w.Write([]byte("DEREGISTERED FAILED"))
			if err != nil {
				p.log.Errorf("[Lossless Event] losslessReadinessCheck write error: %v", err)
			}
		}
	}
	return HandlerFunc
}

func registerToDeregister(instance *model.InstanceRegisterRequest) *model.InstanceDeRegisterRequest {
	return &model.InstanceDeRegisterRequest{
		Namespace:    instance.Namespace,
		Service:      instance.Service,
		Host:         instance.Host,
		Port:         instance.Port,
		InstanceID:   instance.InstanceId,
		ServiceToken: instance.ServiceToken,
		Timeout:      instance.Timeout,
		RetryCount:   instance.RetryCount,
	}
}
