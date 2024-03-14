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
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/polarismesh/polaris-go"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
)

var (
	namespace     string
	service       string
	selfNamespace string
	selfService   string
	port          int64
	token         string
)

func initArgs() {
	flag.StringVar(&namespace, "namespace", "default", "namespace")
	flag.StringVar(&service, "service", "RouteEchoServer", "service")
	flag.StringVar(&selfNamespace, "selfNamespace", "default", "selfNamespace")
	flag.StringVar(&selfService, "selfService", "", "selfService")
	flag.Int64Var(&port, "port", 18080, "port")
	flag.StringVar(&token, "token", "FPI+K9USIvHYU8JUljM3TqAg1Wizxta7i+WEi73RkDMQl1HhIBoIc+EKYinqiViTx7TJlBJSY2/R/tXfZkGv8mGB", "token")
}

// PolarisConsumer .
type PolarisConsumer struct {
	consumer  polaris.ConsumerAPI
	router    polaris.RouterAPI
	provider  polaris.ProviderAPI
	namespace string
	service   string
}

// Run .
func (svr *PolarisConsumer) Run() {
	if selfService != "" && selfNamespace != "" {
		tmpHost, err := getLocalHost(svr.provider.SDKContext().GetConfig().GetGlobal().GetServerConnector().GetAddresses()[0])
		if nil != err {
			panic(fmt.Errorf("error occur while fetching localhost: %v", err))
		}
		req := &polaris.InstanceRegisterRequest{}
		req.Namespace = selfNamespace
		req.Service = selfService
		log.Printf("start to invoke register operation")
		registerRequest := &polaris.InstanceRegisterRequest{}
		registerRequest.Service = service
		registerRequest.Namespace = namespace
		registerRequest.Host = tmpHost
		registerRequest.Port = int(port)
		registerRequest.ServiceToken = token
		resp, err := svr.provider.RegisterInstance(registerRequest)
		if nil != err {
			log.Fatalf("fail to register instance, err is %v", err)
		}
		log.Printf("register response: instanceId %s", resp.InstanceID)
	}

	svr.runWebServer()
}

func (svr *PolarisConsumer) runWebServer() {
	http.HandleFunc("/echo", func(rw http.ResponseWriter, r *http.Request) {
		log.Printf("start to invoke getOneInstance operation")
		getAllRequest := &polaris.GetAllInstancesRequest{}
		getAllRequest.Namespace = namespace
		getAllRequest.Service = service
		getAllRequest.AuthToken = token
		instancesResp, err := svr.consumer.GetAllInstances(getAllRequest)
		if nil != err {
			log.Printf("[error] fail to getAllInstances, err is %v", err)
			rw.WriteHeader(http.StatusOK)
			_, _ = rw.Write([]byte(fmt.Sprintf("fail to getAllInstances, err is %v", err)))
			return
		}
		log.Printf("all instances count %d", len(instancesResp.Instances))
		routerRequest := &polaris.ProcessRoutersRequest{}
		routerRequest.DstInstances = instancesResp
		routerRequest.SourceService.Service = selfService
		routerRequest.SourceService.Namespace = selfNamespace
		routerRequest.AddArguments(convertQuery(r.URL.RawQuery)...)
		routerInstancesResp, err := svr.router.ProcessRouters(routerRequest)
		if nil != err {
			log.Printf("[error] fail to processRouters, err is %v", err)
			rw.WriteHeader(http.StatusOK)
			_, _ = rw.Write([]byte(fmt.Sprintf("fail to processRouters, err is %v", err)))
			return
		}
		log.Printf("router instances count %d", len(routerInstancesResp.Instances))

		buf := &bytes.Buffer{}
		for i := 0; i < 10; i++ {
			lbRequest := &polaris.ProcessLoadBalanceRequest{}
			lbRequest.DstInstances = routerInstancesResp
			lbRequest.LbPolicy = config.DefaultLoadBalancerWR
			oneInstResp, err := svr.router.ProcessLoadBalance(lbRequest)
			if nil != err {
				log.Printf("[error] fail to processLoadBalance, err is %v", err)
				rw.WriteHeader(http.StatusOK)
				_, _ = rw.Write([]byte(fmt.Sprintf("fail to processLoadBalance, err is %v", err)))
				return
			}
			instance := oneInstResp.GetInstance()
			if nil != instance {
				log.Printf("instance getOneInstance is %s:%d", instance.GetHost(), instance.GetPort())
			}

			func() {
				resp, err := http.Get(fmt.Sprintf("http://%s:%d/echo", instance.GetHost(), instance.GetPort()))
				if err != nil {
					log.Printf("[error] send request to %s:%d fail : %s", instance.GetHost(), instance.GetPort(), err)
					rw.WriteHeader(http.StatusOK)
					_, _ = rw.Write([]byte(fmt.Sprintf("send request to %s:%d fail : %s", instance.GetHost(), instance.GetPort(), err)))
					return
				}
				data, err := ioutil.ReadAll(resp.Body)
				if err != nil {
					log.Printf("read resp from %s:%d fail : %s", instance.GetHost(), instance.GetPort(), err)
					rw.WriteHeader(http.StatusOK)
					_, _ = rw.Write([]byte(fmt.Sprintf("read resp from %s:%d fail : %s", instance.GetHost(), instance.GetPort(), err)))
					return
				}
				_, _ = buf.Write(data)
				_ = buf.WriteByte('\n')
				defer resp.Body.Close()
				time.Sleep(30 * time.Millisecond)
			}()

		}
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write(buf.Bytes())

	})

	log.Printf("start run web server, port : %d", port)

	if err := http.ListenAndServe(fmt.Sprintf("0.0.0.0:%d", port), nil); err != nil {
		log.Fatalf("[ERROR]fail to run webServer, err is %v", err)
	}
}

func main() {
	initArgs()
	flag.Parse()
	if len(namespace) == 0 || len(service) == 0 {
		log.Print("namespace and service are required")
		return
	}
	sdkCtx, err := polaris.NewSDKContext()
	if nil != err {
		log.Fatalf("fail to create sdk context, err is %v", err)
	}

	defer sdkCtx.Destroy()

	svr := &PolarisConsumer{
		consumer:  polaris.NewConsumerAPIByContext(sdkCtx),
		router:    polaris.NewRouterAPIByContext(sdkCtx),
		provider:  polaris.NewProviderAPIByContext(sdkCtx),
		namespace: namespace,
		service:   service,
	}

	svr.Run()

}

func convertQuery(rawQuery string) []model.Argument {
	arguments := make([]model.Argument, 0, 4)
	if len(rawQuery) == 0 {
		return arguments
	}
	tokens := strings.Split(rawQuery, "&")
	if len(tokens) > 0 {
		for _, token := range tokens {
			values := strings.Split(token, "=")
			arguments = append(arguments, model.BuildQueryArgument(values[0], values[1]))
		}
	}
	return arguments
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
