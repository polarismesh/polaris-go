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

package serviceroute

import (
	"context"
	"github.com/polarismesh/polaris-go/pkg/clock"
	sysconfig "github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/local"
	"github.com/polarismesh/polaris-go/pkg/model/pb"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/servicerouter"
	"github.com/polarismesh/polaris-go/plugin/statreporter/basereporter"
	"github.com/polarismesh/polaris-go/plugin/statreporter/pb/util"
	monitorpb "github.com/polarismesh/polaris-go/plugin/statreporter/pb/v1"
	"github.com/golang/protobuf/ptypes/timestamp"
	"github.com/google/uuid"
	"time"
)

type Reporter struct {
	*basereporter.Reporter
	cfg      *Config
	statData *routeStatData
}

//Type 插件类型
func (s *Reporter) Type() common.Type {
	return common.TypeStatReporter
}

//Name 插件名，一个类型下插件名唯一
func (s *Reporter) Name() string {
	return "serviceRoute"
}

//创建一个clientStream的方法
func (s *Reporter) createRouteReportStream(conn monitorpb.GrpcAPIClient) (client basereporter.CloseAbleStream,
	cancelFunc context.CancelFunc, err error) {
	var ctx context.Context
	ctx, cancelFunc = context.WithDeadline(context.Background(), clock.GetClock().Now().Add(*s.cfg.ReportInterval))
	client, err = conn.CollectRouteRecord(ctx)
	return
}

//初始化插件
func (s *Reporter) Init(ctx *plugin.InitContext) error {
	ctx.Plugins.RegisterEventSubscriber(common.OnServiceLocalValueCreated, common.PluginEventHandler{
		Callback: s.generateStatData,
	})
	cfgValue := ctx.Config.GetGlobal().GetStatReporter().GetPluginConfig(s.Name())
	if cfgValue == nil {
		return model.NewSDKError(model.ErrCodeAPIInvalidConfig, nil, "config of statReporter serviceRoute must be provided")
	}
	s.cfg = cfgValue.(*Config)
	s.cfg.SetDefault()
	var err error
	err = s.cfg.Verify()
	if err != nil {
		return model.NewSDKError(model.ErrCodeAPIInvalidConfig, err, "invalid config of statReporter serviceRoute")
	}

	s.Reporter, err = basereporter.InitBaseReporter(ctx, []basereporter.CreateClientStreamFunc{s.createRouteReportStream})
	if err != nil {
		return err
	}

	return nil
}

// 启动上报协程
func (s *Reporter) Start() error {
	go s.uploadRouteRecord()
	return nil
}

//定时上报服务的路由记录到monitor
func (g *Reporter) uploadRouteRecord() {
	ticker := time.NewTicker(*g.cfg.ReportInterval)
	defer ticker.Stop()
	for {
		select {
		case <-g.Done():
			log.GetBaseLogger().Infof("%s, uploadRouteRecord of statReporter serviceRoute terminated", g.GetSDKContextID())
			return
		case <-ticker.C:
			log.GetStatReportLogger().Infof("start to upload service route record to monitor")
			err := g.CreateStreamWithIndex(0)
			skipMonitor := false
			if err != nil {
				skipMonitor = true
				log.GetStatReportLogger().Errorf("fail to connect monitor, err: %v, skip upload record", err)
			}
			services := g.Registry.GetServices()
			for svc, _ := range services {
				svcKey := svc.(model.ServiceKey)
				svcInstances := g.Registry.GetInstances(&svcKey, false, true)
				if !svcInstances.IsInitialized() {
					continue
				}
				actualSvcInstances := svcInstances.(*pb.ServiceInstancesInProto)
				localValue := actualSvcInstances.GetServiceLocalValue()
				data := localValue.GetServiceDataByPluginId(g.ID())
				g.constructRecordAndSend(svcInstances.GetNamespace(), svcInstances.GetService(),
					data.(*routeStatData).getRouteRecord(), skipMonitor)
			}
			if !skipMonitor {
				g.DestroyStreamWithIndex(0)
			}
			log.GetStatReportLogger().Infof("end upload service route record to monitor")
		}
	}
}

