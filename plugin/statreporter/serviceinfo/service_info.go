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

package serviceinfo

import (
	"context"
	"fmt"
	"github.com/polarismesh/polaris-go/pkg/clock"
	sysconfig "github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/flow/data"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/local"
	"github.com/polarismesh/polaris-go/pkg/model/pb"
	"github.com/polarismesh/polaris-go/pkg/network"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/localregistry"
	"github.com/polarismesh/polaris-go/plugin/statreporter/pb/util"
	monitorpb "github.com/polarismesh/polaris-go/plugin/statreporter/pb/v1"
	"github.com/golang/protobuf/ptypes/timestamp"
	"github.com/google/uuid"
	"strings"
	"sync"
	"time"
)

const (
	keyExpireDuration = 3 * time.Hour
)

//上报缓存信息的插件
type Reporter struct {
	*plugin.PluginBase
	*common.RunContext
	config             *Config
	connectionManager  network.ConnectionManager
	connection         *network.Connection
	cacheClient        monitorpb.GrpcAPI_CollectSDKCacheClient
	circuitBreakClient monitorpb.GrpcAPI_CollectCircuitBreakClient
	meshClient         monitorpb.GrpcAPI_CollectMeshResourceClient
	clientCancel       context.CancelFunc
	uploadToMonitor    bool
	//本地缓存插件
	registry         localregistry.LocalRegistry
	registryPlugName string
	//全局ctx
	globalCtx model.ValueContext
	//用于存储周期内的服务历史变更
	statusMap *sync.Map
	//用于存储周期内的网格规则变更记录
	meshStatusMap *sync.Map
	//插件工厂
	plugins plugin.Supplier
	//全死全活发生和结束的原因
	recoverAllStartReason string
	recoverAllEndReason   string
}

//插件类型
func (s *Reporter) Type() common.Type {
	return common.TypeStatReporter
}

//插件名称
func (s *Reporter) Name() string {
	return "serviceCache"
}

// enable
func (s *Reporter) IsEnable(cfg sysconfig.Configuration) bool {
	if cfg.GetGlobal().GetSystem().GetMode() == model.ModeWithAgent {
		return false
	} else {
		for _, name := range cfg.GetGlobal().GetStatReporter().GetChain() {
			if name == s.Name() {
				return true
			}
		}
	}
	return false
}

//初始化
func (s *Reporter) Init(ctx *plugin.InitContext) error {
	s.RunContext = common.NewRunContext()
	s.globalCtx = ctx.ValueCtx
	s.statusMap = &sync.Map{}
	s.meshStatusMap = &sync.Map{}
	s.connectionManager = ctx.ConnManager
	s.PluginBase = plugin.NewPluginBase(ctx)
	s.registry, _ = data.GetRegistry(ctx.Config, ctx.Plugins)
	s.config = &Config{}
	s.registryPlugName = ctx.Config.GetConsumer().GetLocalCache().GetType()
	s.plugins = ctx.Plugins
	cfgValue := ctx.Config.GetGlobal().GetStatReporter().GetPluginConfig(s.Name())
	if nil == cfgValue {
		return model.NewSDKError(model.ErrCodeAPIInvalidConfig, nil, "config of statReporter serviceCache must be provided")
	}
	s.config = cfgValue.(*Config)
	s.config.SetDefault()
	err := s.config.Verify()
	if err != nil {
		return err
	}
	ctx.Plugins.RegisterEventSubscriber(common.OnServiceAdded, common.PluginEventHandler{Callback: s.onCacheChanged})
	ctx.Plugins.RegisterEventSubscriber(common.OnServiceUpdated, common.PluginEventHandler{Callback: s.onCacheChanged})
	ctx.Plugins.RegisterEventSubscriber(common.OnServiceDeleted, common.PluginEventHandler{Callback: s.onCacheChanged})
	ctx.Plugins.RegisterEventSubscriber(common.OnRoutedClusterReturned,
		common.PluginEventHandler{Callback: s.onRecoverAllChanged})
	ctx.Plugins.RegisterEventSubscriber(common.OnInstanceLocalValueCreated,
		common.PluginEventHandler{Callback: s.insertCircuitBreakStatus})
	ctx.Plugins.RegisterEventSubscriber(common.OnServiceLocalValueCreated,
		common.PluginEventHandler{Callback: s.createSvcLocalValue})
	s.recoverAllStartReason = fmt.Sprintf("available instances ratio less than or equal to %.2f",
		ctx.Config.GetConsumer().GetServiceRouter().GetPercentOfMinInstances())
	s.recoverAllEndReason = fmt.Sprintf("available instances ratio greater than %.2f",
		ctx.Config.GetConsumer().GetServiceRouter().GetPercentOfMinInstances())
	return nil
}

