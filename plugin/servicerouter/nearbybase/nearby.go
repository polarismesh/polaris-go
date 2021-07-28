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

package nearbybase

import (
	"context"
	"fmt"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/servicerouter"
	"strings"
	"time"
)

//RuleBasedInstancesFilter 基于路由规则的服务实例过滤器
type NearbyBasedInstancesFilter struct {
	*plugin.PluginBase
	wholeCfg              config.Configuration
	valueCtx              model.ValueContext
	percentOfMinInstances float64
	cfg                   *nearbyConfig
	recoverAll            bool
	matchLevel            int
	maxMatchLevel         int
	unHealthyRatio        float64
	locationReadyTimeout  time.Duration
}

//Type 插件类型
func (g *NearbyBasedInstancesFilter) Type() common.Type {
	return common.TypeServiceRouter
}

//Name 插件名，一个类型下插件名唯一
func (g *NearbyBasedInstancesFilter) Name() string {
	return config.DefaultServiceRouterNearbyBased
}

//Init 初始化插件
func (g *NearbyBasedInstancesFilter) Init(ctx *plugin.InitContext) error {
	// 获取最小返回实例比例
	g.percentOfMinInstances = ctx.Config.GetConsumer().GetServiceRouter().GetPercentOfMinInstances()
	g.PluginBase = plugin.NewPluginBase(ctx)
	g.recoverAll = ctx.Config.GetConsumer().GetServiceRouter().IsEnableRecoverAll()
	g.valueCtx = ctx.ValueCtx
	g.wholeCfg = ctx.Config

	cfgValue := ctx.Config.GetConsumer().GetServiceRouter().GetPluginConfig(g.Name())
	if cfgValue != nil {
		g.cfg = cfgValue.(*nearbyConfig)
	}
	g.matchLevel = nearbyLevels[g.cfg.MatchLevel]
	g.maxMatchLevel = nearbyLevels[g.cfg.MaxMatchLevel]
	g.unHealthyRatio = float64(g.cfg.UnhealthyPercentToDegrade) / 100
	g.locationReadyTimeout = (ctx.Config.GetGlobal().GetAPI().GetRetryInterval() +
		ctx.Config.GetGlobal().GetServerConnector().GetConnectTimeout()) *
		time.Duration(ctx.Config.GetGlobal().GetAPI().GetMaxRetryTimes()+1)
	ctx.Plugins.RegisterEventSubscriber(common.OnContextStarted,
		common.PluginEventHandler{Callback: g.waitLocationInfo})
	return nil
}

//Destroy 销毁插件，可用于释放资源
func (g *NearbyBasedInstancesFilter) Destroy() error {
	return nil
}

const (
	priorityLevelAll int = iota
	priorityLevelRegion
	priorityLevelZone
	priorityLevelCampus
)

//当前是否需要启动该服务路由插件
func (g *NearbyBasedInstancesFilter) Enable(routeInfo *servicerouter.RouteInfo, clusters model.ServiceClusters) bool {
	location := g.valueCtx.GetCurrentLocation().GetLocation()
	return nil != location && clusters.IsNearbyEnabled()
}

//一个匹配级别的cluster的健康和全部实例数量
type nearbyLevelInstanceCount struct {
	healthCount   int
	unHealthCount int
	allCount      int
}

//获取一个cluster的全部实例和健康实例个数
func getClusterInstanceCount(c *model.Cluster, clear bool, count *nearbyLevelInstanceCount) {
	if clear {
		c.ClearClusterValue()
	}
	count.allCount = c.GetClusterValue().GetInstancesSetWhenSkipRouteFilter(true, true).Count()
	count.healthCount = c.GetClusterValue().GetInstancesSet(false, false).Count()
	count.unHealthCount = count.allCount - count.healthCount
}

//获取从priorityLevelAll到priorityLevelCampus四个级别匹配的实例数量（全部实例和健康实例）
func (g *NearbyBasedInstancesFilter) checkAllLevelInstCounts(outCluster *model.Cluster, location *model.Location,
	allLevelsCount *[4]nearbyLevelInstanceCount) {
	//priorityLevelAll的实例数
	getClusterInstanceCount(outCluster, false, &allLevelsCount[priorityLevelAll])
	//priorityLevelRegion的实例数
	outCluster.Location.Region = location.Region
	getClusterInstanceCount(outCluster, true, &allLevelsCount[priorityLevelRegion])
	//priorityLevelZone的实例数
	outCluster.Location.Zone = location.Zone
	getClusterInstanceCount(outCluster, true, &allLevelsCount[priorityLevelZone])
	//priorityLevelCampus的实例数
	outCluster.Location.Campus = location.Campus
	getClusterInstanceCount(outCluster, true, &allLevelsCount[priorityLevelCampus])
}

