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

package main

import (
	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	plog "github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"log"
	_ "net/http/pprof"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ctxCount  int
	namespace string
	service   string
)

func CalDuration(timeNow time.Time, duration int64) int64 {
	t1 := timeNow.Unix()
	dur := t1 / int64(duration)
	return dur
}

type Status struct {
	pass  int64
	limit int64

	print bool
}

var totalStatus map[int64]*Status
var lock sync.Mutex

func AddTotal(dur int64, pass int64, limit int64) {
	lock.Lock()
	defer lock.Unlock()
	if v, ok := totalStatus[dur]; ok {
		v.pass += pass
		v.limit += limit
	} else {
		s := Status{
			pass:  pass,
			limit: limit,
			print: false,
		}
		totalStatus[dur] = &s
		if v1, ok := totalStatus[dur-1]; ok {
			total := v1.pass + v1.limit
			var percent float64
			if v1.pass > 100000 {
				percent = float64(v1.pass-100000) / float64(total) * 100
			} else {
				percent = 0
			}
			_ = percent
			api.GetBaseLogger().Infof("[ratelimit-test-info]---rDur:%d total:%d pass:%d limit:%d percent:%f",
				dur-1, v1.pass+v1.limit, v1.pass, v1.limit, percent)
			//fmt.Printf("[polaris-ratelimit-go]---rDur:%d total:%d pass:%d limit:%d percent:%f\n",
			//	dur-1, v1.pass+v1.limit, v1.pass, v1.limit, percent)
		}
	}
}

//主入口函数
func main() {
	err := plog.GetBaseLogger().SetLogLevel(api.InfoLog)
	if nil != err {
		log.Fatalf("fail to SetLogLevel, err is %v", err)
	}
	ctxCount = 3
	contexts := make([]api.LimitAPI, 0, ctxCount)

	namespace = "Test"
	service = "polaris-ratelimit-go"
	labelsMap := make(map[string]string)
	labelsMap["go-label-name2"] = "label-value-whole"

	api.SetLoggersLevel(api.InfoLog)
	for i := 0; i < ctxCount; i++ {
		cfg := api.NewConfiguration()
		cfg.GetGlobal().GetServerConnector().SetAddresses([]string{"9.205.2.29:8081"})
		cfg.GetProvider().GetRateLimit().SetRateLimitCluster(config.ServerNamespace, "polaris.metric.v2.test")
		cfg.GetProvider().GetRateLimit().GetRateLimitCluster().SetRefreshInterval(time.Second * 10)

		cfg.GetGlobal().GetServerConnector().SetConnectTimeout(time.Second * 1)
		cfg.GetGlobal().GetServerConnector().SetMessageTimeout(time.Second * 1)
		cfg.GetGlobal().GetAPI().SetTimeout(time.Second * 3)

		limitApi, err := api.NewLimitAPIByConfig(cfg)
		if nil != err {
			log.Fatalf("fail to create limitapi, error is %v", err)
		}
		contexts = append(contexts, limitApi)
	}
	wg := sync.WaitGroup{}
	wg.Add(ctxCount)
	totalStatus = make(map[int64]*Status)
	lock = sync.Mutex{}

	for i := 0; i < ctxCount; i++ {
		go func(idx int) {
			defer wg.Done()
			timeNow := time.Now()
			needSleep := (timeNow.UnixNano()/int64(time.Second)+1)*int64(1000) -
				timeNow.UnixNano()/int64(time.Millisecond) + 1
			time.Sleep(time.Millisecond * time.Duration(needSleep))
			limitAPI := contexts[idx]
			var limit int64 = 0
			var pass int64 = 0
			rDur := CalDuration(time.Now(), 1)
			for {
				select {
				default:
					quotaRequest := api.NewQuotaRequest()
					quotaRequest.SetNamespace(namespace)
					quotaRequest.SetService(service)
					quotaRequest.SetLabels(labelsMap)

					timeNow := time.Now()
					dur1 := CalDuration(timeNow, 1)
					if dur1 != rDur {
						api.GetBaseLogger().Infof("[ratelimit-test-info]i:%d rDur:%d total:%d pass:%d limit:%d",
							idx, rDur, pass+limit, pass, limit)
						AddTotal(rDur, pass, limit)
						pass = 0
						limit = 0
						rDur = dur1
					}
					quotaRsp, err := limitAPI.GetQuota(quotaRequest)
					if nil != err {
						log.Fatalf("fail to getQuota, error is %v", err)
					}
					resp := quotaRsp.Get()
					if resp.Code == model.QuotaResultOk {
						atomic.AddInt64(&pass, 1)
					} else {
						atomic.AddInt64(&limit, 1)
					}
					time.Sleep(time.Millisecond * 3)
				}
			}
		}(i)
	}
	wg.Wait()
	log.Printf("test done")
}