// 启动上报协程
func (s *Reporter) Start() error {
	go s.uploadStatusHistory()
	return nil
}

//这个插件不实现ReportStat接口，按照服务变化来收集统计信息
func (s *Reporter) ReportStat(t model.MetricType, info model.InstanceGauge) error {
	if t != model.CircuitBreakStat {
		return nil
	}
	err := info.Validate()
	if nil != err {
		return err
	}
	inst := info.GetCalledInstance().(*pb.InstanceInProto)
	cbNode, err := createCircuitBreakNode(info.GetCircuitBreakerStatus(), inst.GetCircuitBreakerStatus())
	if nil != err {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, err, "invalid circuitbreak status change")
	}
	s.addCircuitBreakNode(cbNode, inst.GetInstanceLocalValue())
	return nil
}

//服务实例信息变更情况
type instancesChangeData struct {
	revision string
	changes  *monitorpb.InstancesChange
}

//在缓存服务发生变化时触发事件并进行处理
func (s *Reporter) onCacheChanged(event *common.PluginEvent) error {
	var rv model.RegistryValue
	deleteCache := common.OnServiceDeleted == event.EventType
	svcEventObj := event.EventObject.(*common.ServiceEventObject)
	if deleteCache {
		rv = svcEventObj.OldValue.(model.RegistryValue)
	} else {
		rv = svcEventObj.NewValue.(model.RegistryValue)
	}
	if !rv.IsInitialized() {
		return nil
	}
	currentTime := s.globalCtx.Now()
	if model.EventInstances == rv.GetType() {
		svcObj := rv.(model.ServiceInstances)
		svcKey := &model.ServiceKey{
			Namespace: svcObj.GetNamespace(),
			Service:   svcObj.GetService(),
		}
		instancesChange, oldRevision, newRevision := calculateInstancesChange(event.EventType, svcEventObj)
		logChangeInstances(svcKey, oldRevision, newRevision, instancesChange)
		changeData := &instancesChangeData{
			revision: "",
			changes:  instancesChange,
		}
		sh := s.getStatusHistory(svcKey, true)
		if deleteCache {
			changeData.revision = ""
			sh.histories[serviceStatus].addDeleteStatus(changeData, currentTime)
		} else {
			changeData.revision = svcObj.GetRevision()
			sh.histories[serviceStatus].addStatus(changeData, currentTime)
		}
		sh.lastUpdateTime.Store(currentTime)
	} else if model.EventRouting == rv.GetType() {
		routingObj := rv.(model.ServiceRule)
		routingKey := &model.ServiceKey{
			Namespace: routingObj.GetNamespace(),
			Service:   routingObj.GetService(),
		}
		sh := s.getStatusHistory(routingKey, true)
		if deleteCache {
			sh.histories[routingStatus].addDeleteStatus("", currentTime)
		} else {
			sh.histories[routingStatus].addStatus(routingObj.GetRevision(), currentTime)
		}
		sh.lastUpdateTime.Store(currentTime)
	} else if model.EventRateLimiting == rv.GetType() {
		rateLimitObj := rv.(model.ServiceRule)
		rateLimitKey := &model.ServiceKey{
			Namespace: rateLimitObj.GetNamespace(),
			Service:   rateLimitObj.GetService(),
		}
		sh := s.getStatusHistory(rateLimitKey, true)
		if deleteCache {
			sh.histories[rateLimitStatus].addDeleteStatus("", currentTime)
		} else {
			sh.histories[rateLimitStatus].addStatus(rateLimitObj.GetRevision(), currentTime)
		}
		s.handleRateLimitRuleRevisions(sh.rateLimitRules, svcEventObj.DiffInfo.(*common.RateLimitDiffInfo), currentTime)
	} else if model.EventMeshConfig == rv.GetType() {
		meshObj := rv.(model.MeshConfig)
		diffInfo := svcEventObj.DiffInfo.(*common.MeshResourceDiffInfo)
		meshKey := &meshKey{
			meshID:  diffInfo.MeshID,
			typeUrl: diffInfo.ResourceType.GetTypeUrl().GetValue(),
		}
		sh := s.getMeshStatusHistory(meshKey, true)
		if deleteCache {
			sh.history.addDeleteStatus("", currentTime)
		} else {
			sh.history.addStatus(meshObj.GetRevision(), currentTime)
		}
		s.handleMeshConfigRevision(sh.meshResources, svcEventObj.DiffInfo.(*common.MeshResourceDiffInfo), currentTime)
	}
	return nil
}

