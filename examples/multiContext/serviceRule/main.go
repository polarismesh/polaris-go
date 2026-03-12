package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/gin-gonic/gin"
	apiservice "github.com/polarismesh/specification/source/go/api/v1/service_manage"

	"github.com/polarismesh/polaris-go"
	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
)

var (
	debug        bool
	namespaceVal string
	serviceVal   string
	clientsVal   string
	port         string
)

func init() {
	flag.BoolVar(&debug, "debug", false, "是否开启调试模式")
	flag.StringVar(&namespaceVal, "namespace", "default", "namespace")
	flag.StringVar(&serviceVal, "service", "provider-demo", "service name")
	flag.StringVar(&clientsVal, "clients", "", "clientId与server地址映射，格式: clientId=serverAddr，多个用逗号分隔，例如: ctxA=10.0.0.1,ctxB=10.0.0.2")
	flag.StringVar(&port, "port", ":38080", "HTTP 监听地址")
}

// parseClients 解析 -clients 参数，返回 clientId -> serverAddr 的映射
func parseClients(clientsStr string) (map[string]string, error) {
	result := make(map[string]string)
	if clientsStr == "" {
		return nil, fmt.Errorf("-clients 参数不能为空，格式: clientId=serverAddr，多个用逗号分隔")
	}
	pairs := strings.Split(clientsStr, ",")
	for _, pair := range pairs {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return nil, fmt.Errorf("无效的 client 配置: %q，格式应为 clientId=serverAddr", pair)
		}
		clientId := strings.TrimSpace(parts[0])
		serverAddr := strings.TrimSpace(parts[1])
		if _, exists := result[clientId]; exists {
			return nil, fmt.Errorf("重复的 clientId: %s", clientId)
		}
		result[clientId] = serverAddr
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("-clients 参数解析后为空，格式: clientId=serverAddr，多个用逗号分隔")
	}
	return result, nil
}

// consumerAPIs 存储每个 context 对应的 ConsumerAPI
var consumerAPIs sync.Map // key: clientId(string), value: api.ConsumerAPI

func main() {
	flag.Parse()

	// 解析 -clients 参数
	clientMap, err := parseClients(clientsVal)
	if err != nil {
		log.Fatalf("解析 -clients 参数失败: %v", err)
	}

	log.Printf("Starting serviceRule example with namespace: %s, service: %s", namespaceVal, serviceVal)
	log.Printf("clients: %v", clientMap)
	if debug {
		// 设置日志级别为DEBUG
		if err := api.SetLoggersLevel(api.DebugLog); err != nil {
			log.Printf("fail to set log level to DEBUG, err is %v", err)
		} else {
			log.Printf("successfully set log level to DEBUG")
		}
	}

	// errCh 用于收集 serverProcess 的错误
	errCh := make(chan error, len(clientMap))

	var wg sync.WaitGroup
	wg.Add(len(clientMap))

	for clientId, serverAddr := range clientMap {
		go func(cid, addr string) {
			defer wg.Done()
			if err := serverProcess(addr, cid); err != nil {
				errCh <- fmt.Errorf("%s 异常退出: %v", cid, err)
			}
		}(clientId, serverAddr)
	}

	// 启动 HTTP 服务
	go startHTTPServer()

	// 监听系统信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// 当所有 serverProcess 都结束时通知 doneCh
	doneCh := make(chan struct{})
	go func() {
		wg.Wait()
		close(doneCh)
	}()

	select {
	case sig := <-sigCh:
		log.Printf("收到退出信号: %v，正在退出...", sig)
	case <-doneCh:
		// 所有 serverProcess 都已结束（可能全部出错），收集错误信息
		close(errCh)
		for err := range errCh {
			log.Println(err)
		}
		log.Println("所有 serverProcess 已结束，程序退出")
	}
}

