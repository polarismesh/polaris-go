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

package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/polarismesh/polaris-go"
	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
)

const (
	// defaultRequestTimeout HTTP请求超时时间
	defaultRequestTimeout = 5 * time.Second
	// sleepAfterRequest 请求后的等待时间
	sleepAfterRequest = 30 * time.Millisecond
)

var (
	calleeNamespace string
	calleeService   string
	selfNamespace   string
	selfService     string
	metadataStr     string
	port            int64
	token           string
	debug           bool
)

func initArgs() {
	flag.StringVar(&calleeNamespace, "calleeNamespace", "default", "calleeNamespace")
	flag.StringVar(&calleeService, "calleeService", "RouteMetadataEchoServer", "calleeService")
	flag.StringVar(&selfNamespace, "selfNamespace", "default", "selfNamespace")
	flag.StringVar(&selfService, "selfService", "RouteMetadataEchoCaller", "selfService")
	flag.StringVar(&metadataStr, "metadata", "", "key1=value1&key2=value2")
	flag.Int64Var(&port, "port", 18090, "port")
	flag.StringVar(&token, "token", "", "token")
	flag.BoolVar(&debug, "debug", false, "debug")
}

// PolarisConsumer .
// 本示例演示 ProcessRouters + ProcessLoadBalance 的手动三段式调用，
// 配合"元数据过滤"场景：
//  1. GetAllInstances 拉取被调服务的全量实例；
//  2. 按命令行 --metadata 指定的 key/value 在业务侧筛选实例，并用
//     model.NewDefaultServiceInstances 包装成新的实例集合；
//  3. 再交给 ProcessRouters 执行剩余路由链（规则路由、就近路由等）；
//  4. 最终用 ProcessLoadBalance 选出目标实例并发起调用。
//
// 若只是想让 SDK 内部一把梭（GetOneInstance + dstMetaRouter），参考同级
// 目录下的 simple-consumer/ 示例。
type PolarisConsumer struct {
	consumer    polaris.ConsumerAPI
	router      polaris.RouterAPI
	namespace   string
	service     string
	host        string
	port        int
	metadataMap map[string]string
}

// Run .
func (svr *PolarisConsumer) Run() {
	tmpHost, err := getLocalHost(svr.consumer.SDKContext().GetConfig().GetGlobal().GetServerConnector().GetAddresses()[0])
	if nil != err {
		panic(fmt.Errorf("error occur while fetching localhost: %v", err))
	}
	svr.host = tmpHost
	svr.runWebServer()
}

func (svr *PolarisConsumer) reportResult(svcCallResult *polaris.ServiceCallResult, retStatus model.RetStatus,
	retCode int32) {
	svcCallResult.SetRetStatus(retStatus)
	svcCallResult.SetRetCode(retCode)
	err := svr.consumer.UpdateServiceCallResult(svcCallResult)
	if err != nil {
		log.Printf("[error] fail to UpdateServiceCallResult, err is %v, res:%s", err, jsonEncode(svcCallResult))
	} else {
		log.Printf("UpdateServiceCallResult success, res:%s", jsonEncode(svcCallResult))
	}
}

// callInstance 调用指定的服务实例
func (svr *PolarisConsumer) callInstance(instance model.Instance) ([]byte, error) {
	client := &http.Client{
		Timeout: defaultRequestTimeout,
	}
	url := fmt.Sprintf("http://%s:%d/echo", instance.GetHost(), instance.GetPort())
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("send request to %s:%d fail: %w", instance.GetHost(), instance.GetPort(), err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read resp from %s:%d fail: %w", instance.GetHost(), instance.GetPort(), err)
	}
	return data, nil
}

// filterByMetadata 按 key/value 在业务侧对实例集合做元数据过滤。
// 当 meta 为空时直接原样返回。
func filterByMetadata(instances []model.Instance, meta map[string]string) []model.Instance {
	if len(meta) == 0 {
		return instances
	}
	result := make([]model.Instance, 0, len(instances))
	for _, inst := range instances {
		instMeta := inst.GetMetadata()
		if matchMetadata(instMeta, meta) {
			result = append(result, inst)
		}
	}
	return result
}