//根据事件类型计算实例变更状况
func calculateInstancesChange(eventType common.PluginEventType,
	eventObj *common.ServiceEventObject) (*monitorpb.InstancesChange, string, string) {
	res := &monitorpb.InstancesChange{}
	var oldRevision, newRevision string
	switch eventType {
	case common.OnServiceAdded:
		newInstances := eventObj.NewValue.(model.ServiceInstances)
		res.AddedInstances = createCompleteInstances(newInstances.GetInstances())
		res.NewCount = uint32(len(newInstances.GetInstances()))
		newRevision = newInstances.GetRevision()
	case common.OnServiceDeleted:
		oldInstances := eventObj.OldValue.(model.ServiceInstances)
		res.DeletedInstances = createCompleteInstances(oldInstances.GetInstances())
		res.OldCount = uint32(len(oldInstances.GetInstances()))
		oldRevision = oldInstances.GetRevision()
	case common.OnServiceUpdated:
		oldInstances := eventObj.OldValue.(model.ServiceInstances)
		newInstances := eventObj.NewValue.(model.ServiceInstances)
		res.OldCount = uint32(len(oldInstances.GetInstances()))
		res.NewCount = uint32(len(newInstances.GetInstances()))
		newRevision = newInstances.GetRevision()
		oldRevision = oldInstances.GetRevision()
		getChangedInstances(res, oldInstances, newInstances)
	}
	return res, oldRevision, newRevision
}

//打印实例变化
func logChangeInstances(svcKey *model.ServiceKey, oldRevision string, newRevision string,
	instances *monitorpb.InstancesChange) {
	log.GetBaseLogger().Infof("service instances of %s change, oldRevision %s, newRevision %s,"+
		" oldCount %d, newCount %d", svcKey, oldRevision, newRevision, instances.OldCount, instances.NewCount)
	for _, addInst := range instances.AddedInstances {
		log.GetBaseLogger().Infof("add instance of %s: %s:%d, status: %s",
			svcKey, addInst.Host, addInst.Port, addInst.Info)
	}
	for _, modInst := range instances.ModifiedInstances {
		log.GetBaseLogger().Infof("modify instance of %s: %s:%d, status: %s",
			svcKey, modInst.Host, modInst.Port, modInst.Info)
	}
	for _, delInst := range instances.DeletedInstances {
		log.GetBaseLogger().Infof("delete instance of %s: %s:%d, status: %s",
			svcKey, delInst.Host, delInst.Port, delInst.Info)
	}
}

//创建一个completeinstances数组
func createCompleteInstances(insts []model.Instance) []*monitorpb.ChangeInstance {
	//insts := svcInst.GetInstances()
	res := make([]*monitorpb.ChangeInstance, len(insts))
	for idx, inst := range insts {
		cinst := &monitorpb.ChangeInstance{
			VpcId: inst.GetVpcId(),
			Host:  inst.GetHost(),
			Port:  inst.GetPort(),
			Info:  totalInstanceInfo(inst),
		}
		res[idx] = cinst
	}
	return res
}

//获取一个完整实例的info
func totalInstanceInfo(inst model.Instance) string {
	return fmt.Sprintf("healthy:%v;isolate:%v;weight:%d", inst.IsHealthy(), inst.IsIsolated(), inst.GetWeight())
}