func serverProcess(serverAddr, clientId string) error {
	log.Printf("%s client start, serverAddr=%s, namespace=%s, service=%s", clientId, serverAddr, namespaceVal, serviceVal)
	conf := config.NewDefaultConfiguration([]string{serverAddr + ":8091"})
	conf.GetConsumer().GetLocalCache().SetPersistDir("./cache/" + clientId + "/backup")
	conf.GetGlobal().GetClient().(*config.ClientConfigImpl).AddLabels(map[string]string{
		"uin": clientId,
	})
	sdkContext, err := polaris.NewSDKContextByConfig(conf)
	if err != nil {
		log.Printf("%s fail to create sdk context, serverAddr=%s: %v", clientId, serverAddr, err)
		return err
	}
	idInfoMap := sdkContext.GetConfig().GetGlobal().GetClient().GetLabels()
	idInfoMapStr := printMap(idInfoMap)
	consumerAPI := api.NewConsumerAPIByContext(sdkContext)

	// 将 consumerAPI 注册到全局 map，供 HTTP 接口使用
	consumerAPIs.Store(clientId, consumerAPI)
	log.Printf("%s ConsumerAPI 已注册，可通过 HTTP 接口访问 (clientId=%s)", idInfoMapStr, clientId)

	// === 查询鉴权规则（黑白名单规则） ===
	log.Printf("%s [Auth] 开始查询鉴权规则 - namespace=%s, service=%s, serverAddr=%s",
		idInfoMapStr, namespaceVal, serviceVal, serverAddr)
	authReq := &api.GetServiceRuleRequest{
		GetServiceRuleRequest: model.GetServiceRuleRequest{
			Namespace: namespaceVal,
			Service:   serviceVal,
		},
	}
	authResp, err := consumerAPI.GetBlockAllowRule(authReq)
	if err != nil {
		log.Printf("%s [Auth] 查询失败 - serverAddr=%s: %v", idInfoMapStr, serverAddr, err)
	} else {
		buf, _ := json.MarshalIndent(authResp, "", "  ")
		log.Printf("%s [Auth] 查询成功 - serverAddr=%s, result:\n%s", idInfoMapStr, serverAddr, string(buf))
	}

	// === 查询无损上下线规则 ===
	log.Printf("%s [Lossless] 开始查询无损上下线规则 - namespace=%s, service=%s, serverAddr=%s",
		idInfoMapStr, namespaceVal, serviceVal, serverAddr)
	losslessReq := &api.GetServiceRuleRequest{
		GetServiceRuleRequest: model.GetServiceRuleRequest{
			Namespace: namespaceVal,
			Service:   serviceVal,
		},
	}
	losslessResp, err := consumerAPI.GetLosslessRule(losslessReq)
	if err != nil {
		log.Printf("%s [Lossless] 查询失败 - serverAddr=%s: %v", idInfoMapStr, serverAddr, err)
	} else {
		buf, _ := json.MarshalIndent(losslessResp, "", "  ")
		log.Printf("%s [Lossless] 查询成功 - serverAddr=%s, result:\n%s", idInfoMapStr, serverAddr, string(buf))
	}

	// === 查询泳道规则 ===
	log.Printf("%s [Lane] 开始查询泳道规则 - namespace=%s, service=%s, serverAddr=%s",
		idInfoMapStr, namespaceVal, serviceVal, serverAddr)
	laneReq := &api.GetServiceRuleRequest{
		GetServiceRuleRequest: model.GetServiceRuleRequest{
			Namespace: namespaceVal,
			Service:   serviceVal,
		},
	}
	laneResp, err := consumerAPI.GetLane(laneReq)
	if err != nil {
		log.Printf("%s [Lane] 查询失败 - serverAddr=%s: %v", idInfoMapStr, serverAddr, err)
	} else {
		buf, _ := json.MarshalIndent(laneResp, "", "  ")
		log.Printf("%s [Lane] 查询成功 - serverAddr=%s, result:\n%s", idInfoMapStr, serverAddr, string(buf))
	}

	// === 查询路由规则 ===
	log.Printf("%s [RouteRule] 开始查询路由规则 - namespace=%s, service=%s, serverAddr=%s",
		idInfoMapStr, namespaceVal, serviceVal, serverAddr)
	routeReq := &api.GetServiceRuleRequest{
		GetServiceRuleRequest: model.GetServiceRuleRequest{
			Namespace: namespaceVal,
			Service:   serviceVal,
		},
	}
	routeResp, err := consumerAPI.GetRouteRule(routeReq)
	if err != nil {
		log.Printf("%s [RouteRule] 查询失败 - serverAddr=%s: %v", idInfoMapStr, serverAddr, err)
	} else {
		buf, _ := json.MarshalIndent(routeResp, "", "  ")
		log.Printf("%s [RouteRule] 查询成功 - serverAddr=%s, result:\n%s", idInfoMapStr, serverAddr, string(buf))
	}

	// === 查询限流规则 ===
	log.Printf("%s [RateLimit] 开始查询限流规则 - namespace=%s, service=%s, serverAddr=%s",
		idInfoMapStr, namespaceVal, serviceVal, serverAddr)
	rateLimitReq := &api.GetServiceRuleRequest{
		GetServiceRuleRequest: model.GetServiceRuleRequest{
			Namespace: namespaceVal,
			Service:   serviceVal,
		},
	}
	rateLimitResp, err := consumerAPI.GetRateLimitRule(rateLimitReq)
	if err != nil {
		log.Printf("%s [RateLimit] 查询失败 - serverAddr=%s: %v", idInfoMapStr, serverAddr, err)
	} else {
		buf, _ := json.MarshalIndent(rateLimitResp, "", "  ")
		log.Printf("%s [RateLimit] 查询成功 - serverAddr=%s, result:\n%s", idInfoMapStr, serverAddr, string(buf))
	}

	// === 查询就近路由规则 ===
	log.Printf("%s [NearbyRoute] 开始查询就近路由规则 - namespace=%s, service=%s, serverAddr=%s",
		idInfoMapStr, namespaceVal, serviceVal, serverAddr)
	nearbyReq := &api.GetServiceRuleRequest{
		GetServiceRuleRequest: model.GetServiceRuleRequest{
			Namespace: namespaceVal,
			Service:   serviceVal,
		},
	}
	nearbyResp, err := consumerAPI.GetNearbyRouteRule(nearbyReq)
	if err != nil {
		log.Printf("%s [NearbyRoute] 查询失败 - serverAddr=%s: %v", idInfoMapStr, serverAddr, err)
	} else {
		buf, _ := json.MarshalIndent(nearbyResp, "", "  ")
		log.Printf("%s [NearbyRoute] 查询成功 - serverAddr=%s, result:\n%s", idInfoMapStr, serverAddr, string(buf))
	}

	// === 查询熔断规则 ===
	log.Printf("%s [CircuitBreaker] 开始查询熔断规则 - namespace=%s, service=%s, serverAddr=%s",
		idInfoMapStr, namespaceVal, serviceVal, serverAddr)
	cbReq := &api.GetServiceRuleRequest{
		GetServiceRuleRequest: model.GetServiceRuleRequest{
			Namespace: namespaceVal,
			Service:   serviceVal,
		},
	}
	cbResp, err := consumerAPI.GetCircuitBreakerRule(cbReq)
	if err != nil {
		log.Printf("%s [CircuitBreaker] 查询失败 - serverAddr=%s: %v", idInfoMapStr, serverAddr, err)
	} else {
		buf, _ := json.MarshalIndent(cbResp, "", "  ")
		log.Printf("%s [CircuitBreaker] 查询成功 - serverAddr=%s, result:\n%s", idInfoMapStr, serverAddr, string(buf))
	}

	log.Printf("%s 所有规则查询完成，保持运行中... Press Ctrl+C to exit (serverAddr=%s)",
		idInfoMapStr, serverAddr)
	// 正常情况下阻塞在此，不会返回；由主函数通过信号控制退出
	select {}
}

