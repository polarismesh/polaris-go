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

package flow

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/polarismesh/polaris-go/pkg/flow/data"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/localregistry"
)

type WatchContext interface {
	ServiceEventKey() model.ServiceEventKey
	OnInstances(value model.ServiceInstances)
	OnServices(value model.Services)
	Cancel()
}

type WatchEngine struct {
	rwMutex       sync.RWMutex
	watchContexts map[uint64]WatchContext
	indexSeed     uint64
	registry      localregistry.LocalRegistry
}

func NewWatchEngine(registry localregistry.LocalRegistry) *WatchEngine {
	return &WatchEngine{
		watchContexts: make(map[uint64]WatchContext),
		registry:      registry,
	}
}

// ServiceEventCallback serviceUpdate消息订阅回调
func (w *WatchEngine) ServiceEventCallback(event *common.PluginEvent) error {
	var (
		eventObject *common.ServiceEventObject
		ok          bool
	)
	if eventObject, ok = event.EventObject.(*common.ServiceEventObject); !ok {
		return nil
	}
	var (
		isInstance   bool
		svcInstances model.ServiceInstances

		isServices bool
		services   model.Services
	)
	switch event.EventType {
	case common.OnServiceAdded:
		svcInstances, isInstance = eventObject.NewValue.(model.ServiceInstances)
		services, isServices = eventObject.NewValue.(model.Services)
	case common.OnServiceUpdated:
		svcInstances, isInstance = eventObject.NewValue.(model.ServiceInstances)
		services, isServices = eventObject.NewValue.(model.Services)
	case common.OnServiceDeleted:
		svcInstances, isInstance = eventObject.NewValue.(model.ServiceInstances)
		services, isServices = eventObject.NewValue.(model.Services)
	default:
		// do nothing
	}
	if isInstance && svcInstances != nil {
		func() {
			w.rwMutex.RLock()
			defer w.rwMutex.RUnlock()
			for _, lpCtx := range w.watchContexts {
				if lpCtx.ServiceEventKey().Type == model.EventInstances {
					lpCtx.OnInstances(svcInstances)
				}
			}
		}()
	}
	if isServices && services != nil {
		func() {
			w.rwMutex.RLock()
			defer w.rwMutex.RUnlock()
			for _, lpCtx := range w.watchContexts {
				if lpCtx.ServiceEventKey().Type == model.EventServices {
					lpCtx.OnServices(services)
				}
			}
		}()
	}
	return nil
}

func (w *WatchEngine) CancelWatch(watchId uint64) {
	w.rwMutex.Lock()
	defer w.rwMutex.Unlock()
	ctx, ok := w.watchContexts[watchId]
	if ok {
		delete(w.watchContexts, watchId)
		ctx.Cancel()
		w.registry.UnwatchService(ctx.ServiceEventKey())
	}
}

func (w *WatchEngine) WatchAllServices(
	request *model.WatchAllServicesRequest) (*model.WatchAllServicesResponse, error) {
	if request.WatchMode == model.WatchModeNotify {
		return w.notifyAllServices(request)
	}
	return w.longPullAllServices(request)
}

func (w *WatchEngine) notifyAllServices(
	request *model.WatchAllServicesRequest) (*model.WatchAllServicesResponse, error) {
	nextId := atomic.AddUint64(&w.indexSeed, 1)
	serviceKey := model.ServiceKey{
		Namespace: request.Namespace,
	}
	serivcesResp := w.registry.GetServicesByMeta(&serviceKey, false)
	notifyCtx := &NotifyUpdateContext{
		id: nextId,
		svcEventKey: model.ServiceEventKey{
			ServiceKey: serviceKey,
			Type:       model.EventServices,
		},
		servicesListener: request.ServicesListener,
	}
	w.rwMutex.Lock()
	w.watchContexts[nextId] = notifyCtx
	w.rwMutex.Unlock()
	if !serivcesResp.IsInitialized() {
		notifier, err := w.registry.LoadServices(&serviceKey)
		if err != nil {
			return nil, err
		}
		<-notifier.GetContext().Done()
		if err := notifier.GetError(); err != nil {
			return nil, err
		}
	}
	serivcesResp = w.registry.GetServicesByMeta(&serviceKey, false)
	services := serivcesResp
	return model.NewWatchAllServicesResponse(nextId, &model.ServicesResponse{
		Type:      model.EventServices,
		Value:     services.GetValue(),
		Revision:  services.GetRevision(),
		HashValue: services.GetHashValue(),
	}, w.CancelWatch), nil
}