//获取一个有过修改的实例信息
func getModifiedInstance(oldInst, newInst model.Instance) *monitorpb.ChangeInstance {
	var info = []string{"", "", ""}
	var change bool
	if newInst.IsHealthy() != oldInst.IsHealthy() {
		info[0] = fmt.Sprintf("healthy:%v to %v", oldInst.IsHealthy(), newInst.IsHealthy())
		change = true
	}
	if newInst.IsIsolated() != oldInst.IsIsolated() {
		info[1] = fmt.Sprintf("isolate:%v to %v", oldInst.IsIsolated(), newInst.IsIsolated())
		change = true
	}
	if oldInst.GetWeight() != newInst.GetWeight() {
		info[2] = fmt.Sprintf("weight:%d to %d", oldInst.GetWeight(), newInst.GetWeight())
		change = true
	}
	if !change {
		return nil
	}
	if info[0] == "" {
		info[0] = fmt.Sprintf("healthy:%v", newInst.IsHealthy())
	}
	if info[1] == "" {
		info[1] = fmt.Sprintf("isolate:%v", newInst.IsIsolated())
	}
	if info[2] == "" {
		info[2] = fmt.Sprintf("weight:%d", newInst.GetWeight())
	}
	return &monitorpb.ChangeInstance{
		VpcId: newInst.GetVpcId(),
		Host:  newInst.GetHost(),
		Port:  newInst.GetPort(),
		Info:  strings.Join(info, ";"),
	}
}

//计算实例变更情况
func getChangedInstances(change *monitorpb.InstancesChange, oldSvcInstances model.ServiceInstances,
	newSvcInstances model.ServiceInstances) {
	oldInstances := oldSvcInstances.GetInstances()
	oldInstMap := make(map[string]model.Instance, len(oldInstances))
	if len(oldInstances) > 0 {
		for _, inst := range oldInstances {
			oldInstMap[inst.GetId()] = inst
		}
	}
	newInstances := newSvcInstances.GetInstances()
	var addInstances []model.Instance
	var deleteInstances []model.Instance
	for _, newInst := range newInstances {
		instId := newInst.GetId()
		if oldInst, ok := oldInstMap[instId]; ok {
			modifiedInst := getModifiedInstance(oldInst, newInst)
			if modifiedInst != nil {
				change.ModifiedInstances = append(change.ModifiedInstances, modifiedInst)
			}
			delete(oldInstMap, instId)
		} else {
			addInstances = append(addInstances, newInst)
		}
	}
	for _, oldInst := range oldInstMap {
		deleteInstances = append(deleteInstances, oldInst)
	}
	change.AddedInstances = createCompleteInstances(addInstances)
	change.DeletedInstances = createCompleteInstances(deleteInstances)
}

//处理每个具体的限流规则
func (s *Reporter) handleRateLimitRuleRevisions(rulesMap *sync.Map, diffInfo *common.RateLimitDiffInfo,
	currentTime time.Time) {
	for k, v := range diffInfo.UpdatedRules {
		var statusListIntf interface{}
		var ok bool
		statusListIntf, ok = rulesMap.Load(k)
		if !ok {
			statusListIntf, ok = rulesMap.LoadOrStore(k, newStatusList())
		}
		statusListIntf.(*statusList).addStatus(v.NewRevision, currentTime)
	}
	for k := range diffInfo.DeletedRules {
		var statusListIntf interface{}
		var ok bool
		statusListIntf, ok = rulesMap.Load(k)
		if !ok {
			statusListIntf, ok = rulesMap.LoadOrStore(k, newStatusList())
		}
		statusListIntf.(*statusList).addDeleteStatus("", currentTime)
	}
}

//处理网格规则变更记录
func (s *Reporter) handleMeshConfigRevision(resourceMap *sync.Map, diffInfo *common.MeshResourceDiffInfo,
	currentTime time.Time) {
	for k, v := range diffInfo.UpdatedResources {
		var statusListInf interface{}
		var ok bool
		statusListInf, ok = resourceMap.Load(k)
		if !ok {
			statusListInf, ok = resourceMap.LoadOrStore(k, newStatusList())
		}
		statusListInf.(*statusList).addStatus(v.NewRevision, currentTime)
	}
	for k := range diffInfo.DeletedResources {
		var statusListInf interface{}
		var ok bool
		statusListInf, ok = resourceMap.Load(k)
		if !ok {
			statusListInf, ok = resourceMap.LoadOrStore(k, newStatusList())
		}
		statusListInf.(*statusList).addDeleteStatus("", currentTime)
	}
}