// ====== HTTP 服务部分 ======

// ruleRequest HTTP 请求参数
type ruleRequest struct {
	Namespace string `json:"namespace"`
	Service   string `json:"service"`
	ClientId  string `json:"client_id"` // 指定使用哪个 context（ctxA 或 ctxB）
	Direction string `json:"direction"` // 仅 circuitbreaker 使用，可选值: caller / callee，默认 callee
}

// getParamValue 获取参数值，优先从请求体获取，如果为空则使用默认值
func getParamValue(reqValue, defaultValue string) string {
	if reqValue != "" {
		return reqValue
	}
	return defaultValue
}

// getConsumerAPI 根据 clientId 获取对应的 ConsumerAPI
func getConsumerAPI(clientId string) (api.ConsumerAPI, error) {
	if clientId == "" {
		// 默认使用 ctxA
		clientId = "ctxA"
	}
	v, ok := consumerAPIs.Load(clientId)
	if !ok {
		// 列出所有可用的 clientId
		available := make([]string, 0)
		consumerAPIs.Range(func(key, value interface{}) bool {
			available = append(available, key.(string))
			return true
		})
		return nil, fmt.Errorf("未找到 clientId=%s 的 ConsumerAPI，可用的 clientId: %v", clientId, available)
	}
	return v.(api.ConsumerAPI), nil
}