func matchMetadata(instMeta, expect map[string]string) bool {
	if len(instMeta) == 0 {
		return false
	}
	for k, v := range expect {
		val, ok := instMeta[k]
		if !ok || val != v {
			return false
		}
	}
	return true
}

func (svr *PolarisConsumer) runWebServer() {
	http.HandleFunc("/echo", svr.handleEcho)

	log.Printf("start run web server, port : %d", port)

	ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		log.Fatalf("[ERROR]fail to listen tcp, err is %v", err)
	}
	svr.port = ln.Addr().(*net.TCPAddr).Port

	log.Printf("[INFO] start http server, listen port is %v", svr.port)
	if err := http.Serve(ln, nil); err != nil {
		log.Fatalf("[ERROR]fail to run webServer, err is %v", err)
	}
}

// handleEcho 处理 echo 请求
func (svr *PolarisConsumer) handleEcho(rw http.ResponseWriter, r *http.Request) {
	// 1) 拉取全量实例
	log.Printf("start to invoke getAllInstances operation")
	getAllRequest := &polaris.GetAllInstancesRequest{}
	getAllRequest.Namespace = svr.namespace
	getAllRequest.Service = svr.service
	allResp, err := svr.consumer.GetAllInstances(getAllRequest)
	if err != nil {
		log.Printf("[error] fail to getAllInstances, err is %v", err)
		http.Error(rw, fmt.Sprintf("fail to getAllInstances, err is %v", err), http.StatusInternalServerError)
		return
	}
	log.Printf("all instances count %d", len(allResp.Instances))

	// 2) 按 metadata 在业务侧做一次预过滤，并用 DefaultServiceInstances 包装
	filtered := filterByMetadata(allResp.Instances, svr.metadataMap)
	log.Printf("after metadata filter(%v), instances count %d", svr.metadataMap, len(filtered))
	if len(filtered) == 0 {
		log.Printf("[error] no instance matches metadata %v", svr.metadataMap)
		http.Error(rw, fmt.Sprintf("no instance matches metadata %v", svr.metadataMap), http.StatusServiceUnavailable)
		return
	}
	dstInstances := model.NewDefaultServiceInstances(model.ServiceInfo{
		Namespace: svr.namespace,
		Service:   svr.service,
	}, filtered)

	// 3) ProcessRouters: 再跑一遍 SDK 配置的剩余路由链（规则路由/就近路由等）
	routerRequest := &polaris.ProcessRoutersRequest{}
	routerRequest.DstInstances = dstInstances
	routerRequest.SourceService = model.ServiceInfo{
		Namespace: selfNamespace,
		Service:   selfService,
		Metadata:  svr.metadataMap,
	}
	routerRequest.AddArguments(convertRouteArguments(r)...)
	routerInstancesResp, err := svr.router.ProcessRouters(routerRequest)
	if err != nil {
		log.Printf("[error] fail to processRouters, err is %v", err)
		http.Error(rw, fmt.Sprintf("fail to processRouters, err is %v", err), http.StatusInternalServerError)
		return
	}
	log.Printf("router instances count %d", len(routerInstancesResp.Instances))
	for i, inst := range routerInstancesResp.Instances {
		log.Printf("  [%d] %s:%d metadata=%v", i, inst.GetHost(), inst.GetPort(), inst.GetMetadata())
	}

	// 4) ProcessLoadBalance: 从过滤后的实例里挑一个
	lbRequest := &polaris.ProcessLoadBalanceRequest{}
	lbRequest.DstInstances = routerInstancesResp
	lbRequest.LbPolicy = config.DefaultLoadBalancerWR
	oneInstResp, err := svr.router.ProcessLoadBalance(lbRequest)
	if err != nil {
		log.Printf("[error] fail to processLoadBalance, err is %v", err)
		http.Error(rw, fmt.Sprintf("fail to processLoadBalance, err is %v", err), http.StatusInternalServerError)
		return
	}
	instance := oneInstResp.GetInstance()
	if instance == nil {
		log.Printf("[error] no available instance")
		http.Error(rw, "no available instance", http.StatusServiceUnavailable)
		return
	}
	log.Printf("instance picked is %s:%d, metadata: %v",
		instance.GetHost(), instance.GetPort(), instance.GetMetadata())

	// 5) 真正发起调用并上报
	var buf bytes.Buffer
	loc := svr.consumer.SDKContext().GetValueContext().GetCurrentLocation().GetLocation()
	locStr, _ := json.Marshal(loc)
	msg := fmt.Sprintf("RouteMetadataEchoServer Consumer, MyLocInfo's : %s, host : %s:%d => ",
		string(locStr), svr.host, svr.port)
	buf.WriteString(msg)

	svcCallResult := &polaris.ServiceCallResult{}
	svcCallResult.SetCalledInstance(instance)

	requestStartTime := time.Now()
	data, err := svr.callInstance(instance)
	svcCallResult.SetDelay(time.Since(requestStartTime))
	if err != nil {
		log.Printf("[error] %v", err)
		svr.reportResult(svcCallResult, api.RetFail, -1)
		http.Error(rw, err.Error(), http.StatusBadGateway)
		return
	}
	log.Printf("read resp from %s:%d, data:%s", instance.GetHost(), instance.GetPort(), string(data))
	svr.reportResult(svcCallResult, api.RetSuccess, 0)

	buf.Write(data)
	buf.WriteByte('\n')

	time.Sleep(sleepAfterRequest)

	rw.WriteHeader(http.StatusOK)
	_, _ = rw.Write(buf.Bytes())
}