//定时上报协程
func (s *Reporter) uploadStatusHistory() {
	t := time.NewTicker(*s.config.ReportInterval)
	cleanTimer := time.NewTicker(keyExpireDuration)
	defer t.Stop()
	defer cleanTimer.Stop()
	timeStart := s.globalCtx.Now()
	for {
		select {
		case <-s.Done():
			if nil != s.clientCancel {
				s.clientCancel()
			}
			log.GetBaseLogger().Infof("uploadStatusHistory of serviceInfo stat_monitor has been terminated")
			return
		case <-t.C:
			timeStart = time.Now()
			deadline := timeStart.Add(*s.config.ReportInterval)
			s.uploadToMonitor = true
			err := s.connectToMonitor(deadline)
			if nil != err {
				log.GetStatReportLogger().Errorf("fail to connect to monitor to report service info, error %v", err)
				s.uploadToMonitor = false
			}
			s.sendRevisionHistory()
			s.sendCircuitBreakHistory()
			s.sendMeshRevisionHistory()
			if s.uploadToMonitor {
				s.closeConnection()
			}
		case <-cleanTimer.C:
			s.cleanExpiredClusterRecoverAllRecord()
		}
	}
}

//连接到monitor
func (s *Reporter) connectToMonitor(deadline time.Time) error {
	var err error
	s.connection, err = s.connectionManager.GetConnection("ReportStatus", sysconfig.MonitorCluster)
	if nil != err {
		log.GetStatReportLogger().Errorf("fail to connect to monitor, err: %s", err.Error())
		return err
	}
	client := monitorpb.NewGrpcAPIClient(network.ToGRPCConn(s.connection.Conn))
	var clientCtx context.Context
	clientCtx, s.clientCancel = context.WithDeadline(context.Background(), deadline)
	s.cacheClient, err = client.CollectSDKCache(clientCtx)
	if nil != err {
		log.GetStatReportLogger().Errorf("fail to create stream to report sdk cache, err: %s", err.Error())
		s.closeConnection()
		return err
	}
	s.circuitBreakClient, err = client.CollectCircuitBreak(clientCtx)
	if nil != err {
		log.GetStatReportLogger().Errorf("fail to create stream to report circuitbreak status, err: %s,"+
			" monitor server is %s", err.Error(), s.connection.ConnID)
		s.closeConnection()
		return err
	}
	s.meshClient, err = client.CollectMeshResource(clientCtx)
	if nil != err {
		log.GetStatReportLogger().Errorf("fail to create stream to report mesh config status, err: %s,"+
			" monitor server is %s", err.Error(), s.connection.ConnID)
		s.closeConnection()
		return err
	}
	return nil
}

//关闭连接
func (s *Reporter) closeConnection() {
	s.clientCancel()
	s.clientCancel = nil
	if s.cacheClient != nil {
		s.cacheClient.CloseSend()
		s.cacheClient = nil
	}
	if s.circuitBreakClient != nil {
		s.circuitBreakClient.CloseSend()
		s.circuitBreakClient = nil
	}
	if s.meshClient != nil {
		s.meshClient.CloseSend()
		s.meshClient = nil
	}
	s.connection.Release("ReportStatus")
}

//发送版本变更记录统计数据
func (s *Reporter) sendRevisionHistory() {
	s.statusMap.Range(func(key, value interface{}) bool {
		svcKey := key.(model.ServiceKey)
		sh := value.(*statusHistory)
		//svcStatus, svcSeq, svcCount := sh.histories[serviceStatus].getNodes()
		//routingStatus, routingSeq, routingCount := sh.histories[routingStatus].getNodes()
		//rateLimitStatus, rateLimitSeq, rateLimitCount := sh.histories[rateLimitStatus].getNodes()
		//shLastTime := sh.lastUpdateTime.Load().(time.Time)
		keyDeleted := s.sendStatusToMonitor(&svcKey, sh)
		if keyDeleted {
			s.statusMap.Delete(key)
		}
		return true
	})
}

//将网格变更记录上传到monitor
func (s *Reporter) sendMeshRevisionHistory() {
	s.meshStatusMap.Range(func(key, value interface{}) bool {
		meshKey := key.(meshKey)
		sh := value.(*meshStatusHistory)
		keyDeleted := s.sendMeshStatusToMonitor(&meshKey, sh)
		if keyDeleted {
			s.meshStatusMap.Delete(meshKey)
		}
		return true
	})
}

