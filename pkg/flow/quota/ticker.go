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

package quota

import (
	"github.com/polarismesh/polaris-go/pkg/algorithm/rand"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/flow/data"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/localregistry"
	"github.com/polarismesh/polaris-go/pkg/plugin/serverconnector"
	"time"
)

//远程配额查询任务
type RemoteQuotaCallBack struct {
	registry             localregistry.InstancesRegistry
	asyncRLimitConnector serverconnector.AsyncRateLimitConnector
	engine               model.Engine
	scalableRand         *rand.ScalableRand
}

//创建查询任务
func NewRemoteQuotaCallback(cfg config.Configuration, supplier plugin.Supplier,
	engine model.Engine) (*RemoteQuotaCallBack, error) {
	registry, err := data.GetRegistry(cfg, supplier)
	if nil != err {
		return nil, err
	}
	connector, err := data.GetServerConnector(cfg, supplier)
	if nil != err {
		return nil, err
	}
	return &RemoteQuotaCallBack{
		scalableRand:         rand.NewScalableRand(),
		registry:             registry,
		asyncRLimitConnector: connector.GetAsyncRateLimitConnector(),
		engine:               engine}, nil
}

const (
	intervalMinMilli   = 30
	intervalRangeMilli = 20
)

//处理远程配额查询任务
func (r *RemoteQuotaCallBack) Process(
	taskKey interface{}, taskValue interface{}, lastProcessTime time.Time) model.TaskResult {
	rateLimitWindow := taskValue.(*RateLimitWindow)
	reportInterval := int64(r.scalableRand.Intn(intervalRangeMilli) + intervalMinMilli)
	nowMilli := model.CurrentMillisecond()
	lastProcessMilli := lastProcessTime.UnixNano() / 1e6
	if lastProcessMilli > 0 && nowMilli-lastProcessMilli < reportInterval {
		return model.SKIP
	}
	//尝试触发一次清理
	rateLimitWindow.WindowSet.PurgeWindows(nowMilli)
	//规则变更触发的删除
	if rateLimitWindow.GetStatus() == Deleted {
		log.GetBaseLogger().Infof("[RateLimit]window %s deleted, start terminate task", taskKey.(string))
		return model.TERMINATE
	}
	//状态机
	switch rateLimitWindow.GetStatus() {
	case Created:
		break
	case Deleted:
		break
	case Initializing:
		rateLimitWindow.DoAsyncRemoteInit()
	default:
		if err := rateLimitWindow.DoAsyncRemoteAcquire(); nil != err {
			rateLimitWindow.SetStatus(Initializing)
		}
	}
	return model.CONTINUE
}

//OnTaskEvent 任务事件回调
func (r *RemoteQuotaCallBack) OnTaskEvent(event model.TaskEvent) {

}
