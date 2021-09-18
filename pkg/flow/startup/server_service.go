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

package startup

import (
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/flow/data"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/pb"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/serverconnector"
	"time"
)

//创建系统服务拉取回调
func NewServerServiceCallBack(
	cfg config.Configuration, supplier plugin.Supplier, engine model.Engine) (*ServerServiceCallBack, error) {
	var err error
	var callback = &ServerServiceCallBack{}
	if callback.connector, err = data.GetServerConnector(cfg, supplier); nil != err {
		return nil, err
	}
	callback.engine = engine
	callback.cfg = cfg
	callback.interval = config.DefaultDiscoverServiceRetryInterval
	return callback, nil
}

//初始化系统服务的回调函数
type ServerServiceCallBack struct {
	engine    model.Engine
	connector serverconnector.ServerConnector
	cfg       config.Configuration
	interval  time.Duration
}

//执行系统服务初始化任务
func (s *ServerServiceCallBack) Process(
	taskKey interface{}, taskValue interface{}, lastProcessTime time.Time) model.TaskResult {
	if !lastProcessTime.IsZero() && time.Since(lastProcessTime) < s.interval {
		return model.SKIP
	}
	discoverService := taskValue.(*data.ServiceKeyComparable).SvcKey
	log.GetBaseLogger().Debugf("start to discover server service %s", discoverService)
	request := &model.GetInstancesRequest{}
	request.Namespace = discoverService.Namespace
	request.Service = discoverService.Service
	commonRequest := &data.CommonInstancesRequest{}
	commonRequest.InitByGetMultiRequest(request, s.cfg)
	err := s.engine.SyncGetResources(commonRequest)
	if nil != err {
		sdkErr := err.(model.SDKError)
		//只有超时的情况下，继续尝试加载discover服务
		if sdkErr.ErrorCode() == model.ErrCodeAPITimeoutError {
			log.GetBaseLogger().Warnf("timeout discover server service, %s, consumed time: %v",
				discoverService, commonRequest.CallResult.GetDelay())
			return model.CONTINUE
		}
		log.GetBaseLogger().Errorf("fail to discover server service %s, consumed time %v, err is %s",
			discoverService, commonRequest.CallResult.GetDelay(), sdkErr)
		return model.CONTINUE
	}
	//成功获取了discover服务之后，updateServers通知networkmanager该服务已经就绪
	if err = s.connector.UpdateServers(&model.ServiceEventKey{
		ServiceKey: discoverService,
		Type:       model.EventInstances,
	}); nil != err {
		log.GetBaseLogger().Errorf("fail to update server service %s instances, err is %s",
			discoverService, err)
	}
	if err = s.connector.UpdateServers(&model.ServiceEventKey{
		ServiceKey: discoverService,
		Type:       model.EventRouting,
	}); nil != err {
		log.GetBaseLogger().Errorf("fail to update server service %s routing, err is %s",
			discoverService, err)
	}
	if commonRequest.DstInstances != nil && commonRequest.DstInstances.(*pb.ServiceInstancesInProto).IsCacheLoaded() {
		log.GetBaseLogger().Warnf("success to discover server service instances of %s,"+
			" but it is loaded from cache, continue to discover next time", discoverService)
		return model.CONTINUE
	}
	if commonRequest.RouteInfo.DestRouteRule != nil &&
		commonRequest.RouteInfo.DestRouteRule.(*pb.ServiceRuleInProto).IsCacheLoaded() {
		log.GetBaseLogger().Warnf("success to discover server service route of %s,"+
			" but it is loaded from cache, continue to discover next time", discoverService)
		return model.CONTINUE
	}
	log.GetBaseLogger().Infof("success to discover server service %s", discoverService)
	return model.TERMINATE
}

//OnTaskEvent 任务事件回调
func (s *ServerServiceCallBack) OnTaskEvent(event model.TaskEvent) {

}