func startHTTPServer() {
	r := gin.New()
	r.Use(gin.Logger())
	r.Use(gin.Recovery())

	r.GET("/", defaultHandler)
	r.GET("/auth", authHandler)
	r.GET("/lossless", losslessHandler)
	r.GET("/lane", laneHandler)
	r.GET("/routerule", routeRuleHandler)
	r.GET("/ratelimit", rateLimitHandler)
	r.GET("/nearbyroute", nearbyRouteHandler)
	r.GET("/circuitbreaker", circuitBreakerHandler)

	log.Printf("HTTP 服务启动，监听地址: %s", port)
	if err := r.Run(port); err != nil {
		log.Printf("启动 HTTP 服务失败: %v", err)
	}
}

func defaultHandler(c *gin.Context) {
	helpText := `Polaris MultiContext ServiceRule 服务治理规则查询 API

可用接口:
- GET /auth            - 查询鉴权规则（黑白名单规则）
- GET /lossless        - 查询预热规则（无损上下线规则）
- GET /lane            - 查询泳道规则
- GET /routerule       - 查询路由规则
- GET /ratelimit       - 查询限流规则
- GET /nearbyroute     - 查询就近路由规则
- GET /circuitbreaker  - 查询熔断规则（支持查询主调/被调服务下的熔断策略）

请求参数（JSON Body）:
{
  "namespace": "default",
  "service": "provider-demo",
  "client_id": "ctxA",
  "direction": "callee"
}

参数说明:
- namespace  命名空间（默认使用启动参数值）
- service    服务名（默认使用启动参数值）
- client_id  指定使用哪个 context 查询，可选值: ctxA / ctxB（默认 ctxA）
- direction  仅 circuitbreaker 接口使用，可选值: caller / callee（默认 callee）

如果请求体中未传入参数，将使用启动参数的默认值。

启动参数示例:
  -clients     clientId与server地址映射（格式: clientId=serverAddr，多个用逗号分隔，例如: ctxA=10.0.0.1,ctxB=10.0.0.2）
  -namespace   默认命名空间（默认 default）
  -service     默认服务名（默认 provider-demo）
  -port        HTTP 监听地址（默认 :38080）
  -debug       是否开启调试模式
`
	c.String(http.StatusOK, helpText)
}

