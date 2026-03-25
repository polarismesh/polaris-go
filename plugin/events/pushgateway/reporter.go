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

package pushgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/event"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/sdk"
)

const (
	// PluginName is the name of the plugin.
	PluginName = "pushgateway"
)

// var _ event.EventReporter = (*PushgatewayReporter)(nil)

type PushgatewayReporter struct {
	*plugin.PluginBase
	*common.RunContext
	valueCtx sdk.ValueContext

	cfg      *Config
	clientIP string
	clientID string
	once     sync.Once
	cancel   context.CancelFunc
	events   []event.BaseEvent
	reqChan  chan event.BaseEvent
	doneChan chan struct{} // 用于等待协程完成

	httpClient *http.Client
	targetUrl  string
	// 上下文日志
	logCtx *log.ContextLogger
}

func init() {
	plugin.RegisterPlugin(&PushgatewayReporter{})
}

func (p *PushgatewayReporter) Type() common.Type {
	return common.TypeEventReporter
}

func (p *PushgatewayReporter) Name() string {
	return PluginName
}

func (p *PushgatewayReporter) Destroy() error {
	// 先取消 context 并等待事件 Flush 完成，然后再销毁 PluginBase 和 RunContext
	if p.cancel != nil {
		p.logCtx.GetEventLogger().Infof("[EventReporter][Pushgateway] canceling context and waiting for flush")
		p.cancel()
		// 等待协程完成 Flush
		if p.doneChan != nil {
			<-p.doneChan
			p.logCtx.GetEventLogger().Infof("[EventReporter][Pushgateway] flush completed")
		}
	}

	if p.PluginBase != nil {
		if err := p.PluginBase.Destroy(); err != nil {
			return err
		}
	}
	if p.RunContext != nil {
		if err := p.RunContext.Destroy(); err != nil {
			return err
		}
	}

	return nil
}

// Init 事件插件初始化
func (p *PushgatewayReporter) Init(ctx *plugin.InitContext) error {
	p.PluginBase = plugin.NewPluginBase(ctx)
	p.RunContext = common.NewRunContext()
	p.valueCtx = ctx.ValueCtx
	p.clientIP = ctx.Config.GetGlobal().GetAPI().GetBindIP()
	p.clientID = ctx.Config.GetGlobal().GetClient().GetId()
	p.logCtx = ctx.ValueCtx.GetContextLogger()

	cfgValue := ctx.Config.GetGlobal().GetEventReporter().GetPluginConfig(PluginName)
	if cfgValue != nil {
		p.cfg = cfgValue.(*Config)
	}

	return nil
}

// ReportEvent 数据记录在缓存中，定期1分钟上报
func (p *PushgatewayReporter) ReportEvent(e event.BaseEvent) error {
	p.prepare()
	p.logCtx.GetEventLogger().Infof("[EventReporter][Pushgateway] ReportEvent called, event type: %v, event name: %v", e.GetEventType(),
		e.GetEventName())

	select {
	case p.reqChan <- e:
	default:
		return fmt.Errorf("event queue is full")
	}

	return nil
}

func (p *PushgatewayReporter) prepare() {
	p.once.Do(func() {
		p.logCtx.GetEventLogger().Infof("[EventReporter][Pushgateway] prepare called, initializing...")
		// 只有触发了Chain.ReportEvent，才需要初始化chan，启动接受协程（一次任务）
		p.events = make([]event.BaseEvent, 0, p.cfg.EventQueueSize+1)
		p.reqChan = make(chan event.BaseEvent, p.cfg.EventQueueSize+1)
		p.doneChan = make(chan struct{})

		p.httpClient = &http.Client{Timeout: time.Second * 3}
		if p.cfg.Address != "" {
			p.targetUrl = fmt.Sprintf("http://%s/%s", p.cfg.Address, p.cfg.ReportPath)
		}
		ctx, cancel := context.WithCancel(context.Background())
		p.cancel = cancel
		p.logCtx.GetEventLogger().Infof("[EventReporter][Pushgateway] starting event consumer goroutine")
		go func(ctx context.Context) {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			defer close(p.doneChan) // 协程退出时关闭 doneChan，通知 Destroy 方法
			for {
				select {
				case e := <-p.reqChan:
					p.events = append(p.events, e)
					if len(p.events) >= p.cfg.EventQueueSize {
						p.Flush(false)
					}
				case <-ticker.C:
					p.Flush(false)
				case <-ctx.Done():
					p.logCtx.GetEventLogger().Infof("[EventReporter][Pushgateway] context done, draining " +
						"channel...")
					// 先把 channel 中剩余的事件消费完
					for {
						select {
						case e := <-p.reqChan:
							p.logCtx.GetEventLogger().Infof("[EventReporter][Pushgateway] drained event from " +
								"channel")
							p.events = append(p.events, e)
						default:
							goto flushAndExit
						}
					}
				flushAndExit:
					p.logCtx.GetEventLogger().Infof("[EventReporter][Pushgateway] flushing %d events before exit",
						len(p.events))
					p.Flush(true) // 退出之前同步flush数据
					p.logCtx.GetEventLogger().Infof("[EventReporter][Pushgateway] pushgateway reporter is " +
						"stopping")
					return
				}
			}

		}(ctx)
	})
}

