package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

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
)

func init() {
	flag.BoolVar(&debug, "debug", false, "是否开启调试模式")
	flag.StringVar(&namespaceVal, "namespace", "default", "namespace")
	flag.StringVar(&serviceVal, "service", "DiscoverEchoServer", "service name")
	flag.StringVar(&clientsVal, "clients", "", "clientId与server地址映射，格式: clientId=serverAddr，多个用逗号分隔，例如: ctxA=114.132.192.60,ctxB=114.132.29.62")
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

func main() {
	flag.Parse()

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
	consumerAPI := polaris.NewConsumerAPIByContext(sdkContext)

	// === GetOneInstance ===
	log.Printf("%s [GetOneInstance] 开始查询 - namespace=%s, service=%s, serverAddr=%s",
		idInfoMapStr, namespaceVal, serviceVal, serverAddr)
	getOneReq := &polaris.GetOneInstanceRequest{}
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
	getReq := &polaris.GetInstancesRequest{}
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
	getAllReq := &polaris.GetAllInstancesRequest{}
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
