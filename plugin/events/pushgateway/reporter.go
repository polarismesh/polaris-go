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
	bytes2 "bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"io"
	"net/http"
	"sync"
	"time"
)

const (
	// PluginName is the name of the plugin.
	PluginName = "pushgateway"
)

// var _ event.EventReporter = (*PushgatewayReporter)(nil)

type PushgatewayReporter struct {
	*plugin.PluginBase
	*common.RunContext
	valueCtx model.ValueContext

	cfg      *Config
	clientIP string
	clientID string
	once     sync.Once
	cancel   context.CancelFunc
	events   []model.BaseEvent
	reqChan  chan model.BaseEvent

	httpClient *http.Client
	targetUrl  string
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
	if p.cancel != nil {
		p.cancel()
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

	cfgValue := ctx.Config.GetGlobal().GetEventReporter().GetPluginConfig(PluginName)
	if cfgValue != nil {
		p.cfg = cfgValue.(*Config)
	}

	return nil
}

// ReportEvent 数据记录在缓存中，定期1分钟上报
func (p *PushgatewayReporter) ReportEvent(e model.BaseEvent) error {
	p.prepare()

	select {
	case p.reqChan <- e:
	default:
		return fmt.Errorf("event queue is full")
	}

	return nil
}

func (p *PushgatewayReporter) prepare() {
	p.once.Do(func() {
		// 只有触发了Chain.ReportEvent，才需要初始化chan，启动接受协程（一次任务）
		p.events = make([]model.BaseEvent, 0, p.cfg.EventQueueSize+1)
		p.reqChan = make(chan model.BaseEvent, p.cfg.EventQueueSize+1)

		p.httpClient = &http.Client{Timeout: time.Second * 3}
		if p.cfg.Address != "" {
			p.targetUrl = fmt.Sprintf("http://%s/%s", p.cfg.Address, p.cfg.ReportPath)
		}

		ctx, cancel := context.WithCancel(context.Background())
		p.cancel = cancel
		go func(ctx context.Context) {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
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
					log.GetBaseLogger().Infof("[EventReporter][Pushgateway] receive destroy signal, flush events")
					p.Flush(true) // 退出之前同步flush数据
					log.GetBaseLogger().Infof("[EventReporter][Pushgateway] pushgateway reporter is stopping")
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
	batchEvents.Batch = make([]model.ConfigEvent, 0, len(p.events))
	for _, entry := range p.events {
		// 刷新之前，填充SDK的公共数据
		entry.GetConfigEvent().SetClientIp(p.clientIP)
		entry.GetConfigEvent().SetClientId(p.clientID)
		log.GetBaseLogger().Infof("[EventReporter][Pushgateway] new config event: %+v", entry.GetConfigEvent())

		batchEvents.Batch = append(batchEvents.Batch, entry.GetConfigEvent())

	}
	// 重置p.events
	p.events = make([]model.BaseEvent, 0, p.cfg.EventQueueSize+1)

	flushHandler := func(batch BatchEvents) {
		data, err := json.Marshal(batch)
		if err != nil {
			log.GetBaseLogger().Errorf("[EventReporter][Pushgateway] marshal data(%+v) err: %+v", batchEvents, err)
			return
		}

		dataBuffer := bytes2.NewBuffer(data)
		targetUrl, err := p.getTargetUrl()
		if err != nil {
			log.GetBaseLogger().Warnf("[EventReporter][Pushgateway] not found target event server addr, ignore it. %s", err.Error())
			return
		}
		req, err := http.NewRequest(http.MethodPost, targetUrl, dataBuffer)
		if err != nil {
			log.GetBaseLogger().Errorf("[EventReporter][Pushgateway] new request err: %+v", err)
			return
		}

		var respBuffer bytes2.Buffer
		var respCode int
		resp, respErr := p.httpClient.Do(req)
		if resp != nil {
			respCode = resp.StatusCode
			if resp.Body != nil {
				_, _ = io.Copy(&respBuffer, resp.Body)
				defer resp.Body.Close()
			}
		}
		if respErr != nil {
			log.GetBaseLogger().Errorf("[EventReporter][Pushgateway] do request err: %+v, code: %d, resp: %s", respErr, respCode, respBuffer.String())
			return
		}
	}

	if isSync {
		flushHandler(batchEvents)
	} else {
		go flushHandler(batchEvents)
	}

	return
}

type BatchEvents struct {
	Batch []model.ConfigEvent
}