//进行降级检测匹配
func (g *NearbyBasedInstancesFilter) modifyOutClusterLevel(outCluster *model.Cluster, finalLevel int) {
	if finalLevel != priorityLevelCampus {
		switch finalLevel {
		case priorityLevelZone:
			outCluster.Location.Campus = ""
		case priorityLevelRegion:
			outCluster.Location.Campus = ""
			outCluster.Location.Zone = ""
		case priorityLevelAll:
			outCluster.Location.Campus = ""
			outCluster.Location.Zone = ""
			outCluster.Location.Region = ""
		}
		outCluster.ClearClusterValue()
	}
	return
}

//检查某个level的实例数量是否满足要求，实例数量是否大于0
func (g *NearbyBasedInstancesFilter) checkLevelCount(allLevelsCount *[4]nearbyLevelInstanceCount,
	level int) (satisfied bool, notZero bool) {
	notZero = allLevelsCount[level].allCount > 0
	satisfied = notZero
	if *g.cfg.EnableDegradeByUnhealthyPercent {
		satisfied = notZero &&
			float64(allLevelsCount[level].unHealthCount)/float64(allLevelsCount[level].allCount) < g.unHealthyRatio
	}
	return satisfied, notZero
}

//将allLevelsCount转化为字符串
func allLevelsCountToString(allLevelsCount *[4]nearbyLevelInstanceCount) string {
	return fmt.Sprintf("location matched status：[ all Level:{health: %d, unhealth: %d},"+
		" region Level:{health: %d, unhealth: %d}, zone Level:{health: %d, unhealth: %d},"+
		" campus Level:{health: %d, unhealth: %d} ]", allLevelsCount[0].healthCount, allLevelsCount[0].unHealthCount,
		allLevelsCount[1].healthCount, allLevelsCount[1].unHealthCount,
		allLevelsCount[2].healthCount, allLevelsCount[2].unHealthCount,
		allLevelsCount[3].healthCount, allLevelsCount[3].unHealthCount)
}

//检查是否发生了地域降级
func checkNearbyStatus(matchLevel, finalLevel int) servicerouter.RouteStatus {
	if matchLevel == finalLevel {
		return servicerouter.Normal
	}
	switch finalLevel {
	case priorityLevelZone:
		return servicerouter.DegradeToCity
	case priorityLevelRegion:
		return servicerouter.DegradeToRegion
	case priorityLevelAll:
		return servicerouter.DegradeToAll
	}
	return servicerouter.Normal
}

func (g *NearbyBasedInstancesFilter) GetLevel(clusters model.ServiceClusters) (int, int) {
	matchLevel := g.matchLevel
	maxMatchLevel := g.maxMatchLevel

	namespace := clusters.GetServiceKey().Namespace
	service := clusters.GetServiceKey().Service

	if namespace == config.ServerNamespace && !strings.Contains(service, "polaris.metric") {
		matchLevel = priorityLevelRegion
		maxMatchLevel = priorityLevelAll
	} else {
		serviceSp := g.wholeCfg.GetConsumer().GetServiceSpecific(namespace, service)
		if serviceSp != nil {
			nearbySp := serviceSp.GetServiceRouter().GetNearbyConfig()
			if nearbySp.GetMatchLevel() != "" {
				matchLevel = nearbyLevels[nearbySp.GetMatchLevel()]
			}
			if nearbySp.GetMaxMatchLevel() != "" {
				maxMatchLevelTmp := nearbyLevels[nearbySp.GetMaxMatchLevel()]
				if maxMatchLevelTmp <= matchLevel {
					g.maxMatchLevel = maxMatchLevelTmp
				} else {
					log.GetBaseLogger().Warnf("%s %s nearbyConfig maxMatchLevel > matchLevel", namespace, service)
				}
			}
		}
	}
	return matchLevel, maxMatchLevel
}

