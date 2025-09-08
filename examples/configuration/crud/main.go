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

func defaultHandler(c *gin.Context) {
	c.String(http.StatusOK, "you can use /create, /update, /publish, /get, /fetch to operate config file")
}

func createHandler(c *gin.Context) {
	var req struct {
		Namespace string `json:"namespace"`
		FileGroup string `json:"config-group"`
		FileName  string `json:"config-name"`
		Content   string `json:"content"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.String(http.StatusBadRequest, "invalid request body")
		return
	}

	if err := configAPI.CreateConfigFile(req.Namespace, req.FileGroup, req.FileName, req.Content); err != nil {
		log.Println("failed to create config file.", err)
		c.String(http.StatusInternalServerError, "create failed")
		return
	}
	log.Println("[Create] Success")
	c.String(http.StatusOK, "create success")
}

func updateHandler(c *gin.Context) {
	var req struct {
		Namespace string `json:"namespace"`
		FileGroup string `json:"config-group"`
		FileName  string `json:"config-name"`
		Content   string `json:"content"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.String(http.StatusBadRequest, "invalid request body")
		return
	}

	if err := configAPI.UpdateConfigFile(req.Namespace, req.FileGroup, req.FileName, req.Content); err != nil {
		log.Println("failed to update config file.", err)
		c.String(http.StatusInternalServerError, "update failed")
		return
	}
	log.Println("[Update] Success")
	c.String(http.StatusOK, "update success")
}

func publishHandler(c *gin.Context) {
	var req struct {
		Namespace string `json:"namespace"`
		FileGroup string `json:"config-group"`
		FileName  string `json:"config-name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.String(http.StatusBadRequest, "invalid request body")
		return
	}
	if err := configAPI.PublishConfigFile(req.Namespace, req.FileGroup, req.FileName); err != nil {
		log.Println("failed to publish config file.", err)
		c.String(http.StatusInternalServerError, "publish failed")
		return
	}
	log.Println("[Publish] Success")
	c.String(http.StatusOK, "publish success")
}

func getHandler(c *gin.Context) {
	var req struct {
		Namespace string `json:"namespace"`
		FileGroup string `json:"config-group"`
		FileName  string `json:"config-name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.String(http.StatusBadRequest, "invalid request body")
		return
	}
	configFile, err := configAPI.GetConfigFile(req.Namespace, req.FileGroup, req.FileName)
	if err != nil {
		log.Println("failed to get config file.", err)
		c.String(http.StatusInternalServerError, "get failed")
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
	if err := c.ShouldBindJSON(&req); err != nil {
		c.String(http.StatusBadRequest, "invalid request body")
		return
	}
	fetchReq := &polaris.GetConfigFileRequest{
		GetConfigFileRequest: &model.GetConfigFileRequest{
			Namespace: req.Namespace,
			FileGroup: req.FileGroup,
			FileName:  req.FileName,
		},
	}
	configFileFromFetch, err := configAPI.FetchConfigFile(fetchReq)
	if err != nil {
		log.Println("failed to get config file.", err)
		c.String(http.StatusInternalServerError, "fetch failed")
		return
	}
	log.Printf("fetched config file is %#v\n", jsonString(configFileFromFetch))
	log.Printf("fetched config file content:\n %s\n", configFileFromFetch.GetContent())
	c.String(http.StatusOK, "got config file content:\n %s\n", configFileFromFetch.GetContent())
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
	crud()

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

func crud() {
	err := configAPI.UpdateConfigFile(namespace, fileGroup, fileName, newContent)
	if err != nil {
		log.Println("failed to update config file.", err)
		return
	}
	log.Println("[Update] Success")

	err = configAPI.PublishConfigFile(namespace, fileGroup, fileName)
	if err != nil {
		log.Println("failed to publish config file.", err)
		return
	}
	log.Println("[Publish] Success")

	configFile, err := configAPI.GetConfigFile(namespace, fileGroup, fileName)
	if err != nil {
		log.Println("failed to get config file.", err)
		return
	}
	log.Printf("got config file is %#v\n", jsonString(configFile))
	log.Printf("got config file content is %s\n", configFile.GetContent())

	fetchReq := &polaris.GetConfigFileRequest{
		GetConfigFileRequest: &model.GetConfigFileRequest{
			Namespace: namespace,
			FileGroup: fileGroup,
			FileName:  fileName,
		},
	}
	configFileFromFetch, err := configAPI.FetchConfigFile(fetchReq)
	if err != nil {
		log.Println("failed to get config file.", err)
		return
	}
	log.Printf("fetched config file is %#v\n", jsonString(configFileFromFetch))
	log.Printf("fetched config file content is %s\n", configFileFromFetch.GetContent())
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
