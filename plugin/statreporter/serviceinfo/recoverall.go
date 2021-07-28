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
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/pb"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/plugin/statreporter/pb/util"
	monitorpb "github.com/polarismesh/polaris-go/plugin/statreporter/pb/v1"
	"github.com/golang/protobuf/ptypes/timestamp"
	"github.com/google/uuid"
	"sync"
	"sync/atomic"
	"time"
)

const (
	NoRecoverAll uint32 = 0
	RecoverAll   uint32 = 1
)

//在一个cluster的全死全活状态发生改变时触发
func (s *Reporter) onRecoverAllChanged(event *common.PluginEvent) error {
	cluster := event.EventObject.(*model.Cluster)
	svcLocalValue := cluster.GetClusters().GetServiceInstances().(*pb.ServiceInstancesInProto).GetServiceLocalValue()
	//if svcLocalValue.GetServiceDataByPluginId(s.ID()) == nil {
	//	return nil
	//}
	serviceRecoverAllMap := svcLocalValue.GetServiceDataByPluginId(s.ID()).(*serviceRecoverAllMap)
	clusterRecord := s.getClusterRecoverAllRecord(serviceRecoverAllMap.clusterRecords, cluster.ClusterKey, true)
	//如果现在cluster是全死全活，并且之前不是全死全活状态，那么添加一个开始全死全活的记录
	if cluster.HasLimitedInstances && atomic.CompareAndSwapUint32(&clusterRecord.currentStatus, NoRecoverAll, RecoverAll) {
		serviceRecoverAllMap.changeList.addStatus(&recoverAllChange{clusterInfo: clusterRecord.clusterInfo,
			statusChange: monitorpb.RecoverAllStatus_Start,
			reason:       s.recoverAllStartReason,
		}, s.globalCtx.Now())
		printRecoverAllCluster(cluster, true)
	}
	//如果现在cluster不是全死全活，并且之前的记录是全死全活，那么添加一个结束全死全活的记录
	if !cluster.HasLimitedInstances &&
		atomic.CompareAndSwapUint32(&clusterRecord.currentStatus, RecoverAll, NoRecoverAll) {
		serviceRecoverAllMap.changeList.addStatus(&recoverAllChange{
			clusterInfo:  clusterRecord.clusterInfo,
			statusChange: monitorpb.RecoverAllStatus_End,
			reason:       s.recoverAllEndReason,
		}, s.globalCtx.Now())
		printRecoverAllCluster(cluster, false)
	}
	//更新这个cluster的最后检测时间
	clusterRecord.lastCheckTime.Store(s.globalCtx.Now())
	return nil
}

//打印全死全活cluster的信息
func printRecoverAllCluster(cluster *model.Cluster, recoverAllStart bool) {
	allInstances := cluster.GetClusterValue().GetAllInstanceSet()
	selectableInstances := cluster.GetClusterValue().GetInstancesSet(true, false)
	availableInstances := cluster.GetClusterValue().GetInstancesSet(false, true)
	healthyInstances := cluster.GetClusterValue().GetInstancesSet(false, false)
	if recoverAllStart {
		log.GetBaseLogger().Infof("cluster %s of service %s recover all starts, allInstances: %s, "+
			"selectableInstances: %s, availableInstances: %s, healthyInstances: %s",
			cluster.ClusterKey, cluster.GetClusters().GetServiceKey(), allInstances, selectableInstances,
			availableInstances, healthyInstances)
	} else {
		log.GetBaseLogger().Infof("cluster %s of service %s recover all stops, allInstances: %s, "+
			"selectableInstances: %s, availableInstances: %s, healthyInstances: %s",
			cluster.ClusterKey, cluster.GetClusters().GetServiceKey(), allInstances, selectableInstances,
			availableInstances, healthyInstances)
	}
}

//清空过期的clusterkey
func (s *Reporter) cleanExpiredClusterRecoverAllRecord() {
	now := s.globalCtx.Now()
	registry := s.registry
	if nil == registry {
		return
	}
	services := registry.GetServices()
	for svcValue := range services {
		svc := svcValue.(model.ServiceKey)
		svcInstances := registry.GetInstances(&svc, false, true)
		if !svcInstances.IsInitialized() || len(svcInstances.GetInstances()) == 0 {
			continue
		}

		actualSvcInstances := svcInstances.(*pb.ServiceInstancesInProto)
		svcLocalValue := actualSvcInstances.GetServiceLocalValue()
		serviceRecords := svcLocalValue.GetServiceDataByPluginId(s.ID()).(*serviceRecoverAllMap)

		if nil != serviceRecords.clusterRecords {
			serviceRecords.clusterRecords.Range(func(k, v interface{}) bool {
				rec := v.(*clusterRecoverAllCheck)
				if rec.lastCheckTime.Load().(time.Time).Add(keyExpireDuration).Before(now) {
					log.GetBaseLogger().Debugf("clean expired clusterKey %v of %s\n", k.(model.ClusterKey), svc)
					serviceRecords.clusterRecords.Delete(k)
				}
				return true
			})
		}
	}
}