func (w *WatchEngine) longPullAllServices(
	request *model.WatchAllServicesRequest) (*model.WatchAllServicesResponse, error) {
	nextId := atomic.AddUint64(&w.indexSeed, 1)
	serviceKey := model.ServiceKey{
		Namespace: request.Namespace,
	}
	serivcesResp := w.registry.GetServicesByMeta(&serviceKey, false)
	pullContext := NewLongPullContext(nextId, request.WaitIndex, request.WaitTime, model.ServiceEventKey{
		ServiceKey: serviceKey,
		Type:       model.EventServices,
	})
	w.rwMutex.Lock()
	w.watchContexts[nextId] = pullContext
	w.rwMutex.Unlock()
	defer func() {
		w.rwMutex.Lock()
		delete(w.watchContexts, nextId)
		w.rwMutex.Unlock()
	}()
	if !serivcesResp.IsInitialized() {
		_, err := w.registry.LoadServices(&serviceKey)
		if err != nil {
			return nil, err
		}
	}
	pullContext.Start()
	var latestServices model.Services
	if nil != pullContext.registryValue {
		latestServices = pullContext.registryValue.(model.Services)
	} else {
		latestServices = w.registry.GetServicesByMeta(&serviceKey, false)
	}
	return model.NewWatchAllServicesResponse(nextId, &model.ServicesResponse{
		Type:      model.EventServices,
		Value:     latestServices.GetValue(),
		Revision:  latestServices.GetRevision(),
		HashValue: latestServices.GetHashValue(),
	}, nil), nil
}

func (w *WatchEngine) WatchAllInstances(
	request *model.WatchAllInstancesRequest) (*model.WatchAllInstancesResponse, error) {
	if request.WatchMode == model.WatchModeNotify {
		return w.notifyAllInstances(request)
	}
	return w.longPullAllInstances(request)
}

func (w *WatchEngine) notifyAllInstances(
	request *model.WatchAllInstancesRequest) (*model.WatchAllInstancesResponse, error) {
	nextId := atomic.AddUint64(&w.indexSeed, 1)
	svcInstances := w.registry.GetInstances(&request.ServiceKey, false, false)
	w.registry.WatchService(model.ServiceEventKey{
		ServiceKey: request.ServiceKey,
		Type:       model.EventInstances,
	})
	notifyCtx := &NotifyUpdateContext{
		id: nextId,
		svcEventKey: model.ServiceEventKey{
			ServiceKey: request.ServiceKey,
			Type:       model.EventInstances,
		},
		instancesListener: request.InstancesListener,
	}
	w.rwMutex.Lock()
	w.watchContexts[nextId] = notifyCtx
	w.rwMutex.Unlock()
	if !svcInstances.IsInitialized() {
		_, err := w.registry.LoadInstances(&request.ServiceKey)
		if err != nil {
			return nil, err
		}
	}
	svcInstances = w.registry.GetInstances(&request.ServiceKey, false, false)
	instancesResponse := data.BuildInstancesResponse(request.ServiceKey, nil, svcInstances)
	return model.NewWatchAllInstancesResponse(nextId, instancesResponse, w.CancelWatch), nil
}

