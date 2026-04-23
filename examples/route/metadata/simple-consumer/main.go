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
	"github.com/polarismesh/polaris-go/pkg/model"
)

var (
	calleeNamespace string
	calleeService   string
	namespace       string
	service         string
	metadata        string
	port            int64
)

func initArgs() {
	flag.StringVar(&calleeNamespace, "calleeNamespace", "default", "calleeNamespace") // TODO: rename
	flag.StringVar(&calleeService, "calleeService", "RouteMetadataEchoServer", "calleeService")
	flag.StringVar(&namespace, "namespace", "default", "namespace")
	flag.StringVar(&service, "service", "RouteMetadataEchoCaller", "service")
	flag.StringVar(&metadata, "metadata", "", "key1=value1&key2=value2")
	flag.Int64Var(&port, "port", 18090, "port")
}

// PolarisConsumer .
type PolarisConsumer struct {
	consumer    polaris.ConsumerAPI
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
		log.Fatalf("fail to UpdateServiceCallResult, err is %v, res:%s", err, jsonEncode(svcCallResult))
	} else {
		log.Printf("UpdateServiceCallResult success, res:%s", jsonEncode(svcCallResult))
	}
}

func (svr *PolarisConsumer) runWebServer() {
	http.HandleFunc("/echo", func(rw http.ResponseWriter, r *http.Request) {
		log.Printf("start to invoke getOneInstance operation")
		getOneRequest := &polaris.GetOneInstanceRequest{}
		getOneRequest.Namespace = calleeNamespace
		getOneRequest.Service = calleeService
		getOneRequest.Metadata = svr.metadataMap
		oneInstResp, err := svr.consumer.GetOneInstance(getOneRequest)
		if nil != err {
			log.Printf("[error] fail to getAllInstances, err is %v", err)
			rw.WriteHeader(http.StatusOK)
			_, _ = rw.Write([]byte(fmt.Sprintf("fail to getAllInstances, err is %v", err)))
			return
		}

		instance := oneInstResp.GetInstance()
		if nil != instance {
			log.Printf("instance getOneInstance is %s:%d", instance.GetHost(), instance.GetPort())
		}

		var buf bytes.Buffer

		loc := svr.consumer.SDKContext().GetValueContext().GetCurrentLocation().GetLocation()
		locStr, _ := json.Marshal(loc)

		svr.consumer.SDKContext().GetConfig()
		msg := fmt.Sprintf("RouteNearbyEchoServer Consumer, MyLocInfo's : %s, host : %s:%d => ", string(locStr), svr.host, svr.port)
		_, _ = buf.WriteString(msg)

		//服务调用结果，用于在后面进行调用结果上报
		svcCallResult := &polaris.ServiceCallResult{}
		//将服务上报对象设置为获取到的实例
		svcCallResult.SetCalledInstance(instance)

		func() {
			requestStartTime := time.Now()
			resp, err := http.Get(fmt.Sprintf("http://%s:%d/echo", instance.GetHost(), instance.GetPort()))
			//设置调用耗时
			svcCallResult.SetDelay(time.Since(requestStartTime))
			if err != nil {
				log.Printf("[error] send request to %s:%d fail : %s", instance.GetHost(), instance.GetPort(), err)
				rw.WriteHeader(http.StatusOK)
				_, _ = rw.Write([]byte(fmt.Sprintf("send request to %s:%d fail : %s", instance.GetHost(), instance.GetPort(), err)))
				svr.reportResult(svcCallResult, api.RetFail, -1)
				return
			}
			data, err := io.ReadAll(resp.Body)
			if err != nil {
				log.Printf("read resp from %s:%d fail : %s", instance.GetHost(), instance.GetPort(), err)
				rw.WriteHeader(http.StatusOK)
				_, _ = rw.Write([]byte(fmt.Sprintf("read resp from %s:%d fail : %s", instance.GetHost(), instance.GetPort(), err)))
				svr.reportResult(svcCallResult, api.RetFail, -1)
				return
			}
			log.Printf("read resp from %s:%d, data:%s", instance.GetHost(), instance.GetPort(), string(data))
			svr.reportResult(svcCallResult, api.RetSuccess, 0)

			_, _ = buf.Write(data)
			_ = buf.WriteByte('\n')
			defer resp.Body.Close()
			time.Sleep(30 * time.Millisecond)
		}()

		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write(buf.Bytes())

	})

	log.Printf("start run web server, port : %d", port)

	ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		log.Fatalf("[ERROR]fail to listen tcp, err is %v", err)
	}

	svr.port = ln.Addr().(*net.TCPAddr).Port

	if err := http.Serve(ln, nil); err != nil {
		log.Fatalf("[ERROR]fail to run webServer, err is %v", err)
	}
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

	svcRouterChain := sdkCtx.GetConfig().GetConsumer().GetServiceRouter().GetChain()
	log.Printf("calleeService router chain: %+v", jsonEncode(svcRouterChain))

	svr := &PolarisConsumer{
		consumer:    polaris.NewConsumerAPIByContext(sdkCtx),
		namespace:   calleeNamespace,
		service:     calleeService,
		metadataMap: convertMetadata(),
	}

	svr.Run()

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

func convertMetadata() map[string]string {
	if len(metadata) == 0 {
		return map[string]string{}
	}
	values := strings.Split(metadata, "&")

	meta := make(map[string]string)
	for i := range values {
		entry := strings.Split(values[i], "=")
		if len(entry) == 2 {
			meta[entry[0]] = entry[1]
		}
	}

	return meta
}
