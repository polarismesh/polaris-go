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
	"errors"
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
	"github.com/polarismesh/polaris-go/pkg/model/pb"
)

var (
	polarisServerAddress string
	polarisToken         string
	namespace            string
	service              string
	port                 string
	debug                bool
)

// initArgs 注册命令行参数。
func initArgs() {
	flag.StringVar(&polarisServerAddress, "polaris-server-address", "127.0.0.1:8091", "北极星服务端地址")
	flag.StringVar(&polarisToken, "polaris-token", "", "北极星鉴权 Token")
	flag.StringVar(&namespace, "namespace", "default", "默认命名空间")
	flag.StringVar(&service, "service", "provider-demo", "默认服务名")
	flag.StringVar(&port, "port", ":38080", "HTTP 监听地址")
	flag.BoolVar(&debug, "debug", false, "是否开启 Polaris SDK debug 日志")
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
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)
	initArgs()
	flag.Parse()

	if debug {
		if err := api.SetLoggersLevel(api.DebugLog); err != nil {
			log.Printf("[WARN] 设置日志级别为 DEBUG 失败: %v", err)
		} else {
			log.Printf("[INFO] 已设置 Polaris SDK 日志级别为 DEBUG")
		}
	}

	cfg, err := LoadConfiguration(func(impl *config.ConfigurationImpl) {
		impl.Global.ServerConnector.Addresses = []string{polarisServerAddress}
		impl.Global.ServerConnector.Token = polarisToken
		report := false
		impl.Global.StatReporter.Enable = &report
		// 增加超时时间
		timeout := 10 * time.Second
		impl.Consumer.LocalCache.ServiceRefreshInterval = &timeout
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
	// 现网生产环境验证范围：覆盖 ConsumerAPI 全部 7 类规则查询接口。
	// 每个 handler 在响应中显式暴露 ServiceRuleResponse.GetValidateError()，
	// 便于核对 inmemory.go::messageToServiceRule 把 BehaviorNotRegisteredError
	// 降级为 WARN 的修复在端到端链路上是否生效。
	r.GET("/auth", authHandler)                     // GetBlockAllowRule
	r.GET("/lossless", losslessHandler)             // GetLosslessRule
	r.GET("/lane", laneHandler)                     // GetLane
	r.GET("/circuitbreaker", circuitBreakerHandler) // GetCircuitBreakerRule
	r.GET("/route", routeHandler)                   // GetRouteRule
	r.GET("/nearbyroute", nearbyRouteHandler)       // GetNearbyRouteRule
	r.GET("/ratelimit", rateLimitHandler)           // GetRateLimitRule，含 BehaviorNotRegisteredError 验证

	log.Printf("listening at %s\n", port)
	if err := r.Run(port); err != nil {
		log.Fatalf("启动 HTTP 服务失败: %v", err)
	}
}

func defaultHandler(c *gin.Context) {
	helpText := `Polaris ServiceRule 服务治理规则查询 API

可用接口（覆盖 ConsumerAPI 全部 7 类规则查询）：
- GET /auth            - GetBlockAllowRule       鉴权规则（黑白名单规则）
- GET /lossless        - GetLosslessRule         无损上下线规则
- GET /lane            - GetLane                 泳道规则
- GET /circuitbreaker  - GetCircuitBreakerRule   熔断规则（支持 caller / callee）
- GET /route           - GetRouteRule            路由规则
- GET /nearbyroute     - GetNearbyRouteRule      就近路由规则
- GET /ratelimit       - GetRateLimitRule        限流规则
                         （含 BehaviorNotRegisteredError 修复验证）

请求参数（JSON Body）：
{
  "namespace": "default",
  "service": "provider-demo",
  "direction": "callee"  // 可选：caller / callee，默认 callee
}

响应结构（统一）：
{
  "rule":          { ... ServiceRuleResponse 原文 ... },
  "validateError": {                  // 可选；存在时表示规则有校验错误
    "message": "...",
    "isBehaviorNotRegistered": true,  // 命中 *pb.BehaviorNotRegisteredError
    "unregisteredBehavior":    "tsf"  // 未注册的限流插件名
  }
}

修复验证步骤（针对 action=tsf 限流规则）：
  1) 启动本进程，建议附带 -debug 以便观察 SDK 日志级别变化
  2) 调用 GET /ratelimit 命中含 action=tsf 规则的服务
  3) 在 SDK 日志中应看到 WARN 级别（不再是 ERROR），关键字：
       "rate limit rule for service ... references unregistered behavior plugin"
  4) HTTP 响应中应有 validateError.isBehaviorNotRegistered = true，
     unregisteredBehavior = "tsf"
  5) 对其他 6 个规则接口，validateError 通常为空（其他规则不依赖客户端插件注册）

启动参数：
  -polaris-server-address  北极星服务端地址（默认 127.0.0.1:8091）
  -polaris-token           北极星鉴权 Token
  -namespace               默认命名空间（默认 default）
  -service                 默认服务名（默认 provider-demo）
  -port                    HTTP 监听地址（默认 :38080）
  -debug                   是否开启 Polaris SDK debug 日志（默认 false）
`
	c.String(http.StatusOK, helpText)
}

// ruleRequest HTTP 请求体结构。
type ruleRequest struct {
	Namespace string `json:"namespace"`
	Service   string `json:"service"`
	// Direction 仅熔断规则使用，可选值: caller / callee，默认 callee
	Direction string `json:"direction"`
}

// validateErrorInfo 描述 ServiceRuleResponse.GetValidateError() 的内容，
// 主要用于在 HTTP 响应中验证 BehaviorNotRegisteredError 修复在调用链上的可见性。
type validateErrorInfo struct {
	// Message 校验错误的完整文案，等于 err.Error()
	Message string `json:"message"`
	// IsBehaviorNotRegistered 是否命中 *pb.BehaviorNotRegisteredError
	// 命中表示当前响应中存在客户端未注册的 RateLimiter 插件名（例如 "tsf"），
	// 此时 SDK 内部会以 WARN 级别记录而非 ERROR（这正是本次修复的目标）。
	IsBehaviorNotRegistered bool `json:"isBehaviorNotRegistered"`
	// UnregisteredBehavior 未注册的限流插件名，仅 IsBehaviorNotRegistered 为 true 时有效
	UnregisteredBehavior string `json:"unregisteredBehavior,omitempty"`
}

// ruleQueryResult HTTP 响应结构，统一携带规则原文与校验错误信息。
type ruleQueryResult struct {
	// Rule ServiceRuleResponse 原文（可能为 nil，例如 SDK 直接返回 error）
	Rule *model.ServiceRuleResponse `json:"rule"`
	// ValidateError 规则校验错误，nil 表示无校验错误
	ValidateError *validateErrorInfo `json:"validateError,omitempty"`
}

// inspectValidateError 提取并归类 ServiceRuleResponse 的校验错误，
// 重点识别 *pb.BehaviorNotRegisteredError 这一类"客户端缺插件"的可容忍错误。
// 返回 nil 表示无校验错误。
func inspectValidateError(resp *model.ServiceRuleResponse) *validateErrorInfo {
	if resp == nil {
		return nil
	}
	vErr := resp.GetValidateError()
	if vErr == nil {
		return nil
	}
	info := &validateErrorInfo{Message: vErr.Error()}
	var behaviorErr *pb.BehaviorNotRegisteredError
	if errors.As(vErr, &behaviorErr) {
		info.IsBehaviorNotRegistered = true
		info.UnregisteredBehavior = behaviorErr.Behavior
	}
	return info
}

// ruleAPIFunc 抽象 ConsumerAPI 上 7 类规则查询方法的统一函数签名。
type ruleAPIFunc func(*api.GetServiceRuleRequest) (*model.ServiceRuleResponse, error)

// runRuleQuery 通用规则查询封装，复用 7 个 handler 的共同流程：
// 解析请求、构造 GetServiceRuleRequest、调用 SDK、识别校验错误并落日志。
//
// 参数：
//   - c            Gin 上下文，用于读取 JSON 请求体；不允许为 nil。
//   - kind         日志/响应中标识当前规则类型，例如 "RateLimit"；不允许为空。
//   - useDirection 该规则类型是否需要 Direction 入参（仅熔断规则需要）。
//   - fn           对应的 ConsumerAPI 方法。
//
// 返回：
//   - resp     SDK 返回的 ServiceRuleResponse；err 非 nil 时为 nil。
//   - vInfo    校验错误描述，无校验错误时为 nil。
//   - err      SDK 返回的错误，nil 表示调用成功。
func runRuleQuery(c *gin.Context, kind string, useDirection bool,
	fn ruleAPIFunc) (*model.ServiceRuleResponse, *validateErrorInfo, error) {
	var req ruleRequest
	_ = c.ShouldBindJSON(&req)

	finalNamespace := getParamValue(req.Namespace, namespace)
	finalService := getParamValue(req.Service, service)

	timeout := time.Second * 30
	ruleReq := &api.GetServiceRuleRequest{
		GetServiceRuleRequest: model.GetServiceRuleRequest{
			Namespace: finalNamespace,
			Service:   finalService,
			Timeout:   &timeout,
		},
	}
	if useDirection {
		ruleReq.Direction = parseDirection(req.Direction)
	}

	resp, err := fn(ruleReq)
	if err != nil {
		log.Printf("[%s] 查询失败 - namespace=%s service=%s err=%v",
			kind, finalNamespace, finalService, err)
		return nil, nil, err
	}

	vInfo := inspectValidateError(resp)
	switch {
	case vInfo == nil:
		log.Printf("[%s] 查询成功 - namespace=%s service=%s validateError=<nil>",
			kind, finalNamespace, finalService)
	case vInfo.IsBehaviorNotRegistered:
		// 关键日志：命中本次修复的目标错误类型。
		// 同时建议用户去 SDK 日志中确认对应的 base 模块日志级别已为 WARN。
		log.Printf("[%s] [VERIFY-FIX] 命中 *pb.BehaviorNotRegisteredError, behavior=%q, "+
			"请在 Polaris SDK 日志中确认 base 模块为 WARN（关键字：references unregistered behavior plugin）"+
			" - namespace=%s service=%s",
			kind, vInfo.UnregisteredBehavior, finalNamespace, finalService)
	default:
		log.Printf("[%s] 校验错误（非 BehaviorNotRegistered 类型） - namespace=%s service=%s err=%s",
			kind, finalNamespace, finalService, vInfo.Message)
	}

	return resp, vInfo, nil
}

// writeJSON 统一写出 ruleQueryResult。
func writeJSON(c *gin.Context, resp *model.ServiceRuleResponse, vInfo *validateErrorInfo) {
	buf, _ := json.Marshal(ruleQueryResult{Rule: resp, ValidateError: vInfo})
	c.Data(http.StatusOK, "application/json", buf)
}

// authHandler 查询鉴权规则（黑白名单规则）。
func authHandler(c *gin.Context) {
	resp, vInfo, err := runRuleQuery(c, "Auth", false, consumerAPI.GetBlockAllowRule)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	writeJSON(c, resp, vInfo)
}

// losslessHandler 查询无损上下线规则。
func losslessHandler(c *gin.Context) {
	resp, vInfo, err := runRuleQuery(c, "Lossless", false, consumerAPI.GetLosslessRule)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	writeJSON(c, resp, vInfo)
}

// laneHandler 查询泳道规则。
func laneHandler(c *gin.Context) {
	resp, vInfo, err := runRuleQuery(c, "Lane", false, consumerAPI.GetLane)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	writeJSON(c, resp, vInfo)
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

// circuitBreakerHandler 查询熔断规则，支持 caller / callee 两个方向。
func circuitBreakerHandler(c *gin.Context) {
	resp, vInfo, err := runRuleQuery(c, "CircuitBreaker", true, consumerAPI.GetCircuitBreakerRule)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	writeJSON(c, resp, vInfo)
}

// routeHandler 查询路由规则。
func routeHandler(c *gin.Context) {
	resp, vInfo, err := runRuleQuery(c, "Route", false, consumerAPI.GetRouteRule)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	writeJSON(c, resp, vInfo)
}

// nearbyRouteHandler 查询就近路由规则。
func nearbyRouteHandler(c *gin.Context) {
	resp, vInfo, err := runRuleQuery(c, "NearbyRoute", false, consumerAPI.GetNearbyRouteRule)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	writeJSON(c, resp, vInfo)
}

// rateLimitHandler 查询限流规则，重点用于验证 BehaviorNotRegisteredError 修复：
//   - 当服务端下发的限流规则中 action=tsf 等客户端未注册的插件名时，
//     SDK 内部 messageToServiceRule 会以 WARN 而非 ERROR 记录日志；
//   - 同时 ServiceRuleResponse.GetValidateError() 仍可拿到 *pb.BehaviorNotRegisteredError，
//     调用方可据此感知"该服务限流将被整体跳过"。
func rateLimitHandler(c *gin.Context) {
	resp, vInfo, err := runRuleQuery(c, "RateLimit", false, consumerAPI.GetRateLimitRule)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	writeJSON(c, resp, vInfo)
}

// LoadConfiguration 构造 polaris-go 配置对象。
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
