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
	"log"
	"net"
	"net/http"
	"runtime"

	"github.com/gin-gonic/gin"

	"github.com/polarismesh/polaris-go"
	"github.com/polarismesh/polaris-go/pkg/model"
)

const (
	ActionCreate  = "create"
	ActionUpdate  = "update"
	ActionPublish = "publish"
	ActionGet     = "get"
	ActionFetch   = "fetch"
)

var (
	action     string
	namespace  string
	fileGroup  string
	fileName   string
	content    string
	newContent string
	port       string
)

func initArgs() {
	flag.StringVar(&port, "port", ":38080", "address to listen on")
	flag.StringVar(&action, "action", "get", "action")
	flag.StringVar(&namespace, "namespace", "default", "namespace")
	flag.StringVar(&fileGroup, "config-group", "polaris-config-example", "config group name")
	flag.StringVar(&fileName, "config-name", "example.yaml", "config file name")
	flag.StringVar(&content, "content", "hello world", "config file content")
	flag.StringVar(&newContent, "new-content", "bye~", "new config file content")
}

var actionMap = map[string]string{
	ActionCreate:  "Creating config file",
	ActionUpdate:  "Updating config file",
	ActionPublish: "Publishing config file",
	ActionGet:     "Getting config file",
	ActionFetch:   "Fetching config file",
}

var configAPI polaris.ConfigAPI

func main() {
	initArgs()
	flag.Parse()

	log.Println("NumCPU: ", runtime.NumCPU())
	log.Println("GOMAXPROCS: ", runtime.GOMAXPROCS(0))

	ip, err := getLocalIP()
	if err != nil {
		log.Println("Error:", err)
		return
	}
	log.Println("Local IP:", ip)
	if _, ok := actionMap[action]; !ok {
		log.Println("invalid action.")
		return
	}
	configAPI, err = polaris.NewConfigAPI()
	if err != nil {
		log.Println("failed to start example.", err)
		return
	}

	r := gin.New()
	r.Use(gin.Logger())
	r.Use(gin.Recovery())

	r.GET("/", defaultHandler)
	r.GET("/create", createHandler)
	r.GET("/update", updateHandler)
	r.GET("/publish", publishHandler)
	r.GET("/get", getHandler)
	r.GET("/fetch", fetchHandler)
	log.Printf("listening at %s\n", port)
	err = r.Run(port)
	if err != nil {
		log.Println("failed to start server.")
		return
	}
}

// getParamValue 获取参数值，优先从请求体获取，如果为空则使用默认值
func getParamValue(reqValue, defaultValue string) string {
	if reqValue != "" {
		return reqValue
	}
	return defaultValue
}

func defaultHandler(c *gin.Context) {
	helpText := `Polaris Config CRUD API

Available endpoints:
- GET /create  - Create config file
- GET /update  - Update config file  
- GET /publish - Publish config file
- GET /get     - Get config file
- GET /fetch   - Fetch config file

Parameters can be passed in two ways:
1. JSON request body (recommended for POST requests)
2. Command line arguments (used as fallback)

JSON format example:
{
  "namespace": "default",
  "config-group": "polaris-config-example", 
  "config-name": "example.yaml",
  "content": "hello world"
}

If parameters are not provided in request body, values from command line arguments will be used.
`
	c.String(http.StatusOK, helpText)
}

func createHandler(c *gin.Context) {
	var req struct {
		Namespace string `json:"namespace"`
		FileGroup string `json:"config-group"`
		FileName  string `json:"config-name"`
		Content   string `json:"content"`
	}
	// 尝试绑定JSON，如果失败则使用默认参数
	c.ShouldBindJSON(&req)

	// 如果请求体中没有传入需要的参数，则从启动参数中取值
	finalNamespace := getParamValue(req.Namespace, namespace)
	finalFileGroup := getParamValue(req.FileGroup, fileGroup)
	finalFileName := getParamValue(req.FileName, fileName)
	finalContent := getParamValue(req.Content, content)

	if err := configAPI.CreateConfigFile(finalNamespace, finalFileGroup, finalFileName, finalContent); err != nil {
		log.Println("failed to create config file.", err)
		c.String(http.StatusInternalServerError, "create failed: %v", err)
		return
	}
	log.Printf("[Create] Success - namespace: %s, fileGroup: %s, fileName: %s", finalNamespace, finalFileGroup, finalFileName)
	c.String(http.StatusOK, "create success")
}

