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

package model

type DefaultServiceInstances struct {
	service ServiceInfo

	instances []Instance

	instanceMap map[string]Instance

	totalWeight int

	clusterCache ServiceClusters
}

func NewDefaultServiceInstances(service ServiceInfo, instances []Instance) ServiceInstances {
	instanceMap := make(map[string]Instance, len(instances))
	var totalWeight int
	for _, instance := range instances {
		instanceMap[instance.GetId()] = instance
		totalWeight += instance.GetWeight()
	}
	svcInstances := &DefaultServiceInstances{
		service:     service,
		instances:   instances,
		instanceMap: instanceMap,
		totalWeight: totalWeight,
	}
	clusterCache := NewServiceClusters(svcInstances)
	svcInstances.clusterCache = clusterCache
	if len(instances) > 0 {
		for _, inst := range instances {
			clusterCache.AddInstance(inst)
		}
	}
	return svcInstances
}

// 获取服务名
func (d *DefaultServiceInstances) GetService() string {
	return d.service.GetService()
}

// 获取命名空间
func (d *DefaultServiceInstances) GetNamespace() string {
	return d.service.GetNamespace()
}

// 获取元数据信息
func (d *DefaultServiceInstances) GetMetadata() map[string]string {
	return d.service.GetMetadata()
}

// 获取配置类型
func (d *DefaultServiceInstances) GetType() EventType {
	return EventInstances
}

// 是否初始化，实例列表或路由值是否加载
func (d *DefaultServiceInstances) IsInitialized() bool {
	return true
}

// 获取服务实例或规则的版本号
func (d *DefaultServiceInstances) GetRevision() string {
	return ""
}

// 获取服务实例或规则的Hash值
func (d *DefaultServiceInstances) GetHashValue() uint64 {
	return 0
}

// 获取服务实例列表
func (d *DefaultServiceInstances) GetInstances() []Instance {
	return d.instances
}

func (d *DefaultServiceInstances) IsNotExists() bool {
	return false
}

// 获取全部实例总权重
func (d *DefaultServiceInstances) GetTotalWeight() int {
	return d.totalWeight
}

// 获取集群索引
func (d *DefaultServiceInstances) GetServiceClusters() ServiceClusters {
	return d.clusterCache
}

// 重建缓存索引
func (d *DefaultServiceInstances) ReloadServiceClusters() {

}

// 获取单个服务实例
func (d *DefaultServiceInstances) GetInstance(id string) Instance {
	return d.instanceMap[id]
}

// 数据是否来自于缓存文件
func (d *DefaultServiceInstances) IsCacheLoaded() bool {
	return false
}
