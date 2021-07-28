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

package localchannel

import (
	"errors"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/plugin/subscribe/utils"
	"sync"
	"time"
)

var (
	bufferSize uint32 = 30
)

type SubscribeLocalChannel struct {
	*plugin.PluginBase
	*common.RunContext

	registerServices []model.ServiceKey

	eventChannelMap sync.Map //map[model.ServiceKey]chan model.SubScribeEvent
	lock            *sync.Mutex
}

func (s *SubscribeLocalChannel) Init(ctx *plugin.InitContext) error {
	s.PluginBase = plugin.NewPluginBase(ctx)
	conf := ctx.Config.GetConsumer().GetSubScribe().GetPluginConfig(s.Name())
	if conf != nil {
		pConf := conf.(*Config)
		bufferSize = pConf.ChannelBufferSize
	} else {
		bufferSize = DefaultChannelBufferSize
	}
	s.lock = &sync.Mutex{}
	return nil
}

func (s *SubscribeLocalChannel) Type() common.Type {
	return common.TypeSubScribe
}

func (s *SubscribeLocalChannel) Name() string {
	return config.SubscribeLocalChannel
}

func (s *SubscribeLocalChannel) Start() error {
	return nil
}

func (s *SubscribeLocalChannel) Destroy() error {
	return nil
}

func (s *SubscribeLocalChannel) IsEnable(cfg config.Configuration) bool {
	return true
}

func pushToBufferChannel(event model.SubScribeEvent, ch chan model.SubScribeEvent) error {
	select {
	case ch <- event:
		return nil
	default:
		return errors.New("buffer full")
	}
}

func (s *SubscribeLocalChannel) DoSubScribe(event *common.PluginEvent) error {
	if event.EventType != common.OnServiceUpdated {
		return nil
	}
	serviceEvent := event.EventObject.(*common.ServiceEventObject)
	if serviceEvent.SvcEventKey.Type != model.EventInstances {
		return nil
	}
	insEvent := &model.InstanceEvent{}
	insEvent.AddEvent = utils.CheckAddInstances(serviceEvent)
	insEvent.UpdateEvent = utils.CheckUpdateInstances(serviceEvent)
	insEvent.DeleteEvent = utils.CheckDeleteInstances(serviceEvent)
	value, ok := s.eventChannelMap.Load(serviceEvent.SvcEventKey.ServiceKey)
	if !ok {
		log.GetBaseLogger().Debugf("%s %s not watch", serviceEvent.SvcEventKey.ServiceKey.Namespace,
			serviceEvent.SvcEventKey.ServiceKey.Service)
		return nil
	}
	channel := value.(chan model.SubScribeEvent)
	var err error
	for i := 0; i < 2; i++ {
		err = pushToBufferChannel(insEvent, channel)
		if err == nil {
			break
		} else {
			time.Sleep(time.Millisecond * 10)
		}
	}
	if err != nil {
		log.GetBaseLogger().Errorf("DoSubScribe %s %s pushToBufferChannel err:%s",
			serviceEvent.SvcEventKey.ServiceKey.Namespace, serviceEvent.SvcEventKey.ServiceKey.Service, err.Error())
	}
	return err
}

func (s *SubscribeLocalChannel) WatchService(key model.ServiceKey) (interface{}, error) {
	value, ok := s.eventChannelMap.Load(key)
	if !ok {
		s.lock.Lock()
		defer s.lock.Unlock()
		v, secondCheck := s.eventChannelMap.Load(key)
		if secondCheck {
			return v, nil
		} else {
			ch := make(chan model.SubScribeEvent, bufferSize)
			s.eventChannelMap.Store(key, ch)
			return ch, nil
		}
	}
	return value, nil
}

//注册插件和配置
func init() {
	plugin.RegisterConfigurablePlugin(&SubscribeLocalChannel{}, &Config{})
}
