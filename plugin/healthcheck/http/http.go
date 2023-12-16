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

package http

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/polarismesh/specification/source/go/api/v1/fault_tolerance"

	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/healthcheck"
)

// Detector TCP协议的实例健康探测器
type Detector struct {
	*plugin.PluginBase
	cfg     *Config
	timeout time.Duration
	client  *http.Client
}

// Type 插件类型
func (g *Detector) Type() common.Type {
	return common.TypeHealthCheck
}

// Name 插件名，一个类型下插件名唯一
func (g *Detector) Name() string {
	return "http"
}

// Init 初始化插件
func (g *Detector) Init(ctx *plugin.InitContext) (err error) {
	g.PluginBase = plugin.NewPluginBase(ctx)
	cfgValue := ctx.Config.GetConsumer().GetHealthCheck().GetPluginConfig(g.Name())
	if cfgValue != nil {
		g.cfg = cfgValue.(*Config)
	}
	g.client = &http.Client{}
	g.timeout = ctx.Config.GetConsumer().GetHealthCheck().GetTimeout()
	return nil
}

// Destroy 销毁插件，可用于释放资源
func (g *Detector) Destroy() error {
	return nil
}

// DetectInstance 探测服务实例健康
func (g *Detector) DetectInstance(ins model.Instance, rule *fault_tolerance.FaultDetectRule) (result healthcheck.DetectResult, err error) {
	start := time.Now()
	timeout := g.timeout
	if rule != nil && rule.Protocol == fault_tolerance.FaultDetectRule_HTTP {
		timeout = time.Duration(rule.GetTimeout()) * time.Millisecond
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	// 得到Http address
	detReq, err := g.generateHttpRequest(ctx, ins, rule)
	if err != nil {
		return nil, err
	}
	code, success := g.doHttpDetect(detReq, rule)
	result = &healthcheck.DetectResultImp{
		Success:        success,
		DetectTime:     start,
		DetectInstance: ins,
		Code:           code,
	}
	return result, nil
}

// IsEnable .
func (g *Detector) IsEnable(cfg config.Configuration) bool {
	return cfg.GetGlobal().GetSystem().GetMode() != model.ModeWithAgent
}

// doHttpDetect 执行一次健康探测逻辑
func (g *Detector) doHttpDetect(detReq *http.Request, rule *fault_tolerance.FaultDetectRule) (string, bool) {
	resp, err := g.client.Do(detReq)
	if err != nil {
		log.GetDetectLogger().Errorf("[HealthCheck][http] fail to check %+v, err is %v", detReq.URL, err)
		return "", false
	}
	defer resp.Body.Close()
	if code := resp.StatusCode; code >= 200 && code < 500 {
		return strconv.Itoa(resp.StatusCode), true
	}
	return strconv.Itoa(resp.StatusCode), false
}

// Protocol .
func (g *Detector) Protocol() fault_tolerance.FaultDetectRule_Protocol {
	return fault_tolerance.FaultDetectRule_HTTP
}

func (g *Detector) generateHttpRequest(ctx context.Context, ins model.Instance, rule *fault_tolerance.FaultDetectRule) (*http.Request, error) {
	var (
		address   string
		customUrl = g.cfg.Path
		port      = ins.GetPort()
	)
	header := http.Header{}
	if rule == nil {
		customUrl = strings.TrimPrefix(customUrl, "/")
		if len(g.cfg.Host) > 0 {
			header.Add("Host", g.cfg.Host)
		}
		if len(g.cfg.RequestHeadersToAdd) > 0 {
			for _, requestHeader := range g.cfg.RequestHeadersToAdd {
				header.Add(requestHeader.Key, requestHeader.Value)
			}
		}
	} else {
		if rule.GetPort() > 0 {
			port = rule.Port
		}
		customUrl = rule.GetHttpConfig().GetUrl()
		customUrl = strings.TrimPrefix(customUrl, "/")
		ruleHeaders := rule.GetHttpConfig().GetHeaders()
		for i := range ruleHeaders {
			header.Add(ruleHeaders[i].Key, ruleHeaders[i].Value)
		}
	}
	address = fmt.Sprintf("http://%s:%d", ins.GetHost(), port, customUrl)

	request, err := http.NewRequestWithContext(ctx, rule.GetHttpConfig().Method, address, bytes.NewBufferString(rule.HttpConfig.GetBody()))
	if err != nil {
		log.GetDetectLogger().Errorf("[HealthCheck][http] fail to build request %+v, err is %v", address, err)
		return nil, err
	}

	if len(header) > 0 {
		request.Header = header
	}
	return request, nil
}

// init 注册插件信息
func init() {
	plugin.RegisterConfigurablePlugin(&Detector{}, &Config{})
}
