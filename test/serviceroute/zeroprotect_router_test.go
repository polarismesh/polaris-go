// Package test mock test for set division
/*
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
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/polarismesh/specification/source/go/api/v1/service_manage"
	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/local"
	"github.com/polarismesh/polaris-go/pkg/model/pb"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/servicerouter"
	"github.com/polarismesh/polaris-go/plugin/servicerouter/zeroprotect"
)

func genMockInstance(svcName, namespace string, count int64) []model.Instance {
	ret := make([]model.Instance, 0, count)
	for i := 1; i <= int(count); i++ {
		protoIns := &service_manage.Instance{
			Id:        wrapperspb.String(uuid.NewString()),
			Service:   wrapperspb.String(svcName),
			Namespace: wrapperspb.String(namespace),
			Host:      wrapperspb.String(fmt.Sprintf("%d.%d.%d.%d", i, i, i, i)),
			Port:      wrapperspb.UInt32(uint32(i*1000 + i*100 + i*10 + i)),
			Weight:    wrapperspb.UInt32(100),
			Metadata:  map[string]string{},
		}
		ins := pb.NewInstanceInProto(protoIns, &model.ServiceKey{
			Namespace: namespace,
			Service:   svcName,
		}, local.NewInstanceLocalValue())
		ret = append(ret, ins)
	}

	return ret
}

func Test_ZeroProtectRouter(t *testing.T) {
	svcName := "mock_service"
	nsName := "mock_namespace"

	t.Run("存在健康实例，不会触发 ZeroProtect", func(t *testing.T) {
		insCount := 10
		instances := genMockInstance(svcName, nsName, int64(insCount))
		healthCount := 0
		for i := range instances {
			ins := instances[i]
			ins.SetHealthy(i%2 == 0)
			if !instances[i].IsHealthy() {
				healthChangeTimeSec := time.Now().Add(-10 * time.Second).Unix()
				ins.GetMetadata()[zeroprotect.MetadataInstanceLastHeartbeatTime] = fmt.Sprintf("%d", healthChangeTimeSec)
			} else {
				healthCount++
			}
		}

		defaultConf := config.NewDefaultConfigurationWithDomain()

		router := &zeroprotect.ZeroProtectFilter{}
		router.Init(&plugin.InitContext{
			Config:       defaultConf,
			ValueCtx:     model.NewValueContext(),
			PluginIndex:  1,
			SDKContextID: uuid.NewString(),
		})

		mockCluster := model.NewServiceClusters(model.NewDefaultServiceInstances(model.ServiceInfo{
			Service:   svcName,
			Namespace: nsName,
			Metadata:  map[string]string{},
		}, instances))

		result, err := router.GetFilteredInstances(&servicerouter.RouteInfo{}, mockCluster, model.NewCluster(mockCluster, nil))
		assert.NoError(t, err)
		instanceSet := result.OutputCluster.GetClusterValue().GetInstancesSet(false, false)
		assert.Equal(t, int64(healthCount), int64(instanceSet.Count()))
	})

	t.Run("不存在健康实例，ZeroProtect只恢复满足要求的实例", func(t *testing.T) {
		insCount := 10
		instances := genMockInstance(svcName, nsName, int64(insCount))
		mockTtl := 5
		lastHeartbeatTimeSec := time.Now()
		zeroProtectInsCount := 0
		for i := range instances {
			instances[i].SetHealthy(false)
			ins := instances[i].(*pb.InstanceInProto)
			ins.HealthCheck = &service_manage.HealthCheck{
				Type: service_manage.HealthCheck_HEARTBEAT,
				Heartbeat: &service_manage.HeartbeatHealthCheck{
					Ttl: wrapperspb.UInt32(uint32(mockTtl)),
				},
			}
			diff := -2 * i
			healthChangeTimeSec := lastHeartbeatTimeSec.Add(time.Duration(diff) * time.Second).Unix()

			if zeroprotect.NeedZeroProtect(lastHeartbeatTimeSec.Unix(), healthChangeTimeSec, int64(mockTtl)) {
				zeroProtectInsCount++
			}

			instances[i].GetMetadata()[zeroprotect.MetadataInstanceLastHeartbeatTime] = fmt.Sprintf("%d", healthChangeTimeSec)
		}

		assert.True(t, zeroProtectInsCount > 0)

		defaultConf := config.NewDefaultConfigurationWithDomain()

		router := &zeroprotect.ZeroProtectFilter{}
		router.Init(&plugin.InitContext{
			Config:       defaultConf,
			ValueCtx:     model.NewValueContext(),
			PluginIndex:  1,
			SDKContextID: uuid.NewString(),
		})

		mockCluster := model.NewServiceClusters(model.NewDefaultServiceInstances(model.ServiceInfo{
			Service:   svcName,
			Namespace: nsName,
			Metadata:  map[string]string{},
		}, instances))

		result, err := router.GetFilteredInstances(&servicerouter.RouteInfo{}, mockCluster, model.NewCluster(mockCluster, nil))
		assert.NoError(t, err)
		instanceSet := result.OutputCluster.GetClusterValue().GetInstancesSet(false, false)
		assert.Equal(t, int64(zeroProtectInsCount), int64(instanceSet.Count()))
	})
}
