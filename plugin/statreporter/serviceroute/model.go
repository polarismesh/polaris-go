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
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/servicerouter"
	"github.com/modern-go/reflect2"
	"sync"
	"sync/atomic"
)

//路由统计数据的key
type routeStatKey struct {
	plugId        int32
	retCode       model.ErrCode
	clusterKey    model.ClusterKey
	routeRuleType servicerouter.RuleType
	srcService    model.ServiceKey
	status        servicerouter.RouteStatus
}

//路由规则的key，标识一种路由规则
type ruleKey struct {
	plugId        int32
	srcService    model.ServiceKey
	routeRuleType servicerouter.RuleType
}

//路由结果的key，标识一种路由结果
type resultKey struct {
	errCode    model.ErrCode
	status     servicerouter.RouteStatus
	clusterKey model.ClusterKey
}

//存储服务路由数据的结构
type routeStatData struct {
	currentStore atomic.Value
	preStore     atomic.Value
	store        *sync.Map
}

//初始化routeStatData
func (r *routeStatData) init() {
	r.currentStore.Store(&sync.Map{})
}

//添加一个服务路由的统计数据
func (r *routeStatData) putNewStat(gauge *servicerouter.RouteGauge) {
	rule := ruleKey{
		plugId:        gauge.PluginID,
		srcService:    gauge.SrcService,
		routeRuleType: gauge.RouteRuleType,
	}
	result := resultKey{
		errCode: gauge.RetCode,
		status:  gauge.Status,
	}
	if gauge.Cluster != nil {
		result.clusterKey = gauge.Cluster.ClusterKey
	}
	currentData := r.currentStore.Load().(*sync.Map)
	dataInf, ok := currentData.Load(rule)
	if !ok {
		//var value uint32
		dataInf, _ = currentData.LoadOrStore(rule, &sync.Map{})
	}
	dataMap := dataInf.(*sync.Map)
	valueInf, ok := dataMap.Load(result)
	if !ok {
		var value uint32
		valueInf, _ = dataMap.LoadOrStore(result, &value)
	}
	value := valueInf.(*uint32)
	atomic.AddUint32(value, 1)
}

//获取一个服务下面的所有路由规则的统计数据
func (r *routeStatData) getRouteRecord() map[ruleKey]map[resultKey]uint32 {
	var currentMap, preMap map[ruleKey]map[resultKey]uint32
	currentSyncMap := r.currentStore.Load().(*sync.Map)
	r.currentStore.Store(&sync.Map{})
	currentMap = getRouteRecordFromMap(currentSyncMap)
	preInf := r.preStore.Load()
	if !reflect2.IsNil(preInf) {
		preMap = getRouteRecordFromMap(preInf.(*sync.Map))
	}
	r.preStore.Store(currentSyncMap)

	combineTwoRuleMaps(currentMap, preMap)
	return currentMap
}

//将两个map[ruleKey]map[resultKey]uint32进行结合
func combineTwoRuleMaps(currentMap, preMap map[ruleKey]map[resultKey]uint32) {
	for pk, pv := range preMap {
		if cv, ok := currentMap[pk]; !ok {
			currentMap[pk] = pv
		} else {
			for pvk, pvv := range pv {
				if pvv > 0 {
					cvv := cv[pvk]
					cv[pvk] = pvv + cvv
				}
			}
		}
	}
}

//从一个syncMap中获取一个map[ruleKey]map[resultKey]uint32
func getRouteRecordFromMap(m *sync.Map) map[ruleKey]map[resultKey]uint32 {
	ruleResultMap := make(map[ruleKey]map[resultKey]uint32)
	m.Range(func(k, v interface{}) bool {
		resultSyncMap := v.(*sync.Map)
		key := k.(ruleKey)
		var resultMap map[resultKey]uint32
		var ok bool
		if resultMap, ok = ruleResultMap[key]; !ok {
			resultMap = make(map[resultKey]uint32)
			//ruleResultMap[key] = resultMap
		}
		resultSyncMap.Range(func(rk, rv interface{}) bool {
			value := rv.(*uint32)
			num := atomic.LoadUint32(value)
			if num > 0 {
				resultMap[rk.(resultKey)] = num
				atomic.AddUint32(value, ^(num - 1))
			}
			return true
		})
		if len(resultMap) > 0 {
			ruleResultMap[key] = resultMap
		}
		return true
	})
	return ruleResultMap
}
