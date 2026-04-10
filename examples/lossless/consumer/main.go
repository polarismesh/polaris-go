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
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/polarismesh/polaris-go"
	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/pb"
)

var (
	selfNamespace       string
	selfService         string
	selfRegister        bool
	calleeNamespace     string
	calleeService       string
	port                int
	token               string
	debug               bool
	defaultLoopCount    int
	defaultLoopInterval int // 毫秒
)

func initArgs() {
	flag.StringVar(&selfNamespace, "selfNamespace", "default", "selfNamespace")
	flag.StringVar(&selfService, "selfService", "LosslessCaller", "selfService")
	flag.BoolVar(&selfRegister, "selfRegister", true, "selfRegister")
	flag.StringVar(&calleeNamespace, "calleeNamespace", "default", "calleeNamespace")
	flag.StringVar(&calleeService, "calleeService", "LosslessTimeDelayServer", "calleeService")
	flag.IntVar(&port, "port", 18080, "port")
	flag.StringVar(&token, "token", "", "token")
	flag.BoolVar(&debug, "debug", false, "debug")
	flag.IntVar(&defaultLoopCount, "loopCount", 100, "echo-loop 默认请求次数")
	flag.IntVar(&defaultLoopInterval, "loopInterval", 500, "echo-loop 默认请求间隔(毫秒)")
}

// PolarisClient is a consumer of the circuit breaker calleeService.
type PolarisClient struct {
	provider    polaris.ProviderAPI
	host        string
	isShutdown  bool
	consumer    polaris.ConsumerAPI
	rawConsumer api.ConsumerAPI // 底层ConsumerAPI，用于获取lossless规则等高级接口
	webSvr      *http.Server
}

// reportServiceCallResult 上报服务调用结果的辅助方法
func (svr *PolarisClient) reportServiceCallResult(instance model.Instance, retStatus model.RetStatus, statusCode int,
	delay time.Duration) {
	ret := &polaris.ServiceCallResult{
		ServiceCallResult: model.ServiceCallResult{
			EmptyInstanceGauge: model.EmptyInstanceGauge{},
			CalledInstance:     instance,
			Method:             "/echo",
			RetStatus:          retStatus,
		},
	}
	ret.SetDelay(delay)
	ret.SetRetCode(int32(statusCode))
	if err := svr.consumer.UpdateServiceCallResult(ret); err != nil {
		log.Printf("do report service call result : %+v", err)
	} else {
		log.Printf("report service call result success: instance=%s:%d, status=%v, retCode=%d, delay=%v",
			instance.GetHost(), instance.GetPort(), ret.RetStatus, ret.GetRetCode(), delay)
	}
}

func (svr *PolarisClient) discoverInstance() (model.Instance, error) {
	svr.printAllInstances()
	getOneRequest := &polaris.GetOneInstanceRequest{}
	getOneRequest.Namespace = calleeNamespace
	getOneRequest.Service = calleeService
	oneInstResp, err := svr.consumer.GetOneInstance(getOneRequest)
	if err != nil {
		log.Printf("[error] fail to getOneInstance, err is %v", err)
		return nil, err
	}
	instance := oneInstResp.GetInstance()
	if instance == nil {
		log.Printf("[error] fail to getOneInstance, instance is nil")
		return nil, fmt.Errorf("Consumer.GetOneInstance empty")
	}
	log.Printf("getOneInstance is %s:%d, ishealthy:%v", instance.GetHost(), instance.GetPort(), instance.IsHealthy())
	return instance, nil
}

