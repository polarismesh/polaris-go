package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	apiservice "github.com/polarismesh/specification/source/go/api/v1/service_manage"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
	"gopkg.in/yaml.v3"
)

var (
	polarisServerAddress string
	polarisToken         string
	namespace            string
	service              string
	port                 string
)

func init() {
	flag.StringVar(&polarisServerAddress, "polaris-server-address", "127.0.0.1:8091", "北极星服务端地址")
	flag.StringVar(&polarisToken, "polaris-token", "", "北极星鉴权 Token")
	flag.StringVar(&namespace, "namespace", "default", "默认命名空间")
	flag.StringVar(&service, "service", "provider-demo", "默认服务名")
	flag.StringVar(&port, "port", ":38080", "HTTP 监听地址")
}

// getParamValue 获取参数值，优先从请求体获取，如果为空则使用默认值
func getParamValue(reqValue, defaultValue string) string {
	if reqValue != "" {
		return reqValue
	}
	return defaultValue
}

var consumerAPI api.ConsumerAPI

func main() {
	flag.Parse()

	cfg, err := LoadConfiguration(func(impl *config.ConfigurationImpl) {
		impl.Global.ServerConnector.Addresses = []string{polarisServerAddress}
		impl.Global.ServerConnector.Token = polarisToken
		report := false
		impl.Global.StatReporter.Enable = &report
		// 增加超时时间
		timeout := 10 * time.Second
		impl.Consumer.LocalCache.ServiceRefreshInterval = &timeout
		// 开启调试日志
		impl.Global.ServerConnector.Protocol = "grpc"
	})
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	consumerAPI, err = api.NewConsumerAPIByConfig(cfg)
	if err != nil {
		log.Fatalf("创建 ConsumerAPI 失败: %v", err)
	}

	log.Println("等待 SDK 初始化...")
	time.Sleep(1 * time.Second)

	r := gin.New()
	r.Use(gin.Logger())
	r.Use(gin.Recovery())

	r.GET("/", defaultHandler)
	r.GET("/auth", authHandler)
	r.GET("/lossless", losslessHandler)
	r.GET("/lane", laneHandler)
	r.GET("/circuitbreaker", circuitBreakerHandler)

	log.Printf("listening at %s\n", port)
	if err := r.Run(port); err != nil {
		log.Fatalf("启动 HTTP 服务失败: %v", err)
	}
}

func defaultHandler(c *gin.Context) {
	helpText := `Polaris ServiceRule 服务治理规则查询 API

可用接口:
- GET /auth            - 查询鉴权规则（黑白名单规则）
- GET /warmup          - 查询预热规则（无损上下线规则）
- GET /lane            - 查询泳道规则
- GET /circuitbreaker  - 查询熔断规则（支持查询主调/被调服务下的熔断策略）

请求参数（JSON Body）:
{
  "namespace": "default",
  "service": "provider-demo",
  "direction": "callee"  // 可选值: caller / callee，默认 callee
}

如果请求体中未传入参数，将使用启动参数的默认值。

启动参数示例:
  -polaris-server-address  北极星服务端地址（默认 127.0.0.1:8091）
  -polaris-token           北极星鉴权 Token
  -namespace               默认命名空间（默认 default）
  -service                 默认服务名（默认 provider-demo）
  -port                    HTTP 监听地址（默认 :38080）
`
	c.String(http.StatusOK, helpText)
}

type ruleRequest struct {
	Namespace string `json:"namespace"`
	Service   string `json:"service"`
	Direction string `json:"direction"` // 仅 circuitbreaker 使用，可选值: caller / callee，默认 callee
}

func authHandler(c *gin.Context) {
	var req ruleRequest
	c.ShouldBindJSON(&req)

	finalNamespace := getParamValue(req.Namespace, namespace)
	finalService := getParamValue(req.Service, service)

	timeout := time.Second * 30
	ruleReq := api.GetServiceRuleRequest{
		GetServiceRuleRequest: model.GetServiceRuleRequest{
			Namespace: finalNamespace,
			Service:   finalService,
			Timeout:   &timeout,
		},
	}
	resp, err := consumerAPI.GetBlockAllowRule(&ruleReq)
	if err != nil {
		log.Printf("[Auth] 查询失败 - namespace: %s, service: %s, err: %v", finalNamespace, finalService, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	log.Printf("[Auth] 查询成功 - namespace: %s, service: %s", finalNamespace, finalService)
	buf, _ := json.Marshal(resp)
	c.Data(http.StatusOK, "application/json", buf)
}

func losslessHandler(c *gin.Context) {
	var req ruleRequest
	c.ShouldBindJSON(&req)

	finalNamespace := getParamValue(req.Namespace, namespace)
	finalService := getParamValue(req.Service, service)

	timeout := time.Second * 30
	ruleReq := api.GetServiceRuleRequest{
		GetServiceRuleRequest: model.GetServiceRuleRequest{
			Namespace: finalNamespace,
			Service:   finalService,
			Timeout:   &timeout,
		},
	}
	resp, err := consumerAPI.GetLosslessRule(&ruleReq)
	if err != nil {
		log.Printf("[Warmup] 查询失败 - namespace: %s, service: %s, err: %v", finalNamespace, finalService, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	log.Printf("[Warmup] 查询成功 - namespace: %s, service: %s", finalNamespace, finalService)
	buf, _ := json.Marshal(resp)
	c.Data(http.StatusOK, "application/json", buf)
}

func laneHandler(c *gin.Context) {
	var req ruleRequest
	c.ShouldBindJSON(&req)

	finalNamespace := getParamValue(req.Namespace, namespace)
	finalService := getParamValue(req.Service, service)

	timeout := time.Second * 30
	ruleReq := api.GetServiceRuleRequest{
		GetServiceRuleRequest: model.GetServiceRuleRequest{
			Namespace: finalNamespace,
			Service:   finalService,
			Timeout:   &timeout,
		},
	}
	resp, err := consumerAPI.GetLane(&ruleReq)
	if err != nil {
		log.Printf("[Lane] 查询失败 - namespace: %s, service: %s, err: %v", finalNamespace, finalService, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	log.Printf("[Lane] 查询成功 - namespace: %s, service: %s", finalNamespace, finalService)
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

	finalNamespace := getParamValue(req.Namespace, namespace)
	finalService := getParamValue(req.Service, service)
	finalDirection := parseDirection(req.Direction)

	timeout := time.Second * 30
	ruleReq := &api.GetServiceRuleRequest{
		GetServiceRuleRequest: model.GetServiceRuleRequest{
			Namespace: finalNamespace,
			Service:   finalService,
			Timeout:   &timeout,
			Direction: finalDirection,
		},
	}
	resp, err := consumerAPI.GetCircuitBreakerRule(ruleReq)
	if err != nil {
		log.Printf("[CircuitBreaker] 查询失败 - namespace: %s, service: %s, direction: %s, err: %v", finalNamespace, finalService, finalDirection, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	log.Printf("[CircuitBreaker] 查询成功 - namespace: %s, service: %s, direction: %s", finalNamespace, finalService, finalDirection)
	buf, _ := yaml.Marshal(resp)
	c.Data(http.StatusOK, "application/x-yaml", buf)
}

func LoadConfiguration(set func(*config.ConfigurationImpl)) (*config.ConfigurationImpl, error) {
	var err error
	cfg := &config.ConfigurationImpl{}
	cfg.Init()
	set(cfg)
	cfg.SetDefault()
	if err = cfg.Verify(); err != nil {
		return nil, fmt.Errorf("fail to verify config string")
	}
	return cfg, nil
}
