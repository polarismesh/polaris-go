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
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/polarismesh/polaris-go"
)

var (
	namespace     string
	service       string
	selfNamespace string
	selfService   string
	port          int64
)

func initArgs() {
	flag.StringVar(&namespace, "namespace", "default", "namespace")
	flag.StringVar(&service, "service", "RouteNearbyEchoServer", "service")
	flag.StringVar(&selfNamespace, "selfNamespace", "default", "selfNamespace")
	flag.StringVar(&selfService, "selfService", "", "selfService")
	flag.Int64Var(&port, "port", 18080, "port")
}

// PolarisConsumer .
type PolarisConsumer struct {
	consumer  polaris.ConsumerAPI
	router    polaris.RouterAPI
	namespace string
	service   string
	host      string
	port      int
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

func (svr *PolarisConsumer) runWebServer() {
	http.HandleFunc("/echo", func(rw http.ResponseWriter, r *http.Request) {
		log.Printf("start to invoke getOneInstance operation")
		getOneRequest := &polaris.GetOneInstanceRequest{}
		getOneRequest.Namespace = namespace
		getOneRequest.Service = service
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

		msg := fmt.Sprintf("RouteNearbyEchoServer Consumer, MyLocInfo's : %s, host : %s:%d => ", string(locStr), svr.host, svr.port)
		_, _ = buf.WriteString(msg)

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
		namespace: namespace,
		service:   service,
	}

	svr.Run()

}

func convertQuery(rawQuery string) map[string]string {
	meta := make(map[string]string)
	if len(rawQuery) == 0 {
		return meta
	}
	tokens := strings.Split(rawQuery, "&")
	if len(tokens) > 0 {
		for _, token := range tokens {
			values := strings.Split(token, "=")
			meta[values[0]] = values[1]
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
