/**
 * Tencent is pleased to support the open source community by making CL5 available.
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

package config

var (
	builtInServers = map[string][]uint32{
		JoinPointMainland: {160252635, 160252270, 160252189, 160266503, 160614481, 160614947, 160615871,
			160614461, 160252660, 160614939},
		JoinPointSingapore: {164645443, 164645472},
		JoinPointOA:        {159910894, 160874741, 160878872},
		JoinPointUSA:       {164684321, 164684311},
	}

	joinPointsBuiltInServers = map[string][]uint32{
		JoinPointMainland:      builtInServers[JoinPointMainland],
		JoinPointTcloudFinance: builtInServers[JoinPointMainland],
		JoinPointPrivatePay:    builtInServers[JoinPointMainland],
		JoinPointPrivatePcg:    builtInServers[JoinPointMainland],
		JoinPointSingapore:     builtInServers[JoinPointSingapore],
		JoinPointOA:            builtInServers[JoinPointOA],
		JoinPointUSA:           builtInServers[JoinPointUSA],
	}

	clusterPolarisServers = map[string]map[ClusterType]string{
		JoinPointMainland: {
			DiscoverCluster:    ServerDiscoverService,
			HealthCheckCluster: ServerHeartBeatService,
			MonitorCluster:     ServerMonitorService,
			//MetricCluster: ServerMetricService,
		},
		JoinPointTcloudFinance: {
			DiscoverCluster:    "polaris.discover.finance",
			HealthCheckCluster: "polaris.healthcheck.finance",
			MonitorCluster:     "polaris.monitor.finance",
			//MetricCluster: "polaris.metric.finance",
		},
		JoinPointPrivatePay: {
			DiscoverCluster:    "polaris.discover.pay",
			HealthCheckCluster: "polaris.healthcheck.pay",
			MonitorCluster:     "polaris.monitor.pay",
			//MetricCluster: "polaris.metric.pay",
		},
		JoinPointPrivatePcg: {
			DiscoverCluster:    "polaris.discover.pcg",
			HealthCheckCluster: "polaris.healthcheck.pcg",
			MonitorCluster:     "polaris.monitor.pcg",
			//MetricCluster: "polaris.metric.pcg",
		},
		JoinPointSingapore: {
			DiscoverCluster:    ServerDiscoverService,
			HealthCheckCluster: ServerHeartBeatService,
			MonitorCluster:     ServerMonitorService,
			//MetricCluster: ServerMetricService,
		},
		JoinPointOA: {
			DiscoverCluster:    "polaris.discover.oa",
			HealthCheckCluster: "polaris.healthcheck.oa",
			MonitorCluster:     "polaris.monitor.oa",
			//MetricCluster: ServerMetricService,
		},
		JoinPointUSA: {
			DiscoverCluster:    "polaris.discover.usa",
			HealthCheckCluster: "polaris.healthcheck.usa",
			MonitorCluster:     ServerMonitorService,
		},
	}
)