//将数据上报给monitor
func (s *Reporter) sendStatusToMonitor(serviceKey *model.ServiceKey, sh *statusHistory) (deleteSvc bool) {
	svcStatus, svcSeq, svcCount := sh.histories[serviceStatus].getNodes()
	routingStatus, routingSeq, routingCount := sh.histories[routingStatus].getNodes()
	rateLimitStatus, rateLimitSeq, rateLimitCount := sh.histories[rateLimitStatus].getNodes()
	if svcCount == 0 && routingCount == 0 && rateLimitCount == 0 {
		return
	}
	t, _ := s.globalCtx.GetValue(model.ContextKeyToken)
	sdkToken := t.(model.SDKToken)
	req := &monitorpb.ServiceInfo{
		Id:                  uuid.New().String(),
		SdkToken:            util.GetPBSDkToken(sdkToken),
		Namespace:           serviceKey.Namespace,
		Service:             serviceKey.Service,
		InstancesHistory:    nil,
		InstanceEliminated:  svcSeq == 0,
		RoutingHistory:      nil,
		RoutingEliminated:   routingSeq == 0,
		RateLimitHistory:    nil,
		RateLimitEliminated: rateLimitSeq == 0,
	}
	if len(sdkToken.IP) == 0 {
		req.SdkToken.Ip = s.connectionManager.GetClientInfo().GetIPString()
	}
	var instHistory *monitorpb.InstancesHistory
	var routingHistory *monitorpb.RoutingHistory
	var rateLimitHistory *monitorpb.RateLimitHistory
	if svcCount > 0 {
		instHistory = &monitorpb.InstancesHistory{}
		s.fillInstancesChange(instHistory, svcStatus, svcCount)
		//instHistory.Revision = make([]*monitorpb.RevisionHistory, svcCount, svcCount)
		//s.fillRevisions(instHistory.Revision, svcStatus, svcCount)
	}
	if routingCount > 0 {
		routingHistory = &monitorpb.RoutingHistory{}
		routingHistory.Revision = make([]*monitorpb.RevisionHistory, routingCount, routingCount)
		s.fillRevisions(routingHistory.Revision, routingStatus, routingCount)
	}
	if rateLimitCount > 0 {
		rateLimitHistory = &monitorpb.RateLimitHistory{}
		rateLimitHistory.Revision = make([]*monitorpb.RevisionHistory, rateLimitCount, rateLimitCount)
		s.fillRevisions(rateLimitHistory.Revision, rateLimitStatus, rateLimitCount)
		req.SingleRateLimitHistories = s.getSingleRateLimitRevisions(sh.rateLimitRules)
	}
	req.InstancesHistory = instHistory
	req.RoutingHistory = routingHistory
	req.RateLimitHistory = rateLimitHistory
	log.GetStatLogger().Infof("sdk cache stat:%v", req)
	if !s.uploadToMonitor {
		log.GetStatReportLogger().Warnf("Skip to report sdk cache to monitor for connection problem,"+
			" id: %s", req.Id)
		return
	}
	err := s.cacheClient.Send(req)
	if nil != err {
		log.GetStatReportLogger().Errorf("fail to report sdk cache, id: %s, err %s, monitor server is %s",
			req.Id, err.Error(), s.connection.ConnID)
	}
	resp, err := s.cacheClient.Recv()
	if nil != err || resp.Id.GetValue() != req.Id || resp.Code.GetValue() != monitorpb.ReceiveSuccess {
		log.GetStatReportLogger().Errorf("fail to report sdk cache, resp is %v, err is %v, monitor server is %s",
			resp, err, s.connection.ConnID)
	} else {
		log.GetStatReportLogger().Infof("Success to report sdk cache, resp is %v, monitor server is %s",
			resp, s.connection.ConnID)
	}
	shLastTime := sh.lastUpdateTime.Load().(time.Time)
	keyDeleted := svcSeq == 0 && routingSeq == 0 && rateLimitSeq == 0 &&
		!shLastTime.Add(keyExpireDuration).After(s.globalCtx.Now())
	return keyDeleted
}

