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
	configGroupA string
	configGroupB string
	serverA      string
	serverB      string
)

func init() {
	flag.BoolVar(&debug, "debug", false, "是否开启调试模式")
	flag.StringVar(&namespaceVal, "namespace", "default", "namespace")
	flag.StringVar(&configGroupA, "configGroupA", "configGroupA", "config group A name")
	flag.StringVar(&configGroupB, "configGroupB", "configGroupB", "config group B name")
	flag.StringVar(&serverA, "serverA", "", "address of server A")
	flag.StringVar(&serverB, "serverB", "", "address of server B")
}

func main() {
	flag.Parse()
	log.Printf("Starting config group example with namespace: %s, group: %s", namespaceVal, configGroupA)
	log.Printf("serverA: %s, configGroupA:%s, serverB: %s, configGroupB:%s", serverA, configGroupA, serverB,
		configGroupB)
	if debug {
		// 设置日志级别为DEBUG
		if err := api.SetLoggersLevel(api.DebugLog); err != nil {
			log.Printf("fail to set log level to DEBUG, err is %v", err)
		} else {
			log.Printf("successfully set log level to DEBUG")
		}
	}
	// errCh 用于收集 serverProcess 的错误，当所有 serverProcess 都异常退出时主动结束
	errCh := make(chan error, 2)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		if err := serverProcess(serverA, "ctxA", configGroupA); err != nil {
			errCh <- fmt.Errorf("ctxA 异常退出: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		if err := serverProcess(serverB, "ctxB", configGroupB); err != nil {
			errCh <- fmt.Errorf("ctxB 异常退出: %v", err)
		}
	}()

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

func serverProcess(serverAddr, clientId, groupName string) error {
	log.Printf("%s client start, serverAddr=%s, groupName=%s", clientId, serverAddr, groupName)
	conf := config.NewDefaultConfiguration([]string{serverAddr + ":8091"})
	conf.GetConfigFile().GetConfigConnectorConfig().SetAddresses([]string{serverAddr + ":8093"})
	conf.GetConsumer().GetLocalCache().SetPersistDir("./cache/" + clientId + "/backup")
	conf.GetConfigFile().GetLocalCache().SetPersistDir("./cache/" + clientId + "/config")
	conf.GetGlobal().GetClient().(*config.ClientConfigImpl).AddLabels(map[string]string{
		"uin": clientId,
	})
	sdkContext, err := polaris.NewSDKContextByConfig(conf)
	if err != nil {
		log.Printf("%s fail to create sdk context, serverAddr=%s, groupName=%s: %v", clientId, serverAddr, groupName,
			err)
		return err
	}
	idInfoMap := sdkContext.GetConfig().GetGlobal().GetClient().GetLabels()
	idInfoMapStr := printMap(idInfoMap)
	configGroupAPI := polaris.NewConfigGroupAPIByContext(sdkContext)
	log.Printf("%s Fetching config group: namespace=%s, group=%s, serverAddr=%s", idInfoMapStr, namespaceVal, groupName,
		serverAddr)
	group, err := configGroupAPI.GetConfigGroup(namespaceVal, groupName)
	if err != nil {
		log.Printf("%s fail to get config file, serverAddr=%s, groupName=%s: %v", idInfoMapStr, serverAddr, groupName,
			err)
		return err
	}

	// 获取配置组中的所有文件
	files, revision, success := group.GetFiles()
	if !success {
		log.Printf("%s Warning: Failed to get files from config group or group is empty, serverAddr=%s, "+
			"groupName=%s", idInfoMapStr, serverAddr, groupName)
	} else {
		log.Printf("%s Config group fetched successfully, revision: %s, file count: %d, serverAddr=%s, "+
			"groupName=%s", idInfoMapStr, revision, len(files), serverAddr, groupName)
	}

	// 打印配置组中的所有文件名
	if len(files) > 0 {
		log.Printf("%s Config files in group (serverAddr=%s, groupName=%s):", idInfoMapStr, serverAddr, groupName)
		for _, file := range files {
			log.Printf("%s   - %s (version: %d, md5: %s)", idInfoMapStr, file.FileName, file.Version, file.Md5)
		}
	} else {
		log.Printf("%s No config files found in group, serverAddr=%s, groupName=%s", idInfoMapStr, serverAddr, groupName)
	}

	log.Printf("%s Adding change listener for config group, serverAddr=%s, groupName=%s...", idInfoMapStr, serverAddr,
		groupName)
	group.AddChangeListener(func(event *model.ConfigGroupChangeEvent) {
		before, _ := json.Marshal(event.Before)
		after, _ := json.Marshal(event.After)
		log.Printf("%s receive config_group change event (serverAddr=%s, groupName=%s)\nbefore: %s\nafter: %s",
			idInfoMapStr, serverAddr, groupName, string(before), string(after))
	})
	log.Printf("%s Change listener added successfully, serverAddr=%s, groupName=%s", idInfoMapStr, serverAddr, groupName)

	log.Printf("%s Listening for config group changes (serverAddr=%s, groupName=%s)... Press Ctrl+C to exit",
		idInfoMapStr, serverAddr, groupName)
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
