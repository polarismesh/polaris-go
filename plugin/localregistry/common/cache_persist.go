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

package common

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/pb"
	namingpb "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
	"github.com/golang/protobuf/jsonpb"
	"github.com/golang/protobuf/proto"
	"github.com/hashicorp/go-multierror"
)

const (
	PatternService = "svc#%s#%s#%s"
	CacheSuffix    = ".json"
	PatternGlob    = "svc#?*#?*#?*"
)

//持久化工具类
type CachePersistHandler struct {
	persistDir    string
	maxWriteRetry int
	maxReadRetry  int
	retryInterval time.Duration
	marshaler     *jsonpb.Marshaler
}

type CacheFileInfo struct {
	Msg      proto.Message
	FileInfo os.FileInfo
}

//创建持久化处理器
func NewCachePersistHandler(persistDir string, maxWriteRetry int,
	maxReadRetry int, retryInterval time.Duration) (*CachePersistHandler, error) {
	handler := &CachePersistHandler{}
	handler.persistDir = persistDir
	handler.maxReadRetry = maxReadRetry
	handler.maxWriteRetry = maxWriteRetry
	handler.retryInterval = retryInterval
	if err := handler.init(); nil != err {
		return nil, model.NewSDKError(model.ErrCodeAPIInvalidConfig, err, "fail to init cachePersistHandler")
	}
	return handler, nil
}

//持久化配置初始化
func (cph *CachePersistHandler) init() error {
	if nil == cph.marshaler {
		cph.marshaler = &jsonpb.Marshaler{}
	}
	if err := model.EnsureAndVerifyDir(cph.persistDir); nil != err {
		return err
	}
	return nil
}

//加载目录中所有的缓存文件
func (cph *CachePersistHandler) LoadPersistedServices() map[model.ServiceEventKey]CacheFileInfo {
	cacheFiles, _ := filepath.Glob(filepath.Join(cph.persistDir, PatternGlob+CacheSuffix))
	if len(cacheFiles) == 0 {
		return nil
	}
	values := make(map[model.ServiceEventKey]CacheFileInfo, len(cacheFiles))
	for _, cacheFile := range cacheFiles {
		msg := &namingpb.DiscoverResponse{}
		svcValueKey, fileInfo, err := cph.loadCacheFromFile(cacheFile, msg)
		if nil != err {
			log.GetBaseLogger().Errorf("fail to load cache from file %s, error is %v", cacheFile, err)
			continue
		}
		//加载缓存时，也要将实例进行排序
		sort.Sort(pb.InstSlice(msg.Instances))
		info := CacheFileInfo{
			Msg:      msg,
			FileInfo: fileInfo,
		}
		values[*svcValueKey] = info
	}
	return values
}

//从文件中加载服务缓存
func (cph *CachePersistHandler) loadCacheFromFile(
	cacheFile string, message proto.Message) (*model.ServiceEventKey, os.FileInfo, error) {
	svcValueKey, err := cph.fileNameToServiceEventKey(cacheFile)
	if nil != err {
		return nil, nil, multierror.Prefix(err, fmt.Sprintf("Fail to decode the cache file name %s: ", cacheFile))
	}
	fileInfo, err := os.Stat(cacheFile)
	if err != nil {
		return svcValueKey, nil, multierror.Prefix(err, fmt.Sprintf("Fail to Stat the cache file name %s: ",
			cacheFile))
	}
	if err = cph.loadMessageFromAbsoluteFile(cacheFile, message, 0); nil != err {
		return svcValueKey, nil, err
	}
	if err = pb.ValidateMessage(svcValueKey, message); nil != err {
		return svcValueKey, nil, multierror.Prefix(err, "Fail to validate file cache: ")
	}
	return svcValueKey, fileInfo, nil
}

//从相对文件中加载缓存
func (cph *CachePersistHandler) LoadMessageFromFile(relativeFile string, message proto.Message) error {
	absFile := filepath.Join(cph.persistDir, relativeFile)
	return cph.loadMessageFromAbsoluteFile(absFile, message, cph.maxReadRetry)
}

//从绝对文件中加载缓存
func (cph *CachePersistHandler) loadMessageFromAbsoluteFile(cacheFile string, message proto.Message,
	maxRetry int) error {
	log.GetBaseLogger().Infof("Start to load cache from %s", cacheFile)
	var lastErr error
	var retryTimes int
	for retryTimes = 0; retryTimes <= maxRetry; retryTimes++ {
		cacheJson, err := os.OpenFile(cacheFile, os.O_RDONLY, 0600)
		if nil != err {
			lastErr = model.NewSDKError(model.ErrCodeDiskError, err, "fail to read file cache")
			//文件打开失败的话，重试没有意义，直接失败
			break
		}
		err = jsonpb.Unmarshal(cacheJson, message)
		cacheJson.Close()
		if nil != err {
			lastErr = multierror.Prefix(err, "Fail to unmarshal file cache: ")
			time.Sleep(cph.retryInterval)
			//解码失败可能是读到了部分数据，所以这里可以重试
			continue
		}
		return nil
	}
	return multierror.Prefix(lastErr,
		fmt.Sprintf("load message from %s failed after retry %d times", cacheFile, retryTimes))
}