// 将网格规则的变更记录发送给monitor
func (s *Reporter) sendMeshStatusToMonitor(mk *meshKey, sh *meshStatusHistory) (deleteConfig bool) {
	meshStatus, meshSeq, meshCount := sh.history.getNodes()
	if meshCount == 0 {
		return
	}
	t, _ := s.globalCtx.GetValue(model.ContextKeyToken)
	sdkToken := t.(model.SDKToken)
	req := &monitorpb.MeshResourceInfo{
		Id:                        uuid.New().String(),
		SdkToken:                  util.GetPBSDkToken(sdkToken),
		TypeUrl:                   mk.typeUrl,
		MeshId:                    mk.meshID,
		Revision:                  nil,
		SingleMeshResourceHistory: nil,
	}
	if sdkToken.IP == "" {
		req.SdkToken.Ip = s.connectionManager.GetClientInfo().GetIPString()
	}

	req.Revision = make([]*monitorpb.RevisionHistory, meshCount, meshCount)
	req.MeshConfigDeleted = meshSeq == 0
	s.fillRevisions(req.Revision, meshStatus, meshCount)
	req.SingleMeshResourceHistory = s.getSingleMeshResourceRevisions(sh.meshResources)
	log.GetStatLogger().Infof("sdk mesh cache stat:%v", req)
	if !s.uploadToMonitor {
		log.GetStatReportLogger().Warnf("Skip to report sdk mesh cache to monitor for connection problem,"+
			" id: %s", req.Id)
		return
	}
	err := s.meshClient.Send(req)
	if nil != err {
		log.GetStatReportLogger().Errorf("fail to report sdk cache, id: %s, err %s, monitor server is %s",
			req.Id, err.Error(), s.connection.ConnID)
	}
	resp, err := s.meshClient.Recv()
	if nil != err || resp.Id.GetValue() != req.Id || resp.Code.GetValue() != monitorpb.ReceiveSuccess {
		log.GetStatReportLogger().Errorf("fail to report sdk mesh cache, resp is %v, err is %v, monitor server is %s",
			resp, err, s.connection.ConnID)
	} else {
		log.GetStatReportLogger().Infof("Success to report sdk mesh cache, resp is %v, monitor server is %s",
			resp, s.connection.ConnID)
	}

	shLastTime := sh.lastUpdateTime.Load().(time.Time)
	deleteConfig = meshSeq == 0 && !shLastTime.Add(keyExpireDuration).After(clock.GetClock().Now())

	return deleteConfig
}

//获取每个单独的网格规则的变更记录
func (s *Reporter) getSingleMeshResourceRevisions(resourceMap *sync.Map) []*monitorpb.SingleMeshResourceRuleHistory {
	var res []*monitorpb.SingleMeshResourceRuleHistory
	resourceMap.Range(func(k, v interface{}) bool {
		resourceName := k.(string)
		resourceStatusList := v.(*statusList)
		resourceStatus, resourceSeq, resourceCount := resourceStatusList.getNodes()
		if resourceCount > 0 {
			revisions := make([]*monitorpb.RevisionHistory, resourceCount, resourceCount)
			s.fillRevisions(revisions, resourceStatus, resourceCount)
			res = append(res, &monitorpb.SingleMeshResourceRuleHistory{
				Name:                resourceName,
				Revision:            revisions,
				MeshResourceDeleted: resourceSeq == 0,
			})
		}
		if resourceSeq == 0 {
			resourceMap.Delete(k)
		}
		return true
	})
	return res
}

//将每个具有ruleId的限流规则的版本号变化提取出来
func (s *Reporter) getSingleRateLimitRevisions(rulesMap *sync.Map) []*monitorpb.SingleRateLimitRuleHistory {
	var res []*monitorpb.SingleRateLimitRuleHistory
	rulesMap.Range(func(k, v interface{}) bool {
		ruleId := k.(string)
		ruleStatusList := v.(*statusList)
		ruleStatus, ruleSeq, ruleCount := ruleStatusList.getNodes()
		if ruleCount > 0 {
			revisions := make([]*monitorpb.RevisionHistory, ruleCount, ruleCount)
			s.fillRevisions(revisions, ruleStatus, ruleCount)
			res = append(res, &monitorpb.SingleRateLimitRuleHistory{
				RuleId:   ruleId,
				Revision: revisions,
			})
		}
		if ruleSeq == 0 {
			rulesMap.Delete(k)
		}
		return true
	})
	return res
}

