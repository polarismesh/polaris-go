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

package stability

import (
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/user"
	"time"

	"github.com/golang/protobuf/jsonpb"
	"github.com/golang/protobuf/ptypes/wrappers"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"gopkg.in/check.v1"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/local"
	"github.com/polarismesh/polaris-go/pkg/model/pb"
	namingpb "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/localregistry"
	"github.com/polarismesh/polaris-go/test/mock"
	"github.com/polarismesh/polaris-go/test/util"
)

const (
	cacheNS                = "cacheNS"
	cacheSVC               = "cacheSVC"
	cacheIP                = "127.0.0.1"
	cachePort              = 8028
	serviceExpireDuration  = 15 * time.Second
	serviceRefreshDuration = 2 * time.Second
)

var (
	backupFile = "testdata/backup/svc#" + cacheNS + "#" + cacheSVC + "#" +
		fmt.Sprintf("%s", model.EventInstances) + ".json"
	backupFileCp = backupFile + ".cp"
)

// 缓存持久化测试套件
type CacheTestingSuite struct {
	grpcServer   *grpc.Server
	grpcListener net.Listener
	serviceToken string
	testService  *namingpb.Service
	mockServer   mock.NamingServer
}

// 初始化测试套件
func (t *CacheTestingSuite) SetUpSuite(c *check.C) {
	grpcOptions := make([]grpc.ServerOption, 0)
	maxStreams := 100000
	grpcOptions = append(grpcOptions, grpc.MaxConcurrentStreams(uint32(maxStreams)))
	grpc.EnableTracing = true

	var err error
	t.grpcServer = grpc.NewServer(grpcOptions...)
	t.serviceToken = uuid.New().String()
	t.mockServer = mock.NewNamingServer()
	t.mockServer.RegisterNamespace(&namingpb.Namespace{
		Name:    &wrappers.StringValue{Value: cacheNS},
		Comment: &wrappers.StringValue{Value: "for cache persist test"},
		Owners:  &wrappers.StringValue{Value: "CachePersistor"},
	})
	t.testService = &namingpb.Service{
		Name:      &wrappers.StringValue{Value: cacheSVC},
		Namespace: &wrappers.StringValue{Value: cacheNS},
		Token:     &wrappers.StringValue{Value: t.serviceToken},
	}
	t.mockServer.RegisterService(t.testService)
	t.mockServer.GenTestInstances(t.testService, 5)

	token := t.mockServer.RegisterServerService(config.ServerDiscoverService)
	t.mockServer.RegisterServerInstance(cacheIP, cachePort, config.ServerDiscoverService, token, true)
	namingpb.RegisterPolarisGRPCServer(t.grpcServer, t.mockServer)

	t.grpcListener, err = net.Listen("tcp", fmt.Sprintf("%s:%d", cacheIP, cachePort))
	if err != nil {
		log.Fatal(fmt.Sprintf("error listening appserver %v", err))
	}
	log.Printf("appserver listening on %s:%d\n", cacheIP, cachePort)
	go func() {
		t.grpcServer.Serve(t.grpcListener)
	}()
}

// 测试套件名字
func (t *CacheTestingSuite) GetName() string {
	return "Cache"
}

