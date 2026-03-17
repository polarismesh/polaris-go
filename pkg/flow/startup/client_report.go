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
	"time"

	"github.com/golang/protobuf/proto"
	apiservice "github.com/polarismesh/specification/source/go/api/v1/service_manage"

	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/flow/data"
	"github.com/polarismesh/polaris-go/pkg/log/ctx"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/localregistry"
	statreporter "github.com/polarismesh/polaris-go/pkg/plugin/metrics"
	"github.com/polarismesh/polaris-go/pkg/plugin/serverconnector"
	"github.com/polarismesh/polaris-go/pkg/version"
)

// NewReportClientCallBack  创建上报回调
func NewReportClientCallBack(
	cfg config.Configuration, supplier plugin.Supplier, globalCtx model.ValueContext) (*ReportClientCallBack, error) {
	var err error
	var callback = &ReportClientCallBack{}
	if callback.connector, err = data.GetServerConnector(cfg, supplier); err != nil {
		return nil, err
	}
	if callback.registry, err = data.GetRegistry(cfg, supplier); err != nil {
		return nil, err
	}
	if callback.reporterChain, err = data.GetStatReporterChain(cfg, supplier); err != nil {
		return nil, err
	}
	callback.configuration = cfg
	callback.globalCtx = globalCtx
	callback.interval = cfg.GetGlobal().GetAPI().GetReportInterval()
	callback.logCtx = globalCtx.GetContextLogger()
	callback.loadLocalClientReportResult()
	return callback, nil
}

// ReportClientCallBack 上报客户端状态任务回调
type ReportClientCallBack struct {
	connector     serverconnector.ServerConnector
	registry      localregistry.InstancesRegistry
	configuration config.Configuration
	globalCtx     model.ValueContext
	interval      time.Duration
	reporterChain []statreporter.StatReporter
	logCtx        *ctx.ContextLogger
	// lastLocation 记录上次成功持久化的地域信息，用于对比判断是否需要重新写入 client_info.json
	lastLocation *model.Location
}

const (
	// 地域信息持久化数据
	clientInfoPersistFile = "client_info.json"
)

// loadLocalClientReportResult 从本地缓存加载上报结果信息
func (r *ReportClientCallBack) loadLocalClientReportResult() {
	logBase := r.logCtx.GetBaseLogger()
	resp := &apiservice.Response{}
	cachedFile := clientInfoPersistFile
	err := r.registry.LoadPersistedMessage(cachedFile, resp)
	if err != nil {
		logBase.Warnf("fail to load local region info from %s, err is %v", cachedFile, err)
		return
	}
	location := resp.GetClient().GetLocation()
	loc := &model.Location{
		Region: location.GetRegion().GetValue(),
		Zone:   location.GetZone().GetValue(),
		Campus: location.GetCampus().GetValue(),
	}
	// 初始化 lastLocation，避免首次上报时与缓存相同的 location 也触发重复写入
	r.lastLocation = loc
	r.updateLocation(loc, nil)
}

// reportClientRequest 客户端上报的请求
func (r *ReportClientCallBack) reportClientRequest() *model.ReportClientRequest {
	apiConfig := r.configuration.GetGlobal().GetAPI()
	clientHost := apiConfig.GetBindIP()
	reportClientReq := &model.ReportClientRequest{
		Version:        version.Version,
		Timeout:        r.configuration.GetGlobal().GetAPI().GetTimeout(),
		PersistHandler: r.persistHandlerWithLocationCheck,
	}
	if len(clientHost) > 0 {
		reportClientReq.Host = clientHost
	}

	infos := make([]model.StatInfo, 0, len(r.reporterChain))

	// 收集当前的所有metric插件链的元信息
	for i := range r.reporterChain {
		stat := r.reporterChain[i].Info()
		if stat.Empty() {
			continue
		}
		infos = append(infos, stat)
	}

	reportClientReq.StatInfos = infos
	reportClientReq.ID = r.globalCtx.GetClientId()
	return reportClientReq
}

// persistHandlerWithLocationCheck 带地域信息变更检查的持久化处理函数
// 只有当服务端返回的地域信息与上次持久化的不同时，才执行写入操作，避免不必要的磁盘 I/O
func (r *ReportClientCallBack) persistHandlerWithLocationCheck(message proto.Message) error {
	resp, ok := message.(*apiservice.Response)
	if !ok {
		// 类型不匹配时直接持久化
		return r.registry.PersistMessage(clientInfoPersistFile, message)
	}
	loc := resp.GetClient().GetLocation()
	newLocation := &model.Location{
		Region: loc.GetRegion().GetValue(),
		Zone:   loc.GetZone().GetValue(),
		Campus: loc.GetCampus().GetValue(),
	}
	// 对比新旧地域信息，相同则跳过写入
	if r.lastLocation != nil && *r.lastLocation == *newLocation {
		return nil
	}
	// 地域信息发生变化或首次写入，执行持久化
	if err := r.registry.PersistMessage(clientInfoPersistFile, message); err != nil {
		return err
	}
	r.lastLocation = newLocation
	r.logCtx.GetBaseLogger().Infof("client_info.json updated, location changed to {Region:%s, Zone:%s, Campus:%s}",
		newLocation.Region, newLocation.Zone, newLocation.Campus)
	return nil
}

// Process 执行任务
func (r *ReportClientCallBack) Process(
	taskKey interface{}, taskValue interface{}, lastProcessTime time.Time) model.TaskResult {
	if !lastProcessTime.IsZero() && time.Since(lastProcessTime) < r.interval {
		return model.SKIP
	}
	reportClientReq := r.reportClientRequest()
	if err := reportClientReq.Validate(); err != nil {
		r.logCtx.GetBaseLogger().Errorf("report client request fatal validate error:%v", err)
		return model.TERMINATE
	}

	reportClientResp, err := r.connector.ReportClient(reportClientReq)
	if err != nil {
		r.logCtx.GetBaseLogger().Errorf("report client info:%+v, error:%v", reportClientReq, err)
		r.updateLocation(nil, err.(model.SDKError))
		// 发生错误也要重试，直到获取到地域信息为止
		return model.CONTINUE
	}

	r.updateLocation(&model.Location{
		Region: reportClientResp.Region,
		Zone:   reportClientResp.Zone,
		Campus: reportClientResp.Campus,
	}, nil)
	return model.CONTINUE
}

// OnTaskEvent 任务事件回调
func (r *ReportClientCallBack) OnTaskEvent(event model.TaskEvent) {

}

// updateLocation 更新区域属性
func (r *ReportClientCallBack) updateLocation(location *model.Location, lastErr model.SDKError) {
	// 如果SDK设置了本地获取 location，则忽略 ReportClient 的数据
	if len(r.configuration.GetGlobal().GetLocation().GetProviders()) != 0 {
		return
	}

	if nil != location {
		// 只在地域信息首次获取或发生变化时打印日志，避免重复输出相同内容
		if r.lastLocation == nil || *r.lastLocation != *location {
			r.logCtx.GetBaseLogger().Infof("current client area info is {Region:%s, Zone:%s, Campus:%s}",
				location.Region, location.Zone, location.Campus)
		}
	}
	if r.globalCtx.SetCurrentLocation(location, lastErr) {
		r.logCtx.GetBaseLogger().Infof("client area info is ready")
	}
}
