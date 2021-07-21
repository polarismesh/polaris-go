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
	plog "github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/sample/ratelimit/util"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"strconv"
	"time"
)

const logLevel = api.InfoLog

//主函数入口
func main() {
	go func() {
		log.Println(http.ListenAndServe("localhost:6070", nil))
	}()
	argsWithoutProg := os.Args[1:]
	if len(argsWithoutProg) < 4 {
		log.Fatalf(
			"using %s <namespace> <service> <labelk1:labelv1,labelk2:labelv2,....> <count>", os.Args[0])
	}
	var err error
	var namespace string
	var service string
	var labelsStr string
	var labels map[string]string
	var count int
	//设置日志级别为Debug
	err = plog.GetBaseLogger().SetLogLevel(logLevel)
	if nil != err {
		log.Fatalf("fail to SetLogLevel, err is %v", err)
	}
	namespace = argsWithoutProg[0]
	service = argsWithoutProg[1]
	labelsStr = argsWithoutProg[2]
	count, err = strconv.Atoi(argsWithoutProg[3])
	if nil != err {
		log.Fatalf("fail to convert count %s to int, err %v", argsWithoutProg[5], err)
	}
	labels, err = util.ParseLabels(labelsStr)
	if nil != err {
		log.Fatalf("fail to parse label string %s, err %v", labelsStr, err)
	}
	//获取默认配置
	cfg := api.NewConfiguration()
	limitAPI, err := api.NewLimitAPIByConfig(cfg)
	if nil != err {
		log.Fatalf("fail to create limitAPI api by default config file, err %v", err)
	}
	defer limitAPI.Destroy()
	//保留quotaKey，后续调用可以直接传入quotaKey，无需再使用labels，可以提升性能
	//循环申请多次配额
	for i := 0; i < count; i++ {
		time.Sleep(10 * time.Millisecond)
		//创建访问限流请求
		quotaReq := api.NewQuotaRequest()
		//设置命名空间
		quotaReq.SetNamespace(namespace)
		//设置服务名
		quotaReq.SetService(service)
		//设置标签值
		quotaReq.SetLabels(labels)
		//调用配额获取接口
		future, err := limitAPI.GetQuota(quotaReq)
		if nil != err {
			log.Fatalf("fail to getQuota, err %v", err)
		}
		resp := future.Get()
		if api.QuotaResultOk == resp.Code {
			//本次调用不限流，放通，接下来可以处理业务逻辑
			log.Printf("quota result ok")
		} else {
			//本次调用限流判定不通过，调用受限，需返回错误给主调端
			log.Printf("quota result fail, info is %s", resp.Info)
		}
	}
}