//获取或创建某个cluster的全死全活记录
func (s *Reporter) getClusterRecoverAllRecord(svc *sync.Map, key model.ClusterKey,
	createWhenEmpty bool) *clusterRecoverAllCheck {
	res, ok := svc.Load(key)
	if ok {
		return res.(*clusterRecoverAllCheck)
	}
	if !createWhenEmpty {
		return nil
	}
	newRecord := &clusterRecoverAllCheck{currentStatus: NoRecoverAll,
		clusterInfo: key.ComposeMetaValue + "-" + key.Location.String()}
	newRecord.lastCheckTime.Store(s.globalCtx.Now())
	res, _ = svc.LoadOrStore(key, newRecord)
	return res.(*clusterRecoverAllCheck)
}

//发送熔断和全死全活记录
func (s *Reporter) sendCircuitBreakHistory() {
	registry := s.registry
	if nil == registry {
		log.GetBaseLogger().Warnf("sendCircuitBreakHistory: registry not ready, wait for next period")
		return
	}
	services := registry.GetServices()
	instanceKey := model.InstanceKey{}
	t, _ := s.globalCtx.GetValue(model.ContextKeyToken)
	sdkToken := t.(model.SDKToken)
	if len(sdkToken.IP) == 0 {
		sdkToken.IP = s.connectionManager.GetClientInfo().GetIPString()
	}
	for svcValue := range services {
		instanceKey.ServiceKey = svcValue.(model.ServiceKey)
		svc := svcValue.(model.ServiceKey)
		svcInstances := registry.GetInstances(&svc, false, true)
		if !svcInstances.IsInitialized() || len(svcInstances.GetInstances()) == 0 {
			continue
		}

		actualSvcInstances := svcInstances.(*pb.ServiceInstancesInProto)

		recoverAllChange := s.getAllRecoverRecords(actualSvcInstances)

		var cbHistory []*monitorpb.CircuitbreakHistory
		for _, inst := range actualSvcInstances.GetInstances() {
			instCb := actualSvcInstances.GetInstanceLocalValue(inst.GetId()).GetExtendedData(s.ID())
			var instCBChanges []*monitorpb.CircuitbreakChange
			instCbNode, _, _ := instCb.(*statusList).getNodes()
			for nil != instCbNode {
				changeData := &monitorpb.CircuitbreakChange{Time: &timestamp.Timestamp{
					Seconds: instCbNode.changeTime.Unix(),
					Nanos:   int32(instCbNode.changeTime.Nanosecond())},
					Change:    instCbNode.changeData.(*circuitBreakNode).status,
					Reason:    instCbNode.changeData.(*circuitBreakNode).reason,
					ChangeSeq: instCbNode.changeSeq}
				instCBChanges = append(instCBChanges, changeData)
				instCbNode = instCbNode.next
			}
			if len(instCBChanges) > 0 {
				cbHistory = append(cbHistory, &monitorpb.CircuitbreakHistory{Ip: inst.GetHost(),
					Port:    inst.GetPort(),
					Changes: instCBChanges})
			}
		}

		if len(recoverAllChange) == 0 && len(cbHistory) == 0 {
			continue
		}
		msg := &monitorpb.ServiceCircuitbreak{
			Id:                   uuid.New().String(),
			SdkToken:             util.GetPBSDkToken(sdkToken),
			Namespace:            svc.Namespace,
			Service:              svc.Service,
			RecoverAll:           recoverAllChange,
			InstanceCircuitbreak: cbHistory,
		}
		log.GetStatLogger().Infof("circuitbreak status record:%v", msg)
		if !s.uploadToMonitor {
			log.GetStatReportLogger().Warnf("Skip to report circuitbreak status to monitor"+
				" for connection problem, id: %s", msg.Id)
			return
		}
		err := s.circuitBreakClient.Send(msg)
		if nil != err {
			log.GetStatReportLogger().Errorf("fail to report circuitbreak status, id: %s, err %s，"+
				" monitor server is %s", msg.Id, err.Error(), s.connection.ConnID)
		}
		resp, err := s.circuitBreakClient.Recv()
		if nil != err || resp.Id.GetValue() != msg.Id || resp.Code.GetValue() != monitorpb.ReceiveSuccess {
			log.GetStatReportLogger().Errorf("fail to report circuitbreak status, resp is %v, err is %v,"+
				" monitor server is %s", resp, err, s.connection.ConnID)
		} else {
			log.GetStatReportLogger().Infof("Success to report circuitbreak status, resp is %v,"+
				" monitor server is %s", resp, s.connection.ConnID)
		}
	}
}

//获取当前周期该服务的所有全死全活变化
func (s *Reporter) getAllRecoverRecords(svcInst *pb.ServiceInstancesInProto) []*monitorpb.RecoverAllChange {
	var res []*monitorpb.RecoverAllChange
	svcLocalValue := svcInst.GetServiceLocalValue()
	serviceRecords := svcLocalValue.GetServiceDataByPluginId(s.ID()).(*serviceRecoverAllMap)
	recoverNodes, _, _ := serviceRecords.changeList.getNodes()
	for nil != recoverNodes {
		change := recoverNodes.changeData.(*recoverAllChange)
		res = append(res, &monitorpb.RecoverAllChange{
			Time: &timestamp.Timestamp{
				Seconds: recoverNodes.changeTime.Unix(),
				Nanos:   int32(recoverNodes.changeTime.Nanosecond()),
			},
			InstanceInfo: change.clusterInfo,
			Change:       change.statusChange,
			Reason:       change.reason,
		})
		recoverNodes = recoverNodes.next
	}
	return res
}
