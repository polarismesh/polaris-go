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
	"fmt"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
	monitorpb "github.com/polarismesh/polaris-go/plugin/statreporter/pb/v1"
	"github.com/polarismesh/polaris-go/plugin/statreporter/serviceroute"
	"gopkg.in/check.v1"
	"log"
	"time"
)

//路由调用记录的key
type routerKey struct {
	Namespace     string
	Service       string
	Plugin        string
	SrcNamespace  string
	SrcService    string
	RouteRuleType monitorpb.RouteRecord_RuleType
}

type recordKey struct {
	RouteStatus string
	RetCode     string
}

//将monitor的记录转化成map
func monitorDataToMap(monitorData []*monitorpb.ServiceRouteRecord) map[routerKey]map[recordKey]uint32 {
	res := make(map[routerKey]map[recordKey]uint32)
	templateKey := routerKey{}
	for _, md := range monitorData {
		templateKey.Namespace = md.Namespace
		templateKey.Service = md.Service
		for _, rec := range md.GetRecords() {
			templateKey.Plugin = rec.GetPluginName()
			templateKey.SrcService = rec.GetSrcService()
			templateKey.SrcNamespace = rec.GetSrcNamespace()
			templateKey.RouteRuleType = rec.GetRuleType()
			tmap, ok := res[templateKey]
			if !ok {
				tmap = make(map[recordKey]uint32)
				res[templateKey] = tmap
			}
			for _, rRes := range rec.GetResults() {
				rKey := recordKey{
					RouteStatus: rRes.GetRouteStatus(),
					RetCode:     rRes.GetRetCode(),
				}
				num, ok := tmap[rKey]
				if !ok {
					tmap[rKey] = rRes.GetPeriodTimes()
				} else {
					tmap[rKey] = num + rRes.GetPeriodTimes()
				}
			}
		}
	}
	for k, v := range res {
		fmt.Printf("k: %+v, v: %v\n", k, v)
	}
	return res
}

//检测monitor收到的数据是不是期望的
func checkRouteRecord(monitorData map[routerKey]map[recordKey]uint32, checkData map[routerKey]map[recordKey]uint32, c *check.C) {
	for k, v := range checkData {
		mdata, ok := monitorData[k]
		if !ok {
			log.Printf("not contain key: %+v", k)
		}
		c.Assert(ok, check.Equals, true)
		for k1, v1 := range v {
			cdata, ok := mdata[k1]
			if !ok {
				log.Printf("not contain inner key: %+v", k1)
			}
			c.Assert(ok, check.Equals, true)
			if cdata != v1 {
				log.Printf("expected: %d, actual: %v", cdata, v1)
			}
			c.Assert(cdata, check.Equals, v1)
		}
	}
}

func setRouteRecordMonitor(cfg config.Configuration) {
	cfg.GetGlobal().GetStatReporter().SetChain([]string{config.DefaultServiceRouteReporter})
	cfg.GetGlobal().GetStatReporter().SetPluginConfig(config.DefaultServiceRouteReporter, &serviceroute.Config{ReportInterval: model.ToDurationPtr(1 * time.Second)})
}
