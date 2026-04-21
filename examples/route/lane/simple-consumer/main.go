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

// Package main 泳道路由简化消费端示例（基于 GetOneInstance）。
//
// 与 consumer 示例不同，本示例使用 ConsumerAPI.GetOneInstance 一步完成
// 服务发现 + 路由过滤 + 负载均衡，无需手动调用 ProcessRouters 和
// ProcessLoadBalance，适用于不需要手动控制路由流程的场景。
//
// 用法：
//
//	./bin -namespace=default -service=LaneEchoServer -port=19081
//
// 请求示例：
//
//	curl http://127.0.0.1:19081/echo
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/polarismesh/polaris-go"
	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
)

var (
	namespace string
	service   string
	port      int64
	debug     bool
)

func initArgs() {
	flag.StringVar(&namespace, "namespace", "default", "namespace")
	flag.StringVar(&service, "service", "LaneEchoServer", "service")
	flag.Int64Var(&port, "port", 19081, "consumer HTTP listen port")
	flag.BoolVar(&debug, "debug", false, "是否开启 Polaris SDK debug 日志")
}

// SimpleLaneConsumer 使用 GetOneInstance 的简化泳道消费端
type SimpleLaneConsumer struct {
	consumer  polaris.ConsumerAPI
	namespace string
	service   string
}

// Run starts the HTTP server.
func (svr *SimpleLaneConsumer) Run() {
	http.HandleFunc("/echo", svr.handleEcho)
	log.Printf("start simple lane consumer, port: %d", port)
	if err := http.ListenAndServe(fmt.Sprintf("0.0.0.0:%d", port), nil); err != nil {
		log.Fatalf("[ERROR] fail to run webServer, err is %v", err)
	}
}

func (svr *SimpleLaneConsumer) handleEcho(rw http.ResponseWriter, r *http.Request) {
	log.Printf("received request from %s", r.RemoteAddr)

	// 通过 GetOneInstance 一步完成服务发现 + 路由 + 负载均衡
	instance, err := svr.getOneInstance()
	if err != nil {
		log.Printf("[error] fail to getOneInstance: %v", err)
		http.Error(rw, fmt.Sprintf("fail to getOneInstance: %v", err), http.StatusInternalServerError)
		return
	}
	log.Printf("selected instance: %s:%d metadata=%v",
		instance.GetHost(), instance.GetPort(), instance.GetMetadata())

	// 调用下游服务
	callResult := &polaris.ServiceCallResult{}
	callResult.SetCalledInstance(instance)
	startTime := time.Now()

	resp, err := http.Get(fmt.Sprintf("http://%s:%d/echo", instance.GetHost(), instance.GetPort()))
	callResult.SetDelay(time.Since(startTime))

	if err != nil {
		log.Printf("[error] send request to %s:%d fail: %s", instance.GetHost(), instance.GetPort(), err)
		svr.reportResult(callResult, model.RetFail, -1)
		http.Error(rw, fmt.Sprintf("send request fail: %s", err), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("[error] read resp from %s:%d fail: %s", instance.GetHost(), instance.GetPort(), err)
		svr.reportResult(callResult, model.RetFail, -1)
		http.Error(rw, fmt.Sprintf("read resp fail: %s", err), http.StatusInternalServerError)
		return
	}

	svr.reportResult(callResult, model.RetSuccess, int32(resp.StatusCode))
	rw.WriteHeader(http.StatusOK)
	_, _ = rw.Write(data)
}

// getOneInstance 通过 ConsumerAPI.GetOneInstance 获取单个可用实例。
// SDK 内部会自动执行路由过滤（包括泳道路由）和负载均衡。
func (svr *SimpleLaneConsumer) getOneInstance() (model.Instance, error) {
	getOneRequest := &polaris.GetOneInstanceRequest{}
	getOneRequest.Namespace = svr.namespace
	getOneRequest.Service = svr.service
	oneInstResp, err := svr.consumer.GetOneInstance(getOneRequest)
	if err != nil {
		return nil, fmt.Errorf("fail to getOneInstance: %w", err)
	}
	return oneInstResp.GetInstance(), nil
}

func (svr *SimpleLaneConsumer) reportResult(result *polaris.ServiceCallResult, retStatus model.RetStatus, retCode int32) {
	result.SetRetStatus(retStatus)
	result.SetRetCode(retCode)
	if err := svr.consumer.UpdateServiceCallResult(result); err != nil {
		log.Printf("[error] fail to UpdateServiceCallResult: %v", err)
	}
}

func main() {
	initArgs()
	flag.Parse()
	if namespace == "" || service == "" {
		log.Fatal("namespace and service are required")
	}

	if debug {
		if err := api.SetLoggersLevel(api.DebugLog); err != nil {
			log.Printf("[WARN] 设置日志级别为 DEBUG 失败: %v", err)
		} else {
			log.Printf("[INFO] 已设置 Polaris SDK 日志级别为 DEBUG")
		}
	}

	consumer, err := polaris.NewConsumerAPI()
	if err != nil {
		log.Fatalf("fail to create consumerAPI: %v", err)
	}
	defer consumer.Destroy()

	svr := &SimpleLaneConsumer{
		consumer:  consumer,
		namespace: namespace,
		service:   service,
	}
	svr.Run()
}
