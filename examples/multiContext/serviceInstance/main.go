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
	flag.StringVar(&clientsVal, "clients", "", "clientId与server地址映射，格式: clientId=serverAddr，多个用逗号分隔，例如: ctxA=<server1>,ctxB=<server2>")
	flag.StringVar(&port, "port", ":38070", "HTTP 监听地址")
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
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

	// 解析 -clients 参数
	clientMap, err := parseClients(clientsVal)
	if err != nil {
		log.Fatalf("解析 -clients 参数失败: %v", err)
	}

	log.Printf("Starting serviceInstance example with namespace: %s, service: %s", namespaceVal, serviceVal)
	log.Printf("clients: %v", clientMap)
	if debug {
		// 设置日志级别为DEBUG
		if err := api.SetLoggersLevel(api.DebugLog); err != nil {
			log.Printf("fail to set log level to DEBUG, err is %v", err)
		} else {
			log.Printf("successfully set log level to DEBUG")
		}
	}
	// errCh 用于收集 serverProcess 的错误，当所有 serverProcess 都异常退出时主动结束
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

// instanceInfo 用于 JSON 输出的实例信息结构
type instanceInfo struct {
	ID       string            `json:"id"`
	Host     string            `json:"host"`
	Port     uint32            `json:"port"`
	Weight   int               `json:"weight"`
	Healthy  bool              `json:"healthy"`
	Isolated bool              `json:"isolated"`
	Protocol string            `json:"protocol,omitempty"`
	Version  string            `json:"version,omitempty"`
	Region   string            `json:"region,omitempty"`
	Zone     string            `json:"zone,omitempty"`
	Campus   string            `json:"campus,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// toInstanceInfo 将 model.Instance 转换为可序列化的 instanceInfo
func toInstanceInfo(inst model.Instance) instanceInfo {
	return instanceInfo{
		ID:       inst.GetId(),
		Host:     inst.GetHost(),
		Port:     inst.GetPort(),
		Weight:   inst.GetWeight(),
		Healthy:  inst.IsHealthy(),
		Isolated: inst.IsIsolated(),
		Protocol: inst.GetProtocol(),
		Version:  inst.GetVersion(),
		Region:   inst.GetRegion(),
		Zone:     inst.GetZone(),
		Campus:   inst.GetCampus(),
		Metadata: inst.GetMetadata(),
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

	// === GetOneInstance ===
	log.Printf("%s [GetOneInstance] 开始查询 - namespace=%s, service=%s, serverAddr=%s",
		idInfoMapStr, namespaceVal, serviceVal, serverAddr)
	getOneReq := &api.GetOneInstanceRequest{}
	getOneReq.Namespace = namespaceVal
	getOneReq.Service = serviceVal
	oneResp, err := consumerAPI.GetOneInstance(getOneReq)
	if err != nil {
		log.Printf("%s [GetOneInstance] 查询失败 - serverAddr=%s: %v", idInfoMapStr, serverAddr, err)
	} else {
		inst := oneResp.GetInstance()
		if inst != nil {
			info := toInstanceInfo(inst)
			buf, _ := json.MarshalIndent(info, "", "  ")
			log.Printf("%s [GetOneInstance] 查询成功 - serverAddr=%s, instance:\n%s", idInfoMapStr, serverAddr,
				string(buf))
		} else {
			log.Printf("%s [GetOneInstance] 未找到实例 - serverAddr=%s", idInfoMapStr, serverAddr)
		}
	}

	// === GetInstances ===
	log.Printf("%s [GetInstances] 开始查询 - namespace=%s, service=%s, serverAddr=%s", idInfoMapStr, namespaceVal,
		serviceVal, serverAddr)
	getReq := &api.GetInstancesRequest{}
	getReq.Namespace = namespaceVal
	getReq.Service = serviceVal
	instResp, err := consumerAPI.GetInstances(getReq)
	if err != nil {
		log.Printf("%s [GetInstances] 查询失败 - serverAddr=%s: %v", idInfoMapStr, serverAddr, err)
	} else {
		instances := make([]instanceInfo, 0, len(instResp.Instances))
		for _, inst := range instResp.Instances {
			instances = append(instances, toInstanceInfo(inst))
		}
		buf, _ := json.MarshalIndent(instances, "", "  ")
		log.Printf("%s [GetInstances] 查询成功 - serverAddr=%s, count=%d, instances:\n%s",
			idInfoMapStr, serverAddr, len(instances), string(buf))
	}

	// === GetAllInstances ===
	log.Printf("%s [GetAllInstances] 开始查询 - namespace=%s, service=%s, serverAddr=%s", idInfoMapStr, namespaceVal,
		serviceVal, serverAddr)
	getAllReq := &api.GetAllInstancesRequest{}
	getAllReq.Namespace = namespaceVal
	getAllReq.Service = serviceVal
	allResp, err := consumerAPI.GetAllInstances(getAllReq)
	if err != nil {
		log.Printf("%s [GetAllInstances] 查询失败 - serverAddr=%s: %v", idInfoMapStr, serverAddr, err)
	} else {
		instances := make([]instanceInfo, 0, len(allResp.Instances))
		for _, inst := range allResp.Instances {
			instances = append(instances, toInstanceInfo(inst))
		}
		buf, _ := json.MarshalIndent(instances, "", "  ")
		log.Printf("%s [GetAllInstances] 查询成功 - serverAddr=%s, count=%d, instances:\n%s", idInfoMapStr, serverAddr,
			len(instances), string(buf))
	}

	log.Printf("%s 所有实例查询完成，保持运行中... Press Ctrl+C to exit (serverAddr=%s)", idInfoMapStr, serverAddr)
	// 正常情况下阻塞在此，不会返回；由主函数通过信号控制退出
	select {}
}

// ====== HTTP 服务部分 ======

// instanceRequest HTTP 请求参数
type instanceRequest struct {
	Namespace string `json:"namespace"`
	Service   string `json:"service"`
	ClientId  string `json:"client_id"` // 指定使用哪个 context（ctxA 或 ctxB）
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
	r.GET("/get-one", getOneInstanceHandler)
	r.GET("/get-instances", getInstancesHandler)
	r.GET("/get-all", getAllInstancesHandler)

	log.Printf("HTTP 服务启动，监听地址: %s", port)
	if err := r.Run(port); err != nil {
		log.Printf("启动 HTTP 服务失败: %v", err)
	}
}

func defaultHandler(c *gin.Context) {
	helpText := `Polaris MultiContext ServiceInstance 实例查询 API

可用接口:
- GET /getone         - 获取单个服务实例（经过路由和负载均衡）
- GET /getinstances   - 获取可用的服务实例列表（经过路由，去掉隔离及不健康实例）
- GET /getall         - 获取完整的服务实例列表（包括隔离及不健康实例）

请求参数（JSON Body）:
{
  "namespace": "default",
  "service": "provider-demo",
  "client_id": "ctxA"
}

参数说明:
- namespace  命名空间（默认使用启动参数值）
- service    服务名（默认使用启动参数值）
- client_id  指定使用哪个 context 查询，可选值: ctxA / ctxB（默认 ctxA）

如果请求体中未传入参数，将使用启动参数的默认值。

启动参数示例:
  -clients     clientId与server地址映射（格式: clientId=serverAddr，多个用逗号分隔，例如: ctxA=<server1>,ctxB=<server2>）
  -namespace   默认命名空间（默认 default）
  -service     默认服务名（默认 provider-demo）
  -port        HTTP 监听地址（默认 :38080）
  -debug       是否开启调试模式
`
	c.String(http.StatusOK, helpText)
}

func getOneInstanceHandler(c *gin.Context) {
	var req instanceRequest
	c.ShouldBindJSON(&req)

	consumerAPI, err := getConsumerAPI(req.ClientId)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	finalNamespace := getParamValue(req.Namespace, namespaceVal)
	finalService := getParamValue(req.Service, serviceVal)

	getOneReq := &api.GetOneInstanceRequest{}
	getOneReq.Namespace = finalNamespace
	getOneReq.Service = finalService
	oneResp, err := consumerAPI.GetOneInstance(getOneReq)
	if err != nil {
		log.Printf("[HTTP][GetOneInstance] 查询失败 - clientId: %s, namespace: %s, service: %s, err: %v",
			req.ClientId, finalNamespace, finalService, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	log.Printf("[HTTP][GetOneInstance] 查询成功 - clientId: %s, namespace: %s, service: %s",
		req.ClientId, finalNamespace, finalService)
	inst := oneResp.GetInstance()
	if inst != nil {
		info := toInstanceInfo(inst)
		buf, _ := json.Marshal(info)
		c.Data(http.StatusOK, "application/json", buf)
	} else {
		c.JSON(http.StatusOK, gin.H{"message": "未找到实例"})
	}
}

func getInstancesHandler(c *gin.Context) {
	var req instanceRequest
	c.ShouldBindJSON(&req)

	consumerAPI, err := getConsumerAPI(req.ClientId)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	finalNamespace := getParamValue(req.Namespace, namespaceVal)
	finalService := getParamValue(req.Service, serviceVal)

	getReq := &api.GetInstancesRequest{}
	getReq.Namespace = finalNamespace
	getReq.Service = finalService
	instResp, err := consumerAPI.GetInstances(getReq)
	if err != nil {
		log.Printf("[HTTP][GetInstances] 查询失败 - clientId: %s, namespace: %s, service: %s, err: %v",
			req.ClientId, finalNamespace, finalService, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	log.Printf("[HTTP][GetInstances] 查询成功 - clientId: %s, namespace: %s, service: %s, count: %d",
		req.ClientId, finalNamespace, finalService, len(instResp.Instances))
	instances := make([]instanceInfo, 0, len(instResp.Instances))
	for _, inst := range instResp.Instances {
		instances = append(instances, toInstanceInfo(inst))
	}
	buf, _ := json.Marshal(instances)
	c.Data(http.StatusOK, "application/json", buf)
}

func getAllInstancesHandler(c *gin.Context) {
	var req instanceRequest
	c.ShouldBindJSON(&req)

	consumerAPI, err := getConsumerAPI(req.ClientId)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	finalNamespace := getParamValue(req.Namespace, namespaceVal)
	finalService := getParamValue(req.Service, serviceVal)

	getAllReq := &api.GetAllInstancesRequest{}
	getAllReq.Namespace = finalNamespace
	getAllReq.Service = finalService
	allResp, err := consumerAPI.GetAllInstances(getAllReq)
	if err != nil {
		log.Printf("[HTTP][GetAllInstances] 查询失败 - clientId: %s, namespace: %s, service: %s, err: %v",
			req.ClientId, finalNamespace, finalService, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	log.Printf("[HTTP][GetAllInstances] 查询成功 - clientId: %s, namespace: %s, service: %s, count: %d",
		req.ClientId, finalNamespace, finalService, len(allResp.Instances))
	instances := make([]instanceInfo, 0, len(allResp.Instances))
	for _, inst := range allResp.Instances {
		instances = append(instances, toInstanceInfo(inst))
	}
	buf, _ := json.Marshal(instances)
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