func updateHandler(c *gin.Context) {
	var req struct {
		Namespace string `json:"namespace"`
		FileGroup string `json:"config-group"`
		FileName  string `json:"config-name"`
		Content   string `json:"content"`
	}
	// 尝试绑定JSON，如果失败则使用默认参数
	c.ShouldBindJSON(&req)

	// 如果请求体中没有传入需要的参数，则从启动参数中取值
	finalNamespace := getParamValue(req.Namespace, namespace)
	finalFileGroup := getParamValue(req.FileGroup, fileGroup)
	finalFileName := getParamValue(req.FileName, fileName)
	// 对于update操作，如果请求体中没有content，使用newContent参数
	finalContent := getParamValue(req.Content, newContent)

	if err := configAPI.UpdateConfigFile(finalNamespace, finalFileGroup, finalFileName, finalContent); err != nil {
		log.Println("failed to update config file.", err)
		c.String(http.StatusInternalServerError, "update failed: %v", err)
		return
	}
	log.Printf("[Update] Success - namespace: %s, fileGroup: %s, fileName: %s", finalNamespace, finalFileGroup, finalFileName)
	c.String(http.StatusOK, "update success")
}

func publishHandler(c *gin.Context) {
	var req struct {
		Namespace string `json:"namespace"`
		FileGroup string `json:"config-group"`
		FileName  string `json:"config-name"`
	}
	// 尝试绑定JSON，如果失败则使用默认参数
	c.ShouldBindJSON(&req)

	// 如果请求体中没有传入需要的参数，则从启动参数中取值
	finalNamespace := getParamValue(req.Namespace, namespace)
	finalFileGroup := getParamValue(req.FileGroup, fileGroup)
	finalFileName := getParamValue(req.FileName, fileName)

	if err := configAPI.PublishConfigFile(finalNamespace, finalFileGroup, finalFileName); err != nil {
		log.Println("failed to publish config file.", err)
		c.String(http.StatusInternalServerError, "publish failed: %v", err)
		return
	}
	log.Printf("[Publish] Success - namespace: %s, fileGroup: %s, fileName: %s", finalNamespace, finalFileGroup, finalFileName)
	c.String(http.StatusOK, "publish success")
}

func getHandler(c *gin.Context) {
	var req struct {
		Namespace string `json:"namespace"`
		FileGroup string `json:"config-group"`
		FileName  string `json:"config-name"`
	}
	// 尝试绑定JSON，如果失败则使用默认参数
	c.ShouldBindJSON(&req)

	// 如果请求体中没有传入需要的参数，则从启动参数中取值
	finalNamespace := getParamValue(req.Namespace, namespace)
	finalFileGroup := getParamValue(req.FileGroup, fileGroup)
	finalFileName := getParamValue(req.FileName, fileName)

	configFile, err := configAPI.GetConfigFile(finalNamespace, finalFileGroup, finalFileName)
	if err != nil {
		log.Println("failed to get config file.", err)
		c.String(http.StatusInternalServerError, "get failed: %v", err)
		return
	}
	log.Printf("got config file is %#v\n", jsonString(configFile))
	log.Printf("got config file content:\n %s\n", configFile.GetContent())
	c.String(http.StatusOK, "got config file content:\n %s\n", configFile.GetContent())
}

func fetchHandler(c *gin.Context) {
	var req struct {
		Namespace string `json:"namespace"`
		FileGroup string `json:"config-group"`
		FileName  string `json:"config-name"`
	}
	// 尝试绑定JSON，如果失败则使用默认参数
	c.ShouldBindJSON(&req)

	// 如果请求体中没有传入需要的参数，则从启动参数中取值
	finalNamespace := getParamValue(req.Namespace, namespace)
	finalFileGroup := getParamValue(req.FileGroup, fileGroup)
	finalFileName := getParamValue(req.FileName, fileName)

	fetchReq := &polaris.GetConfigFileRequest{
		GetConfigFileRequest: &model.GetConfigFileRequest{
			Namespace: finalNamespace,
			FileGroup: finalFileGroup,
			FileName:  finalFileName,
		},
	}
	configFileFromFetch, err := configAPI.FetchConfigFile(fetchReq)
	if err != nil {
		log.Println("failed to get config file.", err)
		c.String(http.StatusInternalServerError, "fetch failed: %v", err)
		return
	}
	log.Printf("fetched config file is %#v\n", jsonString(configFileFromFetch))
	log.Printf("fetched config file content:\n %s\n", configFileFromFetch.GetContent())
	c.String(http.StatusOK, "got config file content:\n %s\n", configFileFromFetch.GetContent())
}

func jsonString(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

func getLocalIP() (string, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", err
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				return ipnet.IP.String(), nil
			}
		}
	}
	return "", fmt.Errorf("no local IP address found")
}