func main() {
	initArgs()
	flag.Parse()
	if len(calleeNamespace) == 0 || len(calleeService) == 0 {
		log.Print("calleeNamespace and calleeService are required")
		return
	}
	sdkCtx, err := polaris.NewSDKContext()
	if nil != err {
		log.Fatalf("fail to create sdk context, err is %v", err)
	}
	defer sdkCtx.Destroy()

	if debug {
		if err := api.SetLoggersLevel(api.DebugLog); err != nil {
			log.Printf("fail to set log level to DEBUG, err is %v", err)
		} else {
			log.Printf("successfully set log level to DEBUG")
		}
	}

	svcRouter := sdkCtx.GetConfig().GetConsumer().GetServiceRouter()
	log.Printf("service router config: %+v", jsonEncode(svcRouter))

	svr := &PolarisConsumer{
		consumer:    polaris.NewConsumerAPIByContext(sdkCtx),
		router:      polaris.NewRouterAPIByContext(sdkCtx),
		namespace:   calleeNamespace,
		service:     calleeService,
		metadataMap: convertMetadata(metadataStr),
	}
	log.Printf("parsed metadata filter: %v", svr.metadataMap)

	svr.Run()
}

// convertRouteArguments 把 HTTP Header 与 URL Query 转换成 polaris-go 路由参数
func convertRouteArguments(r *http.Request) []model.Argument {
	arguments := make([]model.Argument, 0, 4)
	for k, vs := range r.Header {
		if len(vs) == 0 {
			continue
		}
		arguments = append(arguments, model.BuildHeaderArgument(strings.ToLower(k), vs[0]))
	}
	for k, vs := range r.URL.Query() {
		if len(vs) == 0 {
			continue
		}
		arguments = append(arguments, model.BuildQueryArgument(strings.ToLower(k), vs[0]))
	}
	log.Printf("total arguments count: %d, %v", len(arguments), arguments)
	return arguments
}

func convertMetadata(raw string) map[string]string {
	if len(raw) == 0 {
		return map[string]string{}
	}
	meta := make(map[string]string)
	for _, kv := range strings.Split(raw, "&") {
		entry := strings.SplitN(kv, "=", 2)
		if len(entry) == 2 {
			meta[entry[0]] = entry[1]
		}
	}
	return meta
}

func getLocalHost(serverAddr string) (string, error) {
	conn, err := net.Dial("tcp", serverAddr)
	if nil != err {
		return "", err
	}
	localAddr := conn.LocalAddr().String()
	colonIdx := strings.LastIndex(localAddr, ":")
	if colonIdx > 0 {
		return localAddr[:colonIdx], nil
	}
	return localAddr, nil
}

func jsonEncode(data interface{}) string {
	buf, err := json.Marshal(data)
	if err != nil {
		return ""
	}
	return string(buf)
}
