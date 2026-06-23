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
	"sync"
	"time"

	"github.com/polarismesh/specification/source/go/api/v1/fault_tolerance"

	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/healthcheck"
)

type HttpSender interface {
	Do(req *http.Request) (*http.Response, error)
}

// Detector TCP协议的实例健康探测器
type Detector struct {
	*plugin.PluginBase
	cfg     *Config
	timeout time.Duration
	client  HttpSender
	// 上下文日志
	logCtx *log.ContextLogger
	// lastDoErr 记录每个探测地址的上一次 HTTP 请求错误信息，err 内容不变时静默不重复打印。
	lastDoErr map[string]string
	// lastDoErrMu 保护 lastDoErr 的并发访问：多个 ResourceHealthChecker 共享同一 detector 实例，
	// 它们的 IntervalExecute 可能并发调用 doHttpDetect 读写该 map。
	lastDoErrMu sync.Mutex
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
	g.lastDoErr = make(map[string]string, 16)
	g.timeout = ctx.Config.GetConsumer().GetHealthCheck().GetTimeout()
	g.logCtx = ctx.ValueCtx.GetContextLogger()
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
		errMsg := err.Error()
		urlStr := detReq.URL.String()
		// 探测异常收敛：err 内容不变时仅首次打印，避免连接超时/拒绝等重复刷屏。
		g.lastDoErrMu.Lock()
		if g.lastDoErr == nil {
			g.lastDoErr = make(map[string]string, 8)
		}
		if lastErr, ok := g.lastDoErr[urlStr]; !ok || lastErr != errMsg {
			g.lastDoErr[urlStr] = errMsg
			g.lastDoErrMu.Unlock()
			g.logCtx.GetDetectLogger().Errorf("[HealthCheck][http] fail to check %+v, err is %v", detReq.URL, err)
		} else {
			g.lastDoErrMu.Unlock()
		}
		return "", false
	}
	// err 恢复后清除记录，确保下次异常能重新打印。
	g.lastDoErrMu.Lock()
	if g.lastDoErr != nil {
		delete(g.lastDoErr, detReq.URL.String())
	}
	g.lastDoErrMu.Unlock()
	defer resp.Body.Close()
	code := resp.StatusCode
	success := code >= 200 && code < 500
	// 探测成功不属于失败事件，但每个探测周期都会产生，使用 Debug 级别记录，便于确认探测真实发起。
	g.logCtx.GetDetectLogger().Debugf("[HealthCheck][http] detect done, url=%+v, code=%d, success=%t",
		detReq.URL, code, success)
	return strconv.Itoa(code), success
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
	address = fmt.Sprintf("http://%s:%d/%s", ins.GetHost(), port, customUrl)

	request, err := http.NewRequestWithContext(ctx, rule.GetHttpConfig().Method, address, bytes.NewBufferString(rule.HttpConfig.GetBody()))
	if err != nil {
		g.logCtx.GetDetectLogger().Errorf("[HealthCheck][http] fail to build request %+v, err is %v", address, err)
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