func (p *PushgatewayReporter) getTargetUrl() (string, error) {
	// 有target，代表传入了IpTarget
	if p.targetUrl != "" {
		return p.targetUrl, nil
	}

	// 从服务端获取IP
	resp, err := p.valueCtx.GetEngine().SyncGetOneInstance(&model.GetOneInstanceRequest{
		FlowID:    uint64(time.Now().Unix()),
		Service:   p.cfg.ServiceName,
		Namespace: p.cfg.NamespaceName,
	})
	if err != nil {
		return "", fmt.Errorf("fail to get instance from service: %s", err.Error())
	}

	return fmt.Sprintf("http://%s:%d/%s", resp.GetInstances()[0].GetHost(), resp.GetInstances()[0].GetPort(), p.cfg.ReportPath), nil
}

// Flush 刷新数据到远端
func (p *PushgatewayReporter) Flush(isSync bool) {
	if len(p.events) == 0 {
		return
	}

	var batchEvents BatchEvents
	batchEvents.Batch = make([]event.BaseEvent, 0, len(p.events))
	for _, entry := range p.events {
		// 刷新之前，填充SDK的公共数据
		entry.SetClientIp(p.clientIP)
		entry.SetClientId(p.clientID)
		p.logCtx.GetEventLogger().Infof("[EventReporter][Pushgateway] new event: %v", model.JsonString(entry))
		batchEvents.Batch = append(batchEvents.Batch, entry)
	}
	// 重置p.events
	p.events = make([]event.BaseEvent, 0, p.cfg.EventQueueSize+1)

	flushHandler := func(batch BatchEvents) {
		data, err := json.Marshal(batch)
		if err != nil {
			p.logCtx.GetEventLogger().Errorf("[EventReporter][Pushgateway] marshal data(%+v) err: %+v", batchEvents, err)
			return
		}

		targetUrl, err := p.getTargetUrl()
		if err != nil {
			p.logCtx.GetEventLogger().Warnf("[EventReporter][Pushgateway] not found target event server addr, ignore it. %s", err.Error())
			return
		}

		// 重试配置：最多重试 3 次，每次间隔递增
		maxRetries := 3
		for attempt := 1; attempt <= maxRetries; attempt++ {
			dataBuffer := bytes.NewBuffer(data)
			req, err := http.NewRequest(http.MethodPost, targetUrl, dataBuffer)
			if err != nil {
				p.logCtx.GetEventLogger().Errorf("[EventReporter][Pushgateway] new request err: %+v", err)
				return
			}

			var respBuffer bytes.Buffer
			var respCode int
			resp, respErr := p.httpClient.Do(req)
			if resp != nil {
				respCode = resp.StatusCode
				if resp.Body != nil {
					_, _ = io.Copy(&respBuffer, resp.Body)
					resp.Body.Close()
				}
			}

			// 请求成功，直接返回
			if respErr == nil && respCode >= 200 && respCode < 300 {
				p.logCtx.GetEventLogger().Infof("[EventReporter][Pushgateway] request success, code: %d", respCode)
				return
			}

			// 请求失败，记录日志
			if respErr != nil {
				p.logCtx.GetEventLogger().Warnf("[EventReporter][Pushgateway] do request err (attempt %d/%d): %+v, code: %d, resp: %s",
					attempt, maxRetries, respErr, respCode, respBuffer.String())
			} else {
				p.logCtx.GetEventLogger().Warnf("[EventReporter][Pushgateway] request failed (attempt %d/%d), code: %d, resp: %s",
					attempt, maxRetries, respCode, respBuffer.String())
			}

			// 如果不是最后一次重试，等待一段时间后重试
			if attempt < maxRetries {
				retryDelay := time.Duration(attempt) * time.Second
				p.logCtx.GetEventLogger().Infof("[EventReporter][Pushgateway] retrying after %v...", retryDelay)
				time.Sleep(retryDelay)
			}
		}

		p.logCtx.GetEventLogger().Errorf("[EventReporter][Pushgateway] all %d retry attempts failed", maxRetries)
	}

	if isSync {
		flushHandler(batchEvents)
	} else {
		go flushHandler(batchEvents)
	}

	return
}

type BatchEvents struct {
	Batch []event.BaseEvent `json:"batch"`
}
