// Tencent is pleased to support the open source community by making polaris-go available.
//
// Copyright (C) 2019 THL A29 Limited, a Tencent company. All rights reserved.
//
// Licensed under the BSD 3-Clause License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// https://opensource.org/licenses/BSD-3-Clause
//
// Unless required by applicable law or agreed to in writing, software distributed
// under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
// CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissionsr and limitations under the License.
//

package prometheus

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/push"
)

const (
	PushDefaultInterval = time.Duration(30) * time.Second
	PushDefaultAddress  = "127.0.0.1:9091"
	PushDefaultJobName  = "defaultJobName"
	PushGroupKey        = "instance"
	RevisionMaxScope    = 2
)

// PushAddressProvider Provides the service address of PushGateway
type PushAddressProvider interface {
	GetAddress() (string, error)
}

func NewProvider(serviceInfo config.ServerClusterConfig, engine model.Engine) PushAddressProvider {
	return &ServiceDiscoveryProvider{
		serviceInfo: serviceInfo,
		engine:      engine,
	}
}

type ServiceDiscoveryProvider struct {
	serviceInfo config.ServerClusterConfig
	engine      model.Engine
}

func (provider *ServiceDiscoveryProvider) GetAddress() (string, error) {
	engine := provider.engine
	svr := provider.serviceInfo
	resp, err := engine.SyncGetOneInstance(&model.GetOneInstanceRequest{
		Service:   svr.GetService(),
		Namespace: svr.GetNamespace(),
	})
	if err != nil {
		return "", err
	}

	instances := resp.Instances
	if len(instances) < 1 {
		return "", model.NewSDKError(model.ErrCodeAPIInstanceNotFound, nil, "instance of %s:%s not found", svr.GetNamespace(), svr.GetService())
	}

	return fmt.Sprintf("%s:%d", instances[0].GetHost(), instances[0].GetPort()), nil
}

type PrometheusHandler struct {
	callerIp      string
	jobName       string
	instanceName  string
	pushInterval  time.Duration
	provider      PushAddressProvider
	registry      *prometheus.Registry
	container     *StatInfoCollectorContainer
	sampleMapping map[string]*prometheus.GaugeVec
}

func NewHandler(callerIp string, provider PushAddressProvider, cfg Config) *PrometheusHandler {
	registry := prometheus.NewRegistry()

	jobName := PushDefaultJobName
	if strings.Compare(cfg.JobName, "") != 0 {
		jobName = cfg.JobName
	}

	pushInterval := PushDefaultInterval
	if cfg.PushInterval != nil {
		pushInterval = *cfg.PushInterval
	}

	instanceName := callerIp
	if strings.Compare(cfg.InstanceName, "") != 0 {
		instanceName = cfg.InstanceName
	}

	handler := &PrometheusHandler{
		callerIp:     callerIp,
		provider:     provider,
		registry:     registry,
		jobName:      jobName,
		pushInterval: pushInterval,
		instanceName: instanceName,
	}

	handler.initSampleMapping(nil, nil)

	return handler
}

// handleStat
func (s *PrometheusHandler) handleStat(statInfo *model.StatInfo) error {

	return nil
}

// doPush
func (s *PrometheusHandler) RunPush(ctx context.Context) {
	t := time.NewTicker(s.pushInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			log.GetBaseLogger().Infof("doPush of statReporter stat_monitor has been terminated")
			return
		case <-t.C:
			//如果registry没有获取到的话，等待下一次
			registry := s.registry
			if nil == registry {
				log.GetBaseLogger().Warnf("registry not ready, wait for next period")
				continue
			}
			log.GetStatReportLogger().Infof("start to upload stat info to monitor")
			log.GetStatReportLogger().Infof("finish to upload stat info to monitor")
		}
	}
}

// initSampleMapping
func (s *PrometheusHandler) initSampleMapping(strategies []MetricValueAggregationStrategy, order []string) {
	for _, strategy := range strategies {
		gauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: strategy.StrategyName(),
			Help: strategy.StrategyDescription(),
		}, order)

		s.registry.Register(gauge)
		s.sampleMapping[strategy.StrategyName()] = gauge
	}
}

func (s *PrometheusHandler) newPushGateway(address string) (*push.Pusher, error) {
	address, err := s.provider.GetAddress()
	if err != nil {
		return nil, err
	}

	pusher := push.New(fmt.Sprintf("http://%s", address), s.jobName).Gatherer(s.registry)
	return pusher, nil
}