// 销毁套件
func (t *CacheTestingSuite) TearDownSuite(c *check.C) {
	t.grpcServer.Stop()
	for i := 0; i < 5; i++ {
		if util.DirExist(util.BackupDir) {
			err := os.RemoveAll(util.BackupDir)
			if nil == err {
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
	}
	util.InsertLog(t, c.GetTestLog())
}

// 测试比较之前的缓存数据
func (t *CacheTestingSuite) testCacheCompareOriginal(cfg config.Configuration, c *check.C) *model.InstancesResponse {
	sdkCtx, err := api.InitContextByConfig(cfg)
	c.Assert(err, check.IsNil)
	consumerAPI := api.NewConsumerAPIByContext(sdkCtx)
	defer consumerAPI.Destroy()
	request := &api.GetInstancesRequest{}
	request.FlowID = 1111
	request.Namespace = cacheNS
	request.Service = cacheSVC
	request.Timeout = model.ToDurationPtr(200 * time.Millisecond)
	origSvcInstances, err := consumerAPI.GetInstances(request)
	c.Assert(err, check.IsNil)
	fmt.Printf("Instance get time: %v, count is %d\n", time.Now(), len(origSvcInstances.Instances))
	expireTime := time.Now().Add(serviceExpireDuration)
	time.Sleep(config.DefaultMinServiceExpireTime + 3*time.Second)
	c.Assert(t.checkPersist(origSvcInstances.Instances), check.Equals, true)
	time.Sleep(expireTime.Sub(time.Now()) + 25*time.Second)
	fmt.Printf("check backupFile %s, time %v\n", backupFile, time.Now())
	c.Assert(util.FileExist(backupFile), check.Equals, false)
	return origSvcInstances
}

// 测试比较后来的缓存数据
func (t *CacheTestingSuite) testCacheCompareForward(
	origSvcInstances *model.InstancesResponse, cfg config.Configuration, someDefaultServerDown bool, c *check.C) {
	if someDefaultServerDown {
		cfgImpl := cfg.(*config.ConfigurationImpl)
		cfgImpl.Global.ServerConnector.Addresses = []string{"127.0.0.1:7655", fmt.Sprintf("%s:%d", cacheIP, cachePort)}
	}
	sdkCtx, err := api.InitContextByConfig(cfg)
	c.Assert(err, check.IsNil)
	consumerAPI1 := api.NewConsumerAPIByContext(sdkCtx)
	defer consumerAPI1.Destroy()
	c.Assert(err, check.IsNil)
	request := &api.GetInstancesRequest{}
	request.FlowID = 1111
	request.Namespace = cacheNS
	request.Service = cacheSVC
	request.SkipRouteFilter = true
	request.Timeout = model.ToDurationPtr(200 * time.Millisecond)
	cachedSvcInstances, err := consumerAPI1.GetInstances(request)
	c.Assert(err, check.IsNil)
	fmt.Printf("Instance get before refresh, time: %v, count is %d\n", time.Now(), len(cachedSvcInstances.GetInstances()))
	// 从缓存中加载的服务在网络通畅情况下，会及时更新与server保持一致
	c.Assert(util.SameInstances(origSvcInstances.Instances, cachedSvcInstances.GetInstances()), check.Equals, false)
	time.Sleep(serviceRefreshDuration + 5*time.Second)
	// 如果是某些埋点server down掉的话，等待更长的时间，
	// 以便与正确的地址建立连接，并更新服务信息
	if someDefaultServerDown {
		time.Sleep(20 * time.Second)
	}

	refreshSvcInstances, err := consumerAPI1.GetInstances(request)
	fmt.Printf("Instance after refresh, time: %v, count is %d\n", time.Now(), len(cachedSvcInstances.GetInstances()))
	c.Assert(err, check.IsNil)
	c.Assert(util.SameInstances(origSvcInstances.Instances, refreshSvcInstances.Instances), check.Equals, false)
	// 上面检测没挂，说明实例信息已经更新了，
	// 等待缓存文件落盘再继续检测
	for {
		if util.FileExist(backupFile) {
			break
		}
	}
	c.Assert(t.checkPersist(refreshSvcInstances.Instances), check.Equals, true)
}

// 测试过程
func (t *CacheTestingSuite) TestCacheExpireAndPersist(c *check.C) {
	defer util.DeleteDir(util.BackupDir)
	fmt.Println("Cache Persist Suite: TestCacheExpireAndPersist")
	cfg, err := config.LoadConfigurationByFile("testdata/cache.yaml")
	cfg.GetConsumer().GetLocalCache().SetStartUseFileCache(false)
	c.Assert(err, check.IsNil)
	origSvcInstances := t.testCacheCompareOriginal(cfg, c)
	err = t.restoreBackupFile()
	c.Assert(err, check.IsNil)
	t.mockServer.GenTestInstances(t.testService, 2)
	t.testCacheCompareForward(origSvcInstances, cfg, false, c)
}

// 测试当一些埋点server down掉时的情景
func (t *CacheTestingSuite) TestCacheWithSomeDefaultServerDown(c *check.C) {
	defer util.DeleteDir(util.BackupDir)
	fmt.Println("Cache Persist Suite: TestCacheRefreshWithSomeDefaultServerDown")
	cfg, err := config.LoadConfigurationByFile("testdata/cache.yaml")
	cfg.GetConsumer().GetLocalCache().SetStartUseFileCache(false)
	c.Assert(err, check.IsNil)
	origSvcInstances := t.testCacheCompareOriginal(cfg, c)
	err = t.restoreBackupFile()
	c.Assert(err, check.IsNil)
	t.mockServer.GenTestInstances(t.testService, 2)
	t.testCacheCompareForward(origSvcInstances, cfg, true, c)
}

// 测试服务端的服务被删除后，内存和文件缓存是否被删除
func (t *CacheTestingSuite) TestServiceDelete(c *check.C) {
	defer util.DeleteDir(util.BackupDir)
	fmt.Println("Cache Persist Suite: TestServiceDelete")
	cfg, err := config.LoadConfigurationByFile("testdata/cache.yaml")
	c.Assert(err, check.IsNil)
	cfg.Consumer.LocalCache.ServiceExpireTime = model.ToDurationPtr(60 * time.Minute)
	sdkCtx, err := api.InitContextByConfig(cfg)
	c.Assert(err, check.IsNil)
	registry, err := sdkCtx.GetPlugins().GetPlugin(common.TypeLocalRegistry, "inmemory")
	regPlug := registry.(localregistry.LocalRegistry)
	c.Assert(err, check.IsNil)

	consumer := api.NewConsumerAPIByContext(sdkCtx)
	request := &api.GetInstancesRequest{}
	request.FlowID = 1111
	request.Namespace = cacheNS
	request.Service = cacheSVC
	request.Timeout = model.ToDurationPtr(200 * time.Millisecond)
	_, err = consumer.GetInstances(request)
	c.Assert(err, check.IsNil)

	svc := t.mockServer.DeregisterService(cacheNS, cacheSVC)
	log.Println("wait 15s for updating service")
	time.Sleep(15 * time.Second)
	svcInsts := regPlug.GetInstances(&model.ServiceKey{
		Service:   cacheSVC,
		Namespace: cacheNS,
	}, false, true)
	c.Assert(svcInsts.IsInitialized(), check.Equals, false)
	c.Assert(util.FileExist(backupFile), check.Equals, false)
	t.mockServer.RegisterService(svc)
}

// 重试打开文件
func tryOpenFile(backupFile string) (*os.File, error) {
	var backupJson *os.File
	var err error
	for i := 0; i < 5; i++ {
		backupJson, err = os.OpenFile(backupFile, os.O_RDONLY, 0644)
		if nil == err {
			return backupJson, err
		}
		time.Sleep(500 * time.Millisecond)
	}
	return nil, err
}

// 检测是否由缓存
func (t *CacheTestingSuite) checkPersist(origInsts []model.Instance) bool {
	startTime := time.Now()
	fmt.Printf("start to checkPersist, %v\n", startTime)
	if !util.FileExist(backupFile) {
		log.Printf("Fail to checkPersist because %s not found, startTime: %v\n", backupFile, startTime)
		return false
	}
	backupJson, err := tryOpenFile(backupFile)
	if err != nil {
		log.Printf("Fail to checkPersist because %s can not be open for %v, startTime: %v\n",
			backupFile, err, startTime)
		backupJson.Close()
		return false
	}
	svcResp := &namingpb.DiscoverResponse{}
	jsonpb.Unmarshal(backupJson, svcResp)
	backupJson.Close()
	svcInsts := pb.NewServiceInstancesInProto(svcResp, func(string) local.InstanceLocalValue {
		return local.NewInstanceLocalValue()
	}, nil, nil)
	svcInsts.CacheLoaded = 1
	insts := svcInsts.GetInstances()
	if !util.SameInstances(origInsts, insts) {
		fmt.Printf("Fail to checkPersist because instances different, startTime: %v\n", startTime)
		return false
	}
	backupContent, _ := ioutil.ReadFile(backupFile)
	ioutil.WriteFile(backupFileCp, backupContent, 0644)
	fmt.Printf("finish checkPersist, startTime: %v\n", startTime)
	return true
}

// 查看备份文件是否存在
func (t *CacheTestingSuite) checkRegistry() bool {
	return !util.FileExist(backupFile)
}

// 恢复备份文件
func (t *CacheTestingSuite) restoreBackupFile() error {
	return os.Rename(backupFileCp, backupFile)
}

func copy(src, dst string) (int64, error) {
	sourceFileStat, err := os.Stat(src)
	if err != nil {
		return 0, err
	}

	if !sourceFileStat.Mode().IsRegular() {
		return 0, fmt.Errorf("%s is not a regular file", src)
	}

	source, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer source.Close()

	destination, err := os.Create(dst)
	if err != nil {
		return 0, err
	}
	defer destination.Close()
	nBytes, err := io.Copy(destination, source)
	return nBytes, err
}

// 不开启缓存生效 -- 获取最新实例
// 开启缓存生效 -- 获取到缓存实例
// sleep , 获取到最新的实例，并且路由也生效
func (t *CacheTestingSuite) TestFirstGetUseCacheFile(c *check.C) {
	delErr := util.DeleteDir("./testdata/test_log/backup")
	fmt.Println("delErr: ", delErr)
	err1 := os.Mkdir("./testdata/test_log/backup", 644)
	c.Assert(err1, check.IsNil)
	defer util.DeleteDir("./testdata/test_log/backup")
	testService := &namingpb.Service{
		Name:      &wrappers.StringValue{Value: fmt.Sprintf("TestCacheFile")},
		Namespace: &wrappers.StringValue{Value: "Test"},
		Token:     &wrappers.StringValue{Value: uuid.New().String()},
	}
	t.mockServer.RegisterService(testService)
	t.mockServer.GenTestInstancesWithHostPortAndMeta(
		testService, 1, "127.0.0.1", 11080, map[string]string{"protocol": "http"})
	t.mockServer.GenTestInstancesWithHostPortAndMeta(
		testService, 1, "127.0.0.1", 11090, map[string]string{"protocol": "grpc"})
	defer t.mockServer.DeregisterService("Test", "TestCacheFile")

	_, err := copy("testdata/test_backup/svc#Test#TestCacheFile#instance.json", "testdata/test_log/backup/svc#Test#TestCacheFile#instance.json")
	c.Assert(err, check.IsNil)
	_, err = copy("testdata/test_backup/svc#Test#TestCacheFile#routing.json", "testdata/test_log/backup/svc#Test#TestCacheFile#routing.json")
	c.Assert(err, check.IsNil)
	_, err = copy("testdata/test_backup/client_info.json", "testdata/test_log/backup/client_info.json")
	c.Assert(err, check.IsNil)

	cfg0 := config.NewDefaultConfiguration(
		[]string{fmt.Sprintf("%s:%d", cacheIP, cachePort)})
	cfg0.GetConsumer().GetLocalCache().SetPersistDir("./testdata/test_log/backup")
	cfg0.GetConsumer().GetLocalCache().SetStartUseFileCache(false)
	consumer0, err := api.NewConsumerAPIByConfig(cfg0)
	c.Assert(err, check.IsNil)
	defer consumer0.Destroy()
	req0 := &api.GetInstancesRequest{}
	req0.Namespace = "Test"
	req0.Service = fmt.Sprintf("TestCacheFile")
	result0, err := consumer0.GetInstances(req0)
	c.Assert(err, check.IsNil)
	fmt.Println(result0.GetInstances())
	c.Assert(len(result0.GetInstances()), check.Equals, 2)
	c.Assert(result0.Instances[0].GetPort() > 11000, check.Equals, true)
	time.Sleep(time.Second * 1)

	_, err = copy("testdata/test_backup/svc#Test#TestCacheFile#instance.json", "testdata/test_log/backup/svc#Test#TestCacheFile#instance.json")
	c.Assert(err, check.IsNil)
	_, err = copy("testdata/test_backup/svc#Test#TestCacheFile#routing.json", "testdata/test_log/backup/svc#Test#TestCacheFile#routing.json")
	c.Assert(err, check.IsNil)
	_, err = copy("testdata/test_backup/client_info.json", "testdata/test_log/backup/client_info.json")
	c.Assert(err, check.IsNil)

	cfg := config.NewDefaultConfiguration(
		[]string{fmt.Sprintf("%s:%d", cacheIP, cachePort)})
	cfg.GetConsumer().GetLocalCache().SetPersistDir("./testdata/test_log/backup")
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()

	req := &api.GetOneInstanceRequest{}
	req.Namespace = "Test"
	req.Service = fmt.Sprintf("TestCacheFile")
	req.SourceService = &model.ServiceInfo{
		Service:   "TestCacheFile",
		Namespace: "Test",
		Metadata:  map[string]string{"tag": "protocol"},
	}
	result, err := consumer.GetOneInstance(req)
	c.Assert(err, check.IsNil)
	fmt.Println(result.GetInstances())
	c.Assert(len(result.GetInstances()), check.Equals, 1)
	c.Assert(result.Instances[0].GetPort() < 2000, check.Equals, true)
	time.Sleep(time.Second * 1)

	result, err = consumer.GetOneInstance(req)
	c.Assert(err, check.IsNil)
	fmt.Println(result.GetInstances())
	c.Assert(len(result.GetInstances()), check.Equals, 1)
	c.Assert(result.Instances[0].GetPort() > 2000, check.Equals, true)

	_, err = copy("testdata/test_backup/svc#Test#TestCacheFile#instance.json", "testdata/test_log/backup/svc#Test#TestCacheFile#instance.json")
	c.Assert(err, check.IsNil)
	_, err = copy("testdata/test_backup/svc#Test#TestCacheFile#routing.json", "testdata/test_log/backup/svc#Test#TestCacheFile#routing.json")
	c.Assert(err, check.IsNil)

	consumer1, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer1.Destroy()

	req1 := &api.GetInstancesRequest{}
	req1.Namespace = "Test"
	req1.Service = fmt.Sprintf("TestCacheFile")
	req1.SourceService = &model.ServiceInfo{
		Service:   "TestCacheFile",
		Namespace: "Test",
		Metadata:  map[string]string{"tag": "protocol"},
	}
	instancesResult, err := consumer1.GetInstances(req1)
	c.Assert(err, check.IsNil)
	fmt.Println(instancesResult.GetInstances())
	c.Assert(len(instancesResult.GetInstances()), check.Equals, 1)
	c.Assert(instancesResult.Instances[0].GetMetadata()["protocol"], check.Equals, "grpc")
	time.Sleep(time.Second * 1)
}

// 测试埋点不同缓存文件的路径不同
func (t *CacheTestingSuite) TestFileCachePwd(c *check.C) {
	t.FileCachePwdFunc(c)
}

func (t *CacheTestingSuite) FileCachePwdFunc(c *check.C) {
	user, err := user.Current()
	if err != nil {
		log.Fatalf(err.Error())
	}
	homeDir := user.HomeDir
	fmt.Printf("Home Directory: %s\n", homeDir)
	defer util.DeleteDir(fmt.Sprintf("%s/polaris/backup", homeDir))

	cfg := api.NewConfiguration()
	cfg.GetGlobal().GetServerConnector().SetAddresses([]string{fmt.Sprintf("%s:%d", cacheIP,
		cachePort)})
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()

	req := &api.GetOneInstanceRequest{}
	req.Namespace = cacheNS
	req.Service = cacheSVC
	result, err := consumer.GetOneInstance(req)
	c.Assert(err, check.IsNil)
	_ = result
	time.Sleep(time.Second * 3)
	// fmt.Println(runtime.GOOS)

	var filePath string
	filePath = fmt.Sprintf("%s/polaris/backup/svc#%s#%s#instance.json", homeDir, cacheNS, cacheSVC)
	_, err = os.Open(filePath)
	fmt.Println(err)
	c.Assert(err, check.IsNil)
}

// 测试缓存文件有效时间
func (t *CacheTestingSuite) TestFileCacheAvailableTime(c *check.C) {
	util.DeleteDir("./testdata/test_log/backup1")
	err1 := os.Mkdir("./testdata/test_log/backup1", 644)
	c.Assert(err1, check.IsNil)
	defer util.DeleteDir("./testdata/test_log/backup1")
	testService := &namingpb.Service{
		Name:      &wrappers.StringValue{Value: fmt.Sprintf("TestCacheFile1")},
		Namespace: &wrappers.StringValue{Value: "Test"},
		Token:     &wrappers.StringValue{Value: uuid.New().String()},
	}
	t.mockServer.RegisterService(testService)
	t.mockServer.GenTestInstancesWithHostPortAndMeta(
		testService, 1, "127.0.0.1", 11080, map[string]string{"protocol": "http"})
	t.mockServer.GenTestInstancesWithHostPortAndMeta(
		testService, 1, "127.0.0.1", 11090, map[string]string{"protocol": "grpc"})
	defer t.mockServer.DeregisterService("Test", "TestCacheFile1")

	cfg := api.NewConfiguration()
	cfg.GetGlobal().GetServerConnector().SetAddresses([]string{fmt.Sprintf("%s:%d", cacheIP,
		cachePort)})
	cfg.GetConsumer().GetLocalCache().SetPersistAvailableInterval(time.Second * 3)
	cfg.GetConsumer().GetLocalCache().SetPersistDir("./testdata/test_log/backup1")

	_, err := copy("testdata/test_backup/svc#Test#TestCacheFile1#instance.json", "testdata/test_log/backup1/svc#Test#TestCacheFile1#instance.json")
	c.Assert(err, check.IsNil)
	_, err = copy("testdata/test_backup/svc#Test#TestCacheFile1#routing.json", "testdata/test_log/backup1/svc#Test#TestCacheFile1#routing.json")
	c.Assert(err, check.IsNil)
	_, err = copy("testdata/test_backup/client_info.json", "testdata/test_log/backup1/client_info.json")
	c.Assert(err, check.IsNil)

	time.Sleep(time.Second * 4)
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()
	req := &api.GetOneInstanceRequest{}
	req.Namespace = "Test"
	req.Service = fmt.Sprintf("TestCacheFile1")
	req.SourceService = &model.ServiceInfo{
		Service:   "TestCacheFile1",
		Namespace: "Test",
		Metadata:  map[string]string{"tag": "protocol"},
	}
	result, err := consumer.GetOneInstance(req)
	c.Assert(err, check.IsNil)
	fmt.Println(result.GetInstances())
	c.Assert(len(result.GetInstances()), check.Equals, 1)
	c.Assert(result.Instances[0].GetPort() < 2000, check.Equals, false)
}