var ruleTypeMap = map[servicerouter.RuleType]monitorpb.RouteRecord_RuleType{
	servicerouter.UnknownRule: monitorpb.RouteRecord_Unknown,
	servicerouter.SrcRule:     monitorpb.RouteRecord_SrcRule,
	servicerouter.DestRule:    monitorpb.RouteRecord_DestRule,
}

//根据数据构造记录并发送到monitor
func (s *Reporter) constructRecordAndSend(namespace string, service string, data map[ruleKey]map[resultKey]uint32,
	skipMonitor bool) {
	if len(data) == 0 {
		return
	}
	now := clock.GetClock().Now()
	totalRecord := &monitorpb.ServiceRouteRecord{
		Id:        uuid.New().String(),
		SdkToken:  util.GetPBSDkToken(s.SDKToken),
		Namespace: namespace,
		Service:   service,
		Time: &timestamp.Timestamp{
			Seconds: now.Unix(),
			Nanos:   int32(now.Nanosecond()),
		},
	}
	if totalRecord.SdkToken.Ip == "" {
		totalRecord.SdkToken.Ip = s.GetIPString()
	}
	stream := s.GetClientStream(0)
	for k1, v1 := range data {
		routePlugin, _ := s.Plugins.GetPluginById(k1.plugId)
		record := &monitorpb.RouteRecord{
			PluginName:   routePlugin.Name(),
			SrcNamespace: k1.srcService.Namespace,
			SrcService:   k1.srcService.Service,
			RuleType:     ruleTypeMap[k1.routeRuleType],
		}
		for k2, v2 := range v1 {
			record.Results = append(record.Results, &monitorpb.RouteResult{
				RetCode:     model.ErrCodeToString(k2.errCode),
				Cluster:     k2.clusterKey.ComposeMetaValue + "-" + k2.clusterKey.Location.String(),
				RouteStatus: k2.status.String(),
				PeriodTimes: v2,
			})
		}
		totalRecord.Records = append(totalRecord.Records, record)
	}
	log.GetStatLogger().Infof("sdk route record: %v", totalRecord)
	if !skipMonitor {
		clientStream := stream.(monitorpb.GrpcAPI_CollectRouteRecordClient)
		err := clientStream.Send(totalRecord)
		if err != nil {
			log.GetStatReportLogger().Errorf("fail to send route record of id: %s, err %v", totalRecord.GetId(), err)
			return
		}
		resp, err := clientStream.Recv()
		if err != nil {
			log.GetStatReportLogger().Errorf("fail to receive response for route record of id %v, err %v",
				totalRecord.GetId(), err)
			return
		}
		if nil != err || resp.Id.GetValue() != totalRecord.Id || resp.Code.GetValue() != monitorpb.ReceiveSuccess {
			log.GetStatReportLogger().Errorf("fail to report route record, resp is %v, err is %v, monitor server is %s",
				resp, err, s.GetClientStreamServer(0))
		} else {
			log.GetStatReportLogger().Infof("Success to report route record, resp is %v, monitor server is %s",
				resp, s.GetClientStreamServer(0))
		}
	}
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

// destroy
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

//ReportStat 上报统计信息
func (s *Reporter) ReportStat(t model.MetricType, info model.InstanceGauge) error {
	if t != model.RouteStat {
		return nil
	}
	gauge := info.(*servicerouter.RouteGauge)
	pbInstances, ok := gauge.ServiceInstances.(*pb.ServiceInstancesInProto)
	if !ok {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil,
			"ServiceInstances of route record gauge is not expected type")
	}
	localValue := pbInstances.GetServiceLocalValue()
	data := localValue.GetServiceDataByPluginId(s.ID())
	//if data == nil {
	//	return nil
	//}
	data.(*routeStatData).putNewStat(gauge)
	return nil
}

//为服务创建路由调用统计信息的存储数据
func (s *Reporter) generateStatData(event *common.PluginEvent) error {
	lv := event.EventObject.(local.ServiceLocalValue)
	stat := &routeStatData{}
	stat.init()
	lv.SetServiceDataByPluginId(s.ID(), stat)
	return nil
}

func init() {
	plugin.RegisterConfigurablePlugin(&Reporter{}, &Config{})
}
