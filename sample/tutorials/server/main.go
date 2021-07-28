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
	"fmt"
	"github.com/polarismesh/polaris-go/api"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
)

//http handler
type httpHandler struct {
	address string
}

//http handler
func (h *httpHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "Hello From %s", h.address)
}

//主流程
func main() {
	var err error
	//参数校验
	argsWithoutProg := os.Args[1:]
	if len(argsWithoutProg) < 5 {
		log.Fatalf("using %s <namespace> <service> <token> <ip> <port>", os.Args[0])
	}
	namespace := argsWithoutProg[0]
	service := argsWithoutProg[1]
	token := argsWithoutProg[2]
	address := net.ParseIP(argsWithoutProg[3])
	if address == nil {
		log.Fatalf("fail to parse %s as valid ip address", argsWithoutProg[3])
	}
	var port int
	port, err = strconv.Atoi(argsWithoutProg[4])
	if err != nil {
		log.Fatalf("fail to convert port %s to number, err: %v", argsWithoutProg[1], err)
	}
	if port < 0 || port > 65535 {
		log.Fatalf("port %d is not in the range [0, 65535]", port)
	}

	//注册服务实例
	var provider api.ProviderAPI
	provider, err = api.NewProviderAPI()
	if err != nil {
		log.Fatalf("fail to create providerAPI, err: %v", err)
	}
	defer provider.Destroy()
	//注册服务实例请求
	request := &api.InstanceRegisterRequest{}
	request.Namespace = namespace
	request.Service = service
	request.ServiceToken = token
	request.Host = address.String()
	request.Port = port
	resp, err := provider.Register(request)
	if err != nil {
		log.Fatalf("fail to register instance, err: %v", err)
	} else {
		log.Printf("success to register %s:%d to polaris, instance id is %s",
			request.Host, request.Port, resp.InstanceID)
	}

	//启动http server
	handler := &httpHandler{address: fmt.Sprintf("%s:%d", address.String(), port)}
	http.Handle("/", handler)
	http.ListenAndServe(handler.address, nil)
}
