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
	"flag"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/polarismesh/polaris-go"
)

var (
	namespace string
	service   string
	fileCount int
	threads   int
	duration  int
)

func initArgs() {
	flag.StringVar(&namespace, "namespace", "default", "namespace")
	flag.StringVar(&service, "service", "", "service")
	flag.IntVar(&fileCount, "fileCount", 100, "number of config files to test")
	flag.IntVar(&threads, "threads", 50, "number of concurrent threads")
	flag.IntVar(&duration, "duration", 30, "test duration in seconds")
}

func main() {
	initArgs()
	flag.Parse()
	
	if len(namespace) == 0 || len(service) == 0 {
		log.Print("namespace and service are required")
		return
	}

	consumer, err := polaris.NewConsumerAPI()
	if err != nil {
		log.Fatalf("fail to create consumerAPI, err is %v", err)
	}
	defer consumer.Destroy()

	log.Printf("Starting concurrent config file access test...")
	log.Printf("Namespace: %s, Service: %s", namespace, service)
	log.Printf("Files: %d, Threads: %d, Duration: %ds", fileCount, threads, duration)

	// 测试数据准备
	fileNames := make([]string, fileCount)
	for i := 0; i < fileCount; i++ {
		fileNames[i] = fmt.Sprintf("config-file-%d.properties", i)
	}

	// 并发测试
	var successCount int64
	var errorCount int64
	var wg sync.WaitGroup
	
	stopChan := make(chan struct{})
	timer := time.NewTimer(time.Duration(duration) * time.Second)

	// 启动统计协程
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		
		for {
			select {
			case <-ticker.C:
				success := atomic.LoadInt64(&successCount)
				error := atomic.LoadInt64(&errorCount)
				log.Printf("Progress - Success: %d, Errors: %d, Total: %d", success, error, success+error)
			case <-stopChan:
				return
			}
		}
	}()

	// 启动工作协程
	for i := 0; i < threads; i++ {
		wg.Add(1)
		go func(threadID int) {
			defer wg.Done()
			
			for {
				select {
				case <-stopChan:
					return
				default:
					// 随机选择一个配置文件进行获取
					fileIndex := threadID % fileCount
					fileName := fileNames[fileIndex]
					
					req := &polaris.GetConfigFileRequest{
						Namespace: namespace,
						FileGroup: service,
						FileName:  fileName,
						Subscribe: false,
					}
					
					startTime := time.Now()
					configFile, err := consumer.GetConfigFile(req)
					elapsed := time.Since(startTime)
					
					if err != nil {
						atomic.AddInt64(&errorCount, 1)
						log.Printf("Thread %d failed to get config file %s: %v", threadID, fileName, err)
					} else {
						atomic.AddInt64(&successCount, 1)
						if elapsed > 100*time.Millisecond {
							log.Printf("Thread %d got config file %s in %v", threadID, fileName, elapsed)
						}
						_ = configFile // 避免未使用变量警告
					}
				}
			}
		}(i)
	}

	// 等待测试时间结束
	<-timer.C
	close(stopChan)
	wg.Wait()

	// 输出最终结果
	finalSuccess := atomic.LoadInt64(&successCount)
	finalError := atomic.LoadInt64(&errorCount)
	totalOps := finalSuccess + finalError
	
	log.Printf("\n=== Test Results ===")
	log.Printf("Total operations: %d", totalOps)
	log.Printf("Successful operations: %d", finalSuccess)
	log.Printf("Failed operations: %d", finalError)
	log.Printf("Success rate: %.2f%%", float64(finalSuccess)/float64(totalOps)*100)
	log.Printf("Operations per second: %.2f", float64(totalOps)/float64(duration))
	
	if finalError == 0 {
		log.Printf("✅ Test PASSED - All operations completed successfully")
	} else {
		log.Printf("⚠️ Test completed with %d errors", finalError)
	}
}