func (svr *PolarisClient) runWebServer() {
	// /lossless-rule 接口：返回被调服务的无损上线规则（包含延迟注册、预热等配置）
	http.HandleFunc("/lossless-rule", func(rw http.ResponseWriter, r *http.Request) {
		ruleReq := &api.GetServiceRuleRequest{
			GetServiceRuleRequest: model.GetServiceRuleRequest{
				Namespace: calleeNamespace,
				Service:   calleeService,
			},
		}
		ruleResp, err := svr.rawConsumer.GetLosslessRule(ruleReq)
		if err != nil {
			rw.WriteHeader(http.StatusInternalServerError)
			_, _ = rw.Write([]byte(fmt.Sprintf(`{"error":"%v"}`, err)))
			return
		}

		// 解析规则中的延迟注册、预热、就绪检查和无损下线配置
		type delayRegisterInfo struct {
			Enable                     bool   `json:"enable"`
			Strategy                   string `json:"strategy"`
			IntervalSeconds            int64  `json:"intervalSeconds"`
			HealthCheckIntervalSeconds string `json:"healthCheckIntervalSeconds,omitempty"`
			HealthCheckPath            string `json:"healthCheckPath,omitempty"`
			HealthCheckProtocol        string `json:"healthCheckProtocol,omitempty"`
			HealthCheckMethod          string `json:"healthCheckMethod,omitempty"`
		}
		type warmupInfo struct {
			Enable                      bool  `json:"enable"`
			IntervalSeconds             int64 `json:"intervalSeconds"`
			Curvature                   int32 `json:"curvature"`
			EnableOverloadProtection    bool  `json:"enableOverloadProtection"`
			OverloadProtectionThreshold int32 `json:"overloadProtectionThreshold"`
		}
		type readinessInfo struct {
			Enable bool `json:"enable"`
		}
		type losslessOfflineInfo struct {
			Enable bool `json:"enable"`
		}
		type ruleInfo struct {
			DelayRegister   *delayRegisterInfo   `json:"delayRegister,omitempty"`
			Warmup          *warmupInfo          `json:"warmup,omitempty"`
			Readiness       *readinessInfo       `json:"readiness,omitempty"`
			LosslessOffline *losslessOfflineInfo `json:"losslessOffline,omitempty"`
		}
		type losslessRuleResponse struct {
			Namespace string     `json:"namespace"`
			Service   string     `json:"service"`
			Rules     []ruleInfo `json:"rules"`
		}

		result := losslessRuleResponse{
			Namespace: calleeNamespace,
			Service:   calleeService,
		}

		if ruleResp != nil && ruleResp.GetValue() != nil {
			if wrapper, ok := ruleResp.GetValue().(*pb.LosslessRuleWrapper); ok && wrapper != nil {
				for _, rule := range wrapper.Rules {
					info := ruleInfo{}
					if online := rule.GetLosslessOnline(); online != nil {
						if dr := online.GetDelayRegister(); dr != nil {
							drInfo := &delayRegisterInfo{
								Enable:          dr.GetEnable(),
								Strategy:        dr.GetStrategy().String(),
								IntervalSeconds: int64(dr.GetIntervalSecond()),
							}
							// 补充健康检查相关字段（DELAY_BY_HEALTH_CHECK 策略使用）
							if dr.GetHealthCheckIntervalSecond() != "" {
								drInfo.HealthCheckIntervalSeconds = dr.GetHealthCheckIntervalSecond()
							}
							if dr.GetHealthCheckPath() != "" {
								drInfo.HealthCheckPath = dr.GetHealthCheckPath()
							}
							if dr.GetHealthCheckProtocol() != "" {
								drInfo.HealthCheckProtocol = dr.GetHealthCheckProtocol()
							}
							if dr.GetHealthCheckMethod() != "" {
								drInfo.HealthCheckMethod = dr.GetHealthCheckMethod()
							}
							info.DelayRegister = drInfo
						}
						if w := online.GetWarmup(); w != nil {
							curvature := w.GetCurvature()
							if curvature == 0 {
								curvature = 2 // 默认曲线系数
							}
							info.Warmup = &warmupInfo{
								Enable:                      w.GetEnable(),
								IntervalSeconds:             int64(w.GetIntervalSecond()),
								Curvature:                   curvature,
								EnableOverloadProtection:    w.GetEnableOverloadProtection(),
								OverloadProtectionThreshold: w.GetOverloadProtectionThreshold(),
							}
						}
						if rd := online.GetReadiness(); rd != nil {
							info.Readiness = &readinessInfo{
								Enable: rd.GetEnable(),
							}
						}
					}
					if offline := rule.GetLosslessOffline(); offline != nil {
						info.LosslessOffline = &losslessOfflineInfo{
							Enable: offline.GetEnable(),
						}
					}
					result.Rules = append(result.Rules, info)
				}
			}
		}

		rw.Header().Set("Content-Type", "application/json")
		rw.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(rw).Encode(result)
	})

	// /instances 接口：返回当前从北极星获取到的所有实例列表（JSON 格式）
	http.HandleFunc("/instances", func(rw http.ResponseWriter, r *http.Request) {
		req := &polaris.GetInstancesRequest{
			GetInstancesRequest: model.GetInstancesRequest{
				Service:   calleeService,
				Namespace: calleeNamespace,
			},
		}
		instancesResp, err := svr.consumer.GetInstances(req)
		if err != nil {
			rw.WriteHeader(http.StatusInternalServerError)
			_, _ = rw.Write([]byte(fmt.Sprintf(`{"error":"%v"}`, err)))
			return
		}
		type instanceInfo struct {
			Host    string `json:"host"`
			Port    int    `json:"port"`
			Healthy bool   `json:"healthy"`
			Weight  int    `json:"weight"`
		}
		var instances []instanceInfo
		for _, ins := range instancesResp.GetInstances() {
			instances = append(instances, instanceInfo{
				Host:    ins.GetHost(),
				Port:    int(ins.GetPort()),
				Healthy: ins.IsHealthy(),
				Weight:  ins.GetWeight(),
			})
		}
		rw.Header().Set("Content-Type", "application/json")
		rw.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(rw).Encode(instances)
	})

	http.HandleFunc("/echo", func(rw http.ResponseWriter, r *http.Request) {
		log.Printf("start to invoke getOneInstance operation")
		instance, err := svr.discoverInstance()
		if err != nil || instance == nil {
			rw.WriteHeader(http.StatusInternalServerError)
			instanceIsNil := instance == nil
			msg := fmt.Sprintf("fail to getOneInstance, err is %v, instance is nil:%v", err, instanceIsNil)
			_, _ = rw.Write([]byte(fmt.Sprintf("[error] discover instance fail : %s", msg)))
			return
		}
		start := time.Now()
		resp, err := http.Get(fmt.Sprintf("http://%s:%d/echo", instance.GetHost(), instance.GetPort()))

		if err != nil {
			log.Printf("[error] send request to %s:%d fail : %s", instance.GetHost(), instance.GetPort(), err)
			rw.WriteHeader(http.StatusInternalServerError)
			_, _ = rw.Write([]byte(fmt.Sprintf("[error] send request to %s:%d fail : %s", instance.GetHost(),
				instance.GetPort(), err)))
			time.Sleep(time.Millisecond * time.Duration(rand.Intn(10)))
			// 上报服务调用结果
			delay := time.Since(start)
			svr.reportServiceCallResult(instance, model.RetFail, http.StatusInternalServerError, delay)
			return
		}
		time.Sleep(time.Millisecond * time.Duration(rand.Intn(10)))

		defer resp.Body.Close()

		// 上报服务调用结果
		delay := time.Since(start)
		retStatus := model.RetSuccess
		if resp.StatusCode == http.StatusTooManyRequests {
			retStatus = model.RetFlowControl
		} else if resp.StatusCode != http.StatusOK {
			retStatus = model.RetFail
		}
		svr.reportServiceCallResult(instance, retStatus, resp.StatusCode, delay)

		data, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Printf("[error] read resp from %s:%d fail : %s", instance.GetHost(), instance.GetPort(), err)
			rw.WriteHeader(http.StatusInternalServerError)
			_, _ = rw.Write([]byte(fmt.Sprintf("[error] read resp from %s:%d fail : %s",
				instance.GetHost(), instance.GetPort(), err)))
			return
		}
		log.Printf("echo success, resp: %+v", string(data))
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write(data)
	})

	// /echo-loop 接口：收到一次请求后持续请求下游，支持指定请求次数和请求间隔
	// 参数:
	//   count    - 请求次数（默认使用 --loopCount 启动参数值）
	//   interval - 请求间隔毫秒（默认使用 --loopInterval 启动参数值）
	// 每10秒在日志中输出下游被调的统计比例
	http.HandleFunc("/echo-loop", func(rw http.ResponseWriter, r *http.Request) {
		// 解析请求参数
		loopCount := defaultLoopCount
		loopIntervalMs := defaultLoopInterval
		if v := r.URL.Query().Get("count"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				loopCount = n
			}
		}
		if v := r.URL.Query().Get("interval"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				loopIntervalMs = n
			}
		}

		log.Printf("[echo-loop] 开始持续请求: count=%d, interval=%dms", loopCount, loopIntervalMs)

		// 使用 SSE (Server-Sent Events) 实时推送统计结果
		rw.Header().Set("Content-Type", "text/event-stream")
		rw.Header().Set("Cache-Control", "no-cache")
		rw.Header().Set("Connection", "keep-alive")
		rw.WriteHeader(http.StatusOK)

		flusher, canFlush := rw.(http.Flusher)

		// 统计数据：key=host:port, value=请求计数
		var statMu sync.Mutex
		instanceStats := make(map[string]int)
		var totalCount int64
		var failCount int64

		// 上一次输出统计的时间
		lastStatTime := time.Now()
		// 区间统计（每10秒重置）
		intervalStats := make(map[string]int)
		var intervalTotal int64

		// 输出统计的辅助函数
		printStats := func(tag string) {
			statMu.Lock()
			defer statMu.Unlock()

			total := atomic.LoadInt64(&totalCount)
			fails := atomic.LoadInt64(&failCount)
			iTotal := atomic.LoadInt64(&intervalTotal)

			// 按端口排序输出
			type portStat struct {
				Addr         string
				Count        int
				Ratio        float64
				IntervalCnt  int
				IntervalRate float64
			}
			var stats []portStat
			for addr, cnt := range instanceStats {
				ratio := 0.0
				if total > 0 {
					ratio = float64(cnt) * 100.0 / float64(total)
				}
				iCnt := intervalStats[addr]
				iRate := 0.0
				if iTotal > 0 {
					iRate = float64(iCnt) * 100.0 / float64(iTotal)
				}
				stats = append(stats, portStat{
					Addr: addr, Count: cnt, Ratio: ratio,
					IntervalCnt: iCnt, IntervalRate: iRate,
				})
			}
			sort.Slice(stats, func(i, j int) bool {
				return stats[i].Addr < stats[j].Addr
			})

			// 构建日志和SSE输出
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("[echo-loop] [%s] 总请求=%d, 失败=%d | ", tag, total, fails))
			for _, s := range stats {
				sb.WriteString(fmt.Sprintf("%s: %d(%.1f%%) 区间:%d(%.1f%%) | ",
					s.Addr, s.Count, s.Ratio, s.IntervalCnt, s.IntervalRate))
			}
			log.Print(sb.String())

			// 构建 JSON 统计数据通过 SSE 推送
			type instanceStatJSON struct {
				Addr         string  `json:"addr"`
				Count        int     `json:"count"`
				Ratio        float64 `json:"ratio"`
				IntervalCnt  int     `json:"intervalCount"`
				IntervalRate float64 `json:"intervalRate"`
			}
			type statEvent struct {
				Tag       string             `json:"tag"`
				Total     int64              `json:"total"`
				Fail      int64              `json:"fail"`
				Instances []instanceStatJSON `json:"instances"`
			}
			evt := statEvent{Tag: tag, Total: total, Fail: fails}
			for _, s := range stats {
				evt.Instances = append(evt.Instances, instanceStatJSON{
					Addr: s.Addr, Count: s.Count, Ratio: s.Ratio,
					IntervalCnt: s.IntervalCnt, IntervalRate: s.IntervalRate,
				})
			}
			evtJSON, _ := json.Marshal(evt)
			fmt.Fprintf(rw, "data: %s\n\n", evtJSON)
			if canFlush {
				flusher.Flush()
			}

			// 重置区间统计
			for k := range intervalStats {
				delete(intervalStats, k)
			}
			atomic.StoreInt64(&intervalTotal, 0)
		}

		for i := 0; i < loopCount; i++ {
			instance, err := svr.discoverInstance()
			if err != nil || instance == nil {
				atomic.AddInt64(&failCount, 1)
				atomic.AddInt64(&totalCount, 1)
				atomic.AddInt64(&intervalTotal, 1)
				log.Printf("[echo-loop] request #%d: discover instance fail: %v", i+1, err)
				time.Sleep(time.Duration(loopIntervalMs) * time.Millisecond)
				continue
			}

			addr := fmt.Sprintf("%s:%d", instance.GetHost(), instance.GetPort())
			start := time.Now()
			resp, err := http.Get(fmt.Sprintf("http://%s/echo", addr))

			atomic.AddInt64(&totalCount, 1)
			atomic.AddInt64(&intervalTotal, 1)

			if err != nil {
				atomic.AddInt64(&failCount, 1)
				delay := time.Since(start)
				svr.reportServiceCallResult(instance, model.RetFail, http.StatusInternalServerError, delay)
				log.Printf("[echo-loop] request #%d to %s fail: %v", i+1, addr, err)
			} else {
				delay := time.Since(start)
				retStatus := model.RetSuccess
				if resp.StatusCode == http.StatusTooManyRequests {
					retStatus = model.RetFlowControl
				} else if resp.StatusCode != http.StatusOK {
					retStatus = model.RetFail
				}
				svr.reportServiceCallResult(instance, retStatus, resp.StatusCode, delay)
				_, _ = io.ReadAll(resp.Body)
				resp.Body.Close()

				statMu.Lock()
				instanceStats[addr]++
				intervalStats[addr]++
				statMu.Unlock()
			}

			// 每10秒输出一次统计
			if time.Since(lastStatTime) >= 10*time.Second {
				elapsed := time.Since(lastStatTime).Truncate(time.Second)
				printStats(fmt.Sprintf("%v elapsed", elapsed))
				lastStatTime = time.Now()
			}

			time.Sleep(time.Duration(loopIntervalMs) * time.Millisecond)
		}

		// 最终统计
		printStats("final")
		log.Printf("[echo-loop] 持续请求完成: count=%d", loopCount)
	})

	// /echo-loop-stop 接口：预留用于未来支持中途停止（当前版本不需要）

	ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		log.Fatalf("[ERROR]fail to listen tcp, err is %v", err)
	}

	go func() {
		log.Printf("[INFO] start http server, listen port is %v", ln.Addr().(*net.TCPAddr).Port)
		if err := svr.webSvr.Serve(ln); err != nil {
			svr.isShutdown = false
			log.Fatalf("[ERROR]fail to run webServer, err is %v", err)
		}
	}()
}

