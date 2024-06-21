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
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/polarismesh/polaris-go"
	"github.com/polarismesh/polaris-go/pkg/model"
)

var (
	namespace string
	service   string
)

func initArgs() {
	flag.StringVar(&namespace, "namespace", "default", "namespace")
	flag.StringVar(&service, "service", "DiscoverEchoServer", "service")
}

// PolarisProvider is an example of provider
type PolarisProvider struct {
	provider  polaris.ProviderAPI
	namespace string
	service   string
	cancel    context.CancelFunc
}

// Run starts the provider
func (svr *PolarisProvider) Run() {
	ctx, cancel := context.WithCancel(context.Background())
	svr.registerService()
	go svr.watchInstances(ctx)
	svr.cancel = cancel
}

func (svr *PolarisProvider) watchInstances(ctx context.Context) {
	consumer := polaris.NewConsumerAPIByContext(svr.provider.SDKContext())
	rsp, err := consumer.WatchService(&polaris.WatchServiceRequest{
		WatchServiceRequest: model.WatchServiceRequest{
			Key: model.ServiceKey{
				Namespace: namespace,
				Service:   service,
			},
		},
	})
	if err != nil {
		panic(err)0
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-rsp.EventChannel:
			healthCnt := 0
			insRsp, err := consumer.GetAllInstances(&polaris.GetAllInstancesRequest{
				GetAllInstancesRequest: model.GetAllInstancesRequest{
					Namespace: namespace,
					Service:   service,
				},
			})
			if err != nil {
				log.Println(err.Error())
				continue
			}

			for i := range insRsp.Instances {
				if insRsp.Instances[i].IsHealthy() {
					healthCnt++
				}
			}
			log.Printf("health count: %d, total count: %d", healthCnt, len(insRsp.Instances))
		}
	}
}

func (svr *PolarisProvider) registerService() {
	log.Printf("start to invoke register operation")
	registerRequest := &polaris.InstanceRegisterRequest{}
	registerRequest.Service = service
	registerRequest.Namespace = namespace
	registerRequest.Host = os.Getenv("INSTANCE_IP")
	registerRequest.Port = 8080
	registerRequest.SetTTL(5)
	resp, err := svr.provider.RegisterInstance(registerRequest)
	if err != nil {
		log.Fatalf("fail to register instance, err is %v", err)
	}
	log.Printf("register response: instanceId %s", resp.InstanceID)
}

func (svr *PolarisProvider) deregisterService() {
	log.Printf("start to invoke deregister operation")
	deregisterRequest := &polaris.InstanceDeRegisterRequest{}
	deregisterRequest.Service = service
	deregisterRequest.Namespace = namespace
	deregisterRequest.Host = os.Getenv("INSTANCE_IP")
	deregisterRequest.Port = 8080
	if err := svr.provider.Deregister(deregisterRequest); err != nil {
		log.Fatalf("fail to deregister instance, err is %v", err)
	}
	log.Printf("deregister successfully.")
}

func (svr *PolarisProvider) runMainLoop() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, []os.Signal{
		syscall.SIGINT, syscall.SIGTERM,
		syscall.SIGSEGV,
	}...)

	for s := range ch {
		log.Printf("catch signal(%+v), stop servers", s)
		svr.deregisterService()
		svr.cancel()
		return
	}
}

func main() {
	initArgs()
	flag.Parse()
	if len(namespace) == 0 || len(service) == 0 {
		log.Print("namespace and service are required")
		return
	}
	// provider, err := polaris.NewProviderAPI()
	// 或者使用以下方法,则不需要创建配置文件
	provider, err := polaris.NewProviderAPIByAddress(os.Getenv("POLARIS_ADDRESS"))

	if err != nil {
		log.Fatalf("fail to create providerAPI, err is %v", err)
	}
	defer provider.Destroy()

	svr := &PolarisProvider{
		provider:  provider,
		namespace: namespace,
		service:   service,
	}

	svr.Run()
	svr.runMainLoop()
}