//生成发送的实例信息变更数据
func (s *Reporter) fillInstancesChange(history *monitorpb.InstancesHistory, nodes *statusNode, count uint32) {
	history.Revision = make([]*monitorpb.RevisionHistory, count)
	for i := uint32(0); i < count; i++ {
		data := nodes.changeData.(*instancesChangeData)
		history.Revision[i] = &monitorpb.RevisionHistory{
			Time: &timestamp.Timestamp{
				Seconds: nodes.changeTime.Unix(),
				Nanos:   int32(nodes.changeTime.Nanosecond()),
			},
			ChangeSeq:      nodes.changeSeq,
			Revision:       data.revision,
			InstanceChange: data.changes,
		}
		nodes = nodes.next
	}
}

//生成发送的pb数据
func (s *Reporter) fillRevisions(revisions []*monitorpb.RevisionHistory, nodes *statusNode, count uint32) {
	for i := uint32(0); i < count; i++ {
		revisions[i] = &monitorpb.RevisionHistory{
			Time: &timestamp.Timestamp{
				Seconds: nodes.changeTime.Unix(),
				Nanos:   int32(nodes.changeTime.Nanosecond()),
			},
			ChangeSeq: nodes.changeSeq,
			Revision:  nodes.changeData.(string),
		}
		nodes = nodes.next
	}
}

//获取或者创建statusHistory
func (s *Reporter) getStatusHistory(svcKey *model.ServiceKey, createWhenEmpty bool) *statusHistory {
	res, ok := s.statusMap.Load(*svcKey)
	if ok {
		return res.(*statusHistory)
	}
	if !createWhenEmpty {
		return nil
	}
	res, _ = s.statusMap.LoadOrStore(*svcKey, newStatusHistory(s.globalCtx.Now()))
	return res.(*statusHistory)
}

//获取或者创建meshStatusHistory
func (s *Reporter) getMeshStatusHistory(mk *meshKey, createWhenEmpty bool) *meshStatusHistory {
	res, ok := s.meshStatusMap.Load(*mk)
	if ok {
		return res.(*meshStatusHistory)
	}
	if !createWhenEmpty {
		return nil
	}
	newStatus := &meshStatusHistory{
		history:       newStatusList(),
		meshResources: &sync.Map{},
	}
	newStatus.lastUpdateTime.Store(clock.GetClock().Now())
	res, _ = s.meshStatusMap.LoadOrStore(*mk, newStatus)
	return res.(*meshStatusHistory)
}

//设置熔断数据
func (s *Reporter) generateCircuitBreakData(event *common.PluginEvent) {
	localValue := event.EventObject.(*local.DefaultInstanceLocalValue)
	localValue.SetExtendedData(s.ID(), newStatusList())
}

//插入一个熔断状态数据结构
func (s *Reporter) insertCircuitBreakStatus(event *common.PluginEvent) error {
	localValue := event.EventObject.(*local.DefaultInstanceLocalValue)
	localValue.SetExtendedData(s.ID(), newStatusList())
	return nil
}

//往实例的熔断列表中添加一个状态变化
func (s *Reporter) addCircuitBreakNode(cbNode *circuitBreakNode, localValue local.InstanceLocalValue) {
	cbIf := localValue.GetExtendedData(s.ID())
	cbList := cbIf.(*statusList)
	cbList.addStatus(cbNode, s.globalCtx.Now())
}

//创建一个服务的全死全活记录数据结构
func (s *Reporter) createSvcLocalValue(event *common.PluginEvent) error {
	lv := event.EventObject.(local.ServiceLocalValue)
	lv.SetServiceDataByPluginId(s.ID(),
		&serviceRecoverAllMap{changeList: newStatusList(), clusterRecords: &sync.Map{}})
	return nil
}

// destroy 解决匿名组合中该函数二义性问题
func (s *Reporter) Destroy() error {
	err := s.PluginBase.Destroy()
	if err != nil {
		return err
	}
	err = s.RunContext.Destroy()
	if err != nil {
		return err
	}
	return nil
}

//注册上报插件
func init() {
	plugin.RegisterConfigurablePlugin(&Reporter{}, &Config{})
}