func (svr *PolarisClient) printAllInstances() {
	req := &polaris.GetInstancesRequest{
		GetInstancesRequest: model.GetInstancesRequest{
			Service:   calleeService,
			Namespace: calleeNamespace,
		},
	}
	instancesResp, err := svr.consumer.GetInstances(req)
	if err != nil {
		log.Printf("[error] fail to getInstances for request [%+v], err is %v\n", req, err)
		return
	}
	log.Printf("printAllInstances get [%d] instances", len(instancesResp.GetInstances()))

	for _, ins := range instancesResp.GetInstances() {
		cbStatus := "close"
		if ins.GetCircuitBreakerStatus() != nil {
			cbStatus = ins.GetCircuitBreakerStatus().GetStatus().String()
		}
		log.Printf("%s:%d. cb status: %s\n", ins.GetHost(), ins.GetPort(), cbStatus)
	}
}

func (svr *PolarisClient) runMainLoop() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, []os.Signal{
		syscall.SIGINT, syscall.SIGTERM,
		syscall.SIGSEGV,
	}...)

	for s := range ch {
		log.Printf("catch signal(%+v), stop servers", s)
		if selfRegister {
			svr.isShutdown = true
			svr.deregisterService()
		}
		_ = svr.webSvr.Close()
		return
	}
}