//从文件名转化为serviceKey
func (cph *CachePersistHandler) fileNameToServiceEventKey(fileName string) (*model.ServiceEventKey, error) {
	svcKeyFile := fileName[0 : len(fileName)-len(CacheSuffix)]
	pieces := strings.Split(svcKeyFile, "#")
	namespace, err := url.QueryUnescape(pieces[1])
	if nil != err {
		return nil, err
	}
	svc, err := url.QueryUnescape(pieces[2])
	if nil != err {
		return nil, err
	}
	eventType := model.ToEventType(pieces[3])
	if eventType == model.EventUnknown {
		return nil, fmt.Errorf("unknow event type %s", pieces[3])
	}
	svcValueKey := &model.ServiceEventKey{}
	svcValueKey.Namespace = namespace
	svcValueKey.Service = svc
	svcValueKey.Type = eventType
	return svcValueKey, nil
}

//删除缓存文件
func (cph *CachePersistHandler) DeleteCacheFromFile(fileName string) {
	fileToDelete := filepath.Join(cph.persistDir, fileName)
	log.GetBaseLogger().Infof("Start to delete cache for %s", fileToDelete)
	for retryTimes := 0; retryTimes <= cph.maxWriteRetry; retryTimes++ {
		err := os.Remove(fileToDelete)
		if nil != err {
			if !os.IsNotExist(err) {
				log.GetBaseLogger().Warnf("Fail to delete cache file %s,"+
					" because %s, next retrytimes %d", fileToDelete, err.Error(), retryTimes)
			} else {
				log.GetBaseLogger().Warnf("Fail to delete cache file %s,"+
					" error %s is not retryable, stop retrying", fileToDelete, err.Error())
				return
			}
		} else {
			log.GetBaseLogger().Infof("Success to delete cache file %s", fileToDelete)
			return
		}
		time.Sleep(cph.retryInterval)
	}
}

//按服务来进行缓存存储
func (cph *CachePersistHandler) SaveMessageToFile(fileName string, svcResp proto.Message) {
	fileToAdd := filepath.Join(cph.persistDir, fileName)
	log.GetBaseLogger().Infof("Start to save cache to file %s", fileToAdd)
	msg, err := cph.marshaler.MarshalToString(svcResp)
	if nil != err {
		log.GetBaseLogger().Warnf("Fail to marshal the service response for %s", fileToAdd)
		return
	}
	for retryTimes := 0; retryTimes <= cph.maxWriteRetry; retryTimes++ {
		err = cph.doWriteFile(fileToAdd, []byte(msg))
		if nil != err {
			if retryTimes > 0 {
				log.GetBaseLogger().Warnf("Fail to write cache file %s, error: %s,"+
					" retry times: %v", fileToAdd, err.Error(), retryTimes)
			}
		} else {
			log.GetBaseLogger().Infof("Success to write cache file %s", fileToAdd)
			return
		}
		time.Sleep(cph.retryInterval)
	}
}

//实际写文件
func (cph *CachePersistHandler) doWriteFile(cacheFile string, msg []byte) error {
	tempFileName := cacheFile + ".tmp"
	tmpFile, err := os.OpenFile(tempFileName, os.O_WRONLY|os.O_CREATE, 0600)
	if nil != err {
		return model.NewSDKError(model.ErrCodeDiskError, err, "fail to open file %s to write", tempFileName)
	}
	n, err := tmpFile.Write(msg)
	if nil == err && n < len(msg) {
		return model.NewSDKError(model.ErrCodeDiskError, nil, "unable to write all bytes to file %s", tempFileName)
	}
	if err = cph.closeTmpFile(tmpFile, cacheFile); nil != err {
		os.Remove(tempFileName)
		return err
	}
	return nil
}

//关闭文件
func (cph *CachePersistHandler) closeTmpFile(tmpFile *os.File, cacheFile string) error {
	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		return model.NewSDKError(model.ErrCodeDiskError, err, "fail to sync file %s", tmpFile.Name())
	}
	if err := tmpFile.Close(); err != nil {
		return model.NewSDKError(model.ErrCodeDiskError, err, "fail to close file %s", tmpFile.Name())
	}
	if model.PathExist(cacheFile) {
		if err := os.Chmod(cacheFile, 0600); err != nil {
			return model.NewSDKError(model.ErrCodeDiskError, err, "fail to chmod file %s", cacheFile)
		}
	}
	err := os.Rename(tmpFile.Name(), cacheFile)
	if err != nil {
		return model.NewSDKError(model.ErrCodeDiskError, err, "fail to rename file %s to %s", tmpFile.Name(), cacheFile)
	}
	return nil
}

//服务名转化为文件名
func ServiceEventKeyToFileName(svcKey model.ServiceEventKey) string {
	svcKey.Namespace = url.QueryEscape(svcKey.Namespace)
	svcKey.Service = url.QueryEscape(svcKey.Service)
	return fmt.Sprintf(PatternService, svcKey.Namespace, svcKey.Service, svcKey.Type) + CacheSuffix
}
