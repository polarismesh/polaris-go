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

package loadbalancer

import "github.com/polarismesh/polaris-go/pkg/model"

//执行负载均衡
func ChooseInstance(ctx model.ValueContext, loadbalancer LoadBalancer,
	criteria *Criteria, instances model.ServiceInstances) (model.Instance, model.SDKError) {
	var sdkErr model.SDKError
	cluster := criteria.Cluster
	if nil == cluster {
		svcKey := model.ServiceKey{Namespace: instances.GetNamespace(), Service: instances.GetService()}
		return nil, model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil,
			"cluster is nil for loadBalance for %s", svcKey)
	}
	instance, err := loadbalancer.ChooseInstance(criteria, cluster.GetClusters().GetServiceInstances())
	if nil != err {
		sdkErr = err.(model.SDKError)
	}
	if nil != criteria.Cluster {
		criteria.Cluster.PoolPut()
		criteria.Cluster = nil
	}
	return instance, sdkErr
}