//GetFilteredInstances 进行服务实例过滤，并返回过滤后的实例列表
func (g *NearbyBasedInstancesFilter) GetFilteredInstances(rInfo *servicerouter.RouteInfo,
	clusters model.ServiceClusters, withinCluster *model.Cluster) (*servicerouter.RouteResult, error) {
	//记录各个匹配级别的实例数量
	var allLevelsCount [4]nearbyLevelInstanceCount
	var outCluster *model.Cluster
	//var enableNearby bool
	var setNearbyCluster = true
	location := g.valueCtx.GetCurrentLocation().GetLocation()
	var finalLevel, notZeroLevel int
	matchLevel, maxMatchLevel := g.GetLevel(clusters)

	if len(withinCluster.ComposeMetaValue) == 0 {
		var nearCluster *model.Cluster
		//假如是全量服务，则尝试直接获取缓存
		nearCluster, finalLevel = clusters.GetNearbyCluster(*location)
		if nil != nearCluster {
			outCluster = nearCluster
			setNearbyCluster = false
			goto finally
		}
	}
	//enableNearby = clusters.IsNearbyEnabled()
	//if !enableNearby {
	//	result, err := filteronly.GetFilteredInstances(
	//		g.valueCtx, rInfo, clusters, g.percentOfMinInstances, withinCluster, g.recoverAll)
	//	if nil != err {
	//		return nil, err
	//	}
	//	if len(withinCluster.ComposeMetaValue) == 0 {
	//		clusters.SetNearbyCluster(*location, result.OutputCluster, priorityLevelAll)
	//	}
	//	return result, nil
	//}

	outCluster = model.NewCluster(clusters, withinCluster)
	g.checkAllLevelInstCounts(outCluster, location, &allLevelsCount)
	//如果priorityLevelAll级别的实例数量为0，说明没有实例，直接报错
	if allLevelsCount[priorityLevelAll].allCount == 0 {
		outCluster.MissLocationInstances = true
		outCluster.LocationMatchInfo = allLevelsCountToString(&allLevelsCount)
		goto finally
	}
	finalLevel = -1
	notZeroLevel = -1
	for l := matchLevel; l >= maxMatchLevel; l-- {
		satisfied, notZero := g.checkLevelCount(&allLevelsCount, l)
		if notZero && notZeroLevel == -1 {
			notZeroLevel = l
		}
		if satisfied {
			finalLevel = l
			break
		}
	}
	//如果没有满足条件的finalLevel，那么将finalLevel设置为最高的实例数大于0的级别notZeroLevel
	if finalLevel < priorityLevelAll {
		finalLevel = notZeroLevel
	}
	if finalLevel < priorityLevelAll {
		outCluster.MissLocationInstances = true
		outCluster.LocationMatchInfo = allLevelsCountToString(&allLevelsCount)
		goto finally
	}

	//如果进行降级，修改outcluster的地域信息以对齐最终匹配级别，否则直接使用已经匹配到的实例
	g.modifyOutClusterLevel(outCluster, finalLevel)

finally:
	if len(withinCluster.ComposeMetaValue) == 0 && setNearbyCluster {
		clusters.SetNearbyCluster(*location, outCluster, finalLevel)
	}
	//根据是否开启recoverall，决定是否要进行兜底过滤
	rInfo.SetIgnoreFilterOnlyOnEndChain(!g.recoverAll)
	if outCluster.MissLocationInstances {
		return nil, g.misMatchError(location, outCluster)
	}
	result := servicerouter.PoolGetRouteResult(g.valueCtx)
	result.OutputCluster = outCluster
	result.Status = checkNearbyStatus(matchLevel, finalLevel)
	return result, nil
}

//返回地域匹配错误
func (g *NearbyBasedInstancesFilter) misMatchError(location *model.Location, outCluster *model.Cluster) model.SDKError {
	maxLevel := g.cfg.MaxMatchLevel
	if maxLevel == "" {
		maxLevel = "all"
	}
	if *g.cfg.EnableDegradeByUnhealthyPercent {
		return model.NewSDKError(model.ErrCodeLocationMismatch, nil,
			"no instance of %s in nearby level between %s and %s, client location is %s, %s",
			outCluster.GetClusters().GetServiceKey(), maxLevel, g.cfg.MatchLevel, location, outCluster.LocationMatchInfo)
	}
	return model.NewSDKError(model.ErrCodeLocationMismatch, nil,
		"no instance of %s in nearby matchLevel %s, client location is %s, %s",
		outCluster.GetClusters().GetServiceKey(), g.cfg.MatchLevel, location, outCluster.LocationMatchInfo)
}

//等待地域信息就绪
func (g *NearbyBasedInstancesFilter) waitLocationInfo(event *common.PluginEvent) error {
	if !g.cfg.StrictNearby {
		g.valueCtx.WaitLocationInfo(nil, model.LocationInit)
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), g.locationReadyTimeout)
	defer cancel()
	ready := g.valueCtx.WaitLocationInfo(ctx, model.LocationReady)
	if !ready {
		return fmt.Errorf("fail to get location ready, timeout is %v, location err: %v",
			g.locationReadyTimeout, g.valueCtx.GetCurrentLocation().GetLastError())
	}
	return nil
}

//init 注册插件
func init() {
	plugin.RegisterConfigurablePlugin(&NearbyBasedInstancesFilter{}, &nearbyConfig{})
}