func (w *WatchEngine) longPullAllInstances(
	request *model.WatchAllInstancesRequest) (*model.WatchAllInstancesResponse, error) {
	nextId := atomic.AddUint64(&w.indexSeed, 1)
	svcInstances := w.registry.GetInstances(&request.ServiceKey, false, false)
	w.registry.WatchService(model.ServiceEventKey{
		ServiceKey: request.ServiceKey,
		Type:       model.EventInstances,
	})
	pullContext := NewLongPullContext(nextId, request.WaitIndex, request.WaitTime, model.ServiceEventKey{
		ServiceKey: request.ServiceKey,
		Type:       model.EventInstances,
	})
	w.rwMutex.Lock()
	w.watchContexts[nextId] = pullContext
	w.rwMutex.Unlock()
	defer func() {
		w.rwMutex.Lock()
		delete(w.watchContexts, nextId)
		w.rwMutex.Unlock()
	}()
	if !svcInstances.IsInitialized() {
		_, err := w.registry.LoadInstances(&request.ServiceKey)
		if err != nil {
			return nil, err
		}
	}
	pullContext.Start()
	var latestSvcInstances model.ServiceInstances
	if nil != pullContext.registryValue {
		latestSvcInstances = pullContext.registryValue.(model.ServiceInstances)
	} else {
		latestSvcInstances = w.registry.GetInstances(&request.ServiceKey, false, false)
	}
	instancesResponse := data.BuildInstancesResponse(request.ServiceKey, nil, latestSvcInstances)
	return model.NewWatchAllInstancesResponse(nextId, instancesResponse, nil), nil
}

type NotifyUpdateContext struct {
	id                uint64
	svcEventKey       model.ServiceEventKey
	instancesListener model.InstancesListener
	servicesListener  model.ServicesListener
}

func (l *NotifyUpdateContext) ServiceEventKey() model.ServiceEventKey {
	return l.svcEventKey
}

func (l *NotifyUpdateContext) OnInstances(value model.ServiceInstances) {
	go func() {
		instancesResponse := data.BuildInstancesResponse(l.svcEventKey.ServiceKey, nil, value)
		l.instancesListener.OnInstancesUpdate(instancesResponse)
	}()
}

func (l *NotifyUpdateContext) OnServices(value model.Services) {
	go func() {
		l.servicesListener.OnServicesUpdate(&model.ServicesResponse{
			Type:      model.EventServices,
			Value:     value.GetValue(),
			Revision:  value.GetRevision(),
			HashValue: value.GetHashValue(),
		})
	}()
}

func (l *NotifyUpdateContext) Cancel() {

}

type LongPullContext struct {
	id            uint64
	mutex         sync.Mutex
	svcEventKey   model.ServiceEventKey
	registryValue model.RegistryValue
	waitCtx       context.Context
	waitCancel    context.CancelFunc
	waitIndex     uint64
	valueChan     chan model.RegistryValue
}

func NewLongPullContext(
	id uint64, waitIndex uint64, waitTime time.Duration, svcEventKey model.ServiceEventKey) *LongPullContext {
	pullCtx := &LongPullContext{
		id:          id,
		waitIndex:   waitIndex,
		svcEventKey: svcEventKey,
		valueChan:   make(chan model.RegistryValue, 1),
	}
	pullCtx.waitCtx, pullCtx.waitCancel = context.WithTimeout(context.Background(), waitTime)
	return pullCtx
}

func (l *LongPullContext) ServiceEventKey() model.ServiceEventKey {
	return l.svcEventKey
}

func (l *LongPullContext) OnInstances(value model.ServiceInstances) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	l.registryValue = value
	if l.registryValue.IsInitialized() && l.registryValue.GetHashValue() != l.waitIndex {
		l.waitCancel()
	}
}

func (l *LongPullContext) OnServices(value model.Services) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	l.registryValue = value
	if l.registryValue.IsInitialized() && l.registryValue.GetHashValue() != l.waitIndex {
		l.waitCancel()
	}
}

func (l *LongPullContext) Start() {
	for {
		select {
		case <-l.waitCtx.Done():
			log.GetBaseLogger().Infof("wait context %d exit", l.id)
			return
		}
	}
}

func (l *LongPullContext) Cancel() {
	l.waitCancel()
}