func authHandler(c *gin.Context) {
	var req ruleRequest
	c.ShouldBindJSON(&req)

	consumerAPI, err := getConsumerAPI(req.ClientId)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	finalNamespace := getParamValue(req.Namespace, namespaceVal)
	finalService := getParamValue(req.Service, serviceVal)

	ruleReq := &api.GetServiceRuleRequest{
		GetServiceRuleRequest: model.GetServiceRuleRequest{
			Namespace: finalNamespace,
			Service:   finalService,
		},
	}
	resp, err := consumerAPI.GetBlockAllowRule(ruleReq)
	if err != nil {
		log.Printf("[HTTP][Auth] 查询失败 - clientId: %s, namespace: %s, service: %s, err: %v",
			req.ClientId, finalNamespace, finalService, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	log.Printf("[HTTP][Auth] 查询成功 - clientId: %s, namespace: %s, service: %s",
		req.ClientId, finalNamespace, finalService)
	buf, _ := json.Marshal(resp)
	c.Data(http.StatusOK, "application/json", buf)
}

func losslessHandler(c *gin.Context) {
	var req ruleRequest
	c.ShouldBindJSON(&req)

	consumerAPI, err := getConsumerAPI(req.ClientId)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	finalNamespace := getParamValue(req.Namespace, namespaceVal)
	finalService := getParamValue(req.Service, serviceVal)

	ruleReq := &api.GetServiceRuleRequest{
		GetServiceRuleRequest: model.GetServiceRuleRequest{
			Namespace: finalNamespace,
			Service:   finalService,
		},
	}
	resp, err := consumerAPI.GetLosslessRule(ruleReq)
	if err != nil {
		log.Printf("[HTTP][Lossless] 查询失败 - clientId: %s, namespace: %s, service: %s, err: %v",
			req.ClientId, finalNamespace, finalService, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	log.Printf("[HTTP][Lossless] 查询成功 - clientId: %s, namespace: %s, service: %s",
		req.ClientId, finalNamespace, finalService)
	buf, _ := json.Marshal(resp)
	c.Data(http.StatusOK, "application/json", buf)
}

func laneHandler(c *gin.Context) {
	var req ruleRequest
	c.ShouldBindJSON(&req)

	consumerAPI, err := getConsumerAPI(req.ClientId)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	finalNamespace := getParamValue(req.Namespace, namespaceVal)
	finalService := getParamValue(req.Service, serviceVal)

	ruleReq := &api.GetServiceRuleRequest{
		GetServiceRuleRequest: model.GetServiceRuleRequest{
			Namespace: finalNamespace,
			Service:   finalService,
		},
	}
	resp, err := consumerAPI.GetLane(ruleReq)
	if err != nil {
		log.Printf("[HTTP][Lane] 查询失败 - clientId: %s, namespace: %s, service: %s, err: %v",
			req.ClientId, finalNamespace, finalService, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	log.Printf("[HTTP][Lane] 查询成功 - clientId: %s, namespace: %s, service: %s",
		req.ClientId, finalNamespace, finalService)
	buf, _ := json.Marshal(resp)
	c.Data(http.StatusOK, "application/json", buf)
}

func routeRuleHandler(c *gin.Context) {
	var req ruleRequest
	c.ShouldBindJSON(&req)

	consumerAPI, err := getConsumerAPI(req.ClientId)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	finalNamespace := getParamValue(req.Namespace, namespaceVal)
	finalService := getParamValue(req.Service, serviceVal)

	ruleReq := &api.GetServiceRuleRequest{
		GetServiceRuleRequest: model.GetServiceRuleRequest{
			Namespace: finalNamespace,
			Service:   finalService,
		},
	}
	resp, err := consumerAPI.GetRouteRule(ruleReq)
	if err != nil {
		log.Printf("[HTTP][RouteRule] 查询失败 - clientId: %s, namespace: %s, service: %s, err: %v",
			req.ClientId, finalNamespace, finalService, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	log.Printf("[HTTP][RouteRule] 查询成功 - clientId: %s, namespace: %s, service: %s",
		req.ClientId, finalNamespace, finalService)
	buf, _ := json.Marshal(resp)
	c.Data(http.StatusOK, "application/json", buf)
}

func rateLimitHandler(c *gin.Context) {
	var req ruleRequest
	c.ShouldBindJSON(&req)

	consumerAPI, err := getConsumerAPI(req.ClientId)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	finalNamespace := getParamValue(req.Namespace, namespaceVal)
	finalService := getParamValue(req.Service, serviceVal)

	ruleReq := &api.GetServiceRuleRequest{
		GetServiceRuleRequest: model.GetServiceRuleRequest{
			Namespace: finalNamespace,
			Service:   finalService,
		},
	}
	resp, err := consumerAPI.GetRateLimitRule(ruleReq)
	if err != nil {
		log.Printf("[HTTP][RateLimit] 查询失败 - clientId: %s, namespace: %s, service: %s, err: %v",
			req.ClientId, finalNamespace, finalService, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	log.Printf("[HTTP][RateLimit] 查询成功 - clientId: %s, namespace: %s, service: %s",
		req.ClientId, finalNamespace, finalService)
	buf, _ := json.Marshal(resp)
	c.Data(http.StatusOK, "application/json", buf)
}

func nearbyRouteHandler(c *gin.Context) {
	var req ruleRequest
	c.ShouldBindJSON(&req)

	consumerAPI, err := getConsumerAPI(req.ClientId)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	finalNamespace := getParamValue(req.Namespace, namespaceVal)
	finalService := getParamValue(req.Service, serviceVal)

	ruleReq := &api.GetServiceRuleRequest{
		GetServiceRuleRequest: model.GetServiceRuleRequest{
			Namespace: finalNamespace,
			Service:   finalService,
		},
	}
	resp, err := consumerAPI.GetNearbyRouteRule(ruleReq)
	if err != nil {
		log.Printf("[HTTP][NearbyRoute] 查询失败 - clientId: %s, namespace: %s, service: %s, err: %v",
			req.ClientId, finalNamespace, finalService, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	log.Printf("[HTTP][NearbyRoute] 查询成功 - clientId: %s, namespace: %s, service: %s",
		req.ClientId, finalNamespace, finalService)
	buf, _ := json.Marshal(resp)
	c.Data(http.StatusOK, "application/json", buf)
}

// parseDirection 解析 direction 参数，默认返回 Callee
func parseDirection(direction string) apiservice.DiscoverDirection {
	switch strings.ToLower(strings.TrimSpace(direction)) {
	case "caller":
		return apiservice.DiscoverDirection_Caller
	case "callee", "":
		return apiservice.DiscoverDirection_Callee
	default:
		return apiservice.DiscoverDirection_Callee
	}
}

func circuitBreakerHandler(c *gin.Context) {
	var req ruleRequest
	c.ShouldBindJSON(&req)

	consumerAPI, err := getConsumerAPI(req.ClientId)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	finalNamespace := getParamValue(req.Namespace, namespaceVal)
	finalService := getParamValue(req.Service, serviceVal)
	finalDirection := parseDirection(req.Direction)

	ruleReq := &api.GetServiceRuleRequest{
		GetServiceRuleRequest: model.GetServiceRuleRequest{
			Namespace: finalNamespace,
			Service:   finalService,
			Direction: finalDirection,
		},
	}
	resp, err := consumerAPI.GetCircuitBreakerRule(ruleReq)
	if err != nil {
		log.Printf("[HTTP][CircuitBreaker] 查询失败 - clientId: %s, namespace: %s, service: %s, direction: %s, err: %v",
			req.ClientId, finalNamespace, finalService, finalDirection, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	log.Printf("[HTTP][CircuitBreaker] 查询成功 - clientId: %s, namespace: %s, service: %s, direction: %s",
		req.ClientId, finalNamespace, finalService, finalDirection)
	buf, _ := json.Marshal(resp)
	c.Data(http.StatusOK, "application/json", buf)
}

// 需要过滤的 SDK 系统标签
var systemLabels = map[string]bool{
	"CLIENT_ID":       true,
	"CLIENT_VERSION":  true,
	"CLIENT_LANGUAGE": true,
	"CLIENT_IP":       true,
}

func printMap(m map[string]string) string {
	if len(m) == 0 {
		return "empty"
	}
	parts := make([]string, 0, len(m))
	for k, v := range m {
		if systemLabels[k] {
			continue
		}
		parts = append(parts, fmt.Sprintf("{%q:%q}", k, v))
	}
	if len(parts) == 0 {
		return "empty"
	}
	return strings.Join(parts, ", ")
}