func (svr *PolarisClient) registerService() {
	log.Printf("start to invoke register operation")
	tmpHost, err := getLocalHost(svr.provider.SDKContext().GetConfig().GetGlobal().GetServerConnector().GetAddresses()[0])
	if err != nil {
		panic(fmt.Errorf("error occur while fetching localhost: %v", err))
	}
	svr.host = tmpHost
	registerRequest := &polaris.InstanceRegisterRequest{}
	registerRequest.Service = selfService
	registerRequest.Namespace = selfNamespace
	registerRequest.Host = tmpHost
	registerRequest.Port = port
	registerRequest.ServiceToken = token
	registerRequest.SetTTL(1)
	log.Printf("registerRequest: %+v", jsonStr(registerRequest))
	resp, err := svr.provider.LosslessRegister(registerRequest)
	if err != nil {
		log.Fatalf("fail to register instance, err is %v", err)
	}
	log.Printf("register response: instanceId %s", resp.InstanceID)
}

func (svr *PolarisClient) deregisterService() {
	log.Printf("start to invoke deregister operation")
	deregisterRequest := &polaris.InstanceDeRegisterRequest{}
	deregisterRequest.Service = selfService
	deregisterRequest.Namespace = selfNamespace
	deregisterRequest.Host = svr.host
	deregisterRequest.Port = port
	deregisterRequest.ServiceToken = token
	if err := svr.provider.Deregister(deregisterRequest); err != nil {
		log.Fatalf("fail to deregister instance, err is %v", err)
	}
	log.Printf("deregister successfully.")
}

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	initArgs()
	flag.Parse()
	if len(calleeNamespace) == 0 || len(calleeService) == 0 {
		log.Print("calleeNamespace and calleeService are required")
		return
	}
	if debug {
		// 设置日志级别为DEBUG
		if err := api.SetLoggersLevel(api.DebugLog); err != nil {
			log.Printf("fail to set log level to DEBUG, err is %v", err)
		} else {
			log.Printf("successfully set log level to DEBUG")
		}
	}

	provider, err := polaris.NewProviderAPI()
	if err != nil {
		log.Printf("fail to create provider API, err is %v", err)
		return
	}
	defer provider.Destroy()
	consumer, err := polaris.NewConsumerAPI()
	if err != nil {
		log.Printf("fail to create consumer API, err is %v", err)
		return
	}
	defer consumer.Destroy()

	// 创建底层ConsumerAPI，用于获取lossless规则等高级接口
	rawConsumer := api.NewConsumerAPIByContext(consumer.SDKContext())

	svr := &PolarisClient{
		provider:    provider,
		consumer:    consumer,
		rawConsumer: rawConsumer,
		webSvr:      &http.Server{},
	}
	if selfRegister {
		svr.registerService()
	}
	svr.runWebServer()
	svr.runMainLoop()
}

func getLocalHost(serverAddr string) (string, error) {
	conn, err := net.Dial("tcp", serverAddr)
	if err != nil {
		return "", err
	}
	localAddr := conn.LocalAddr().String()
	colonIdx := strings.LastIndex(localAddr, ":")
	if colonIdx > 0 {
		return localAddr[:colonIdx], nil
	}
	return localAddr, nil
}

func jsonStr(v interface{}) string {
	str, _ := json.Marshal(v)
	return string(str)
}
