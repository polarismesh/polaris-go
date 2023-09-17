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

package configuration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/hashicorp/go-multierror"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
)

const (
	// PatternService is the pattern of service name
	PatternService = "config#%s#%s#%s"
	// CacheSuffix filesystem suffix
	CacheSuffix = ".json"
	// PatternGlob is the pattern of glob
	PatternGlob = "#?*#?*#?*"
)

// CachePersistHandler 持久化工具类
type CachePersistHandler struct {
	persistDir    string
	maxWriteRetry int
	maxReadRetry  int
	retryInterval time.Duration
}

// CacheFileInfo 文件信息
type CacheFileInfo struct {
	Msg      proto.Message
	FileInfo os.FileInfo
}

// NewCachePersistHandler create persistence handler
func NewCachePersistHandler(persistDir string, maxWriteRetry int,
	maxReadRetry int, retryInterval time.Duration) (*CachePersistHandler, error) {
	handler := &CachePersistHandler{}
	handler.persistDir = persistDir
	handler.maxReadRetry = maxReadRetry
	handler.maxWriteRetry = maxWriteRetry
	handler.retryInterval = retryInterval
	if err := handler.init(); err != nil {
		return nil, model.NewSDKError(model.ErrCodeAPIInvalidConfig, err, "fail to init cachePersistHandler")
	}
	return handler, nil
}

// 持久化配置初始化
func (cph *CachePersistHandler) init() error {
	if err := model.EnsureAndVerifyDir(cph.persistDir); err != nil {
		return err
	}
	return nil
}

// LoadMessageFromFile 从相对文件中加载缓存
func (cph *CachePersistHandler) LoadMessageFromFile(relativeFile string, message interface{}) error {
	absFile := filepath.Join(cph.persistDir, relativeFile)
	return cph.loadMessageFromAbsoluteFile(absFile, message, cph.maxReadRetry)
}

// 从绝对文件中加载缓存
func (cph *CachePersistHandler) loadMessageFromAbsoluteFile(cacheFile string, message interface{},
	maxRetry int) error {
	log.GetBaseLogger().Infof("Start to load cache from %s", cacheFile)
	var lastErr error
	var retryTimes int
	for retryTimes = 0; retryTimes <= maxRetry; retryTimes++ {
		cacheJson, err := os.ReadFile(cacheFile)
		if err != nil {
			lastErr = model.NewSDKError(model.ErrCodeDiskError, err, "fail to read file cache")
			// 文件打开失败的话，重试没有意义，直接失败
			break
		}
		if err := json.Unmarshal(cacheJson, message); err != nil {
			lastErr = multierror.Prefix(err, "Fail to unmarshal file cache: ")
			time.Sleep(cph.retryInterval)
			// 解码失败可能是读到了部分数据，所以这里可以重试
			continue
		}
		return nil
	}
	return multierror.Prefix(lastErr,
		fmt.Sprintf("load message from %s failed after retry %d times", cacheFile, retryTimes))
}

// DeleteCacheFromFile 删除缓存文件
func (cph *CachePersistHandler) DeleteCacheFromFile(fileName string) {
	fileToDelete := filepath.Join(cph.persistDir, fileName)
	log.GetBaseLogger().Infof("Start to delete cache for %s", fileToDelete)
	for retryTimes := 0; retryTimes <= cph.maxWriteRetry; retryTimes++ {
		err := os.Remove(fileToDelete)
		if err != nil {
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

// SaveMessageToFile 按服务来进行缓存存储
func (cph *CachePersistHandler) SaveMessageToFile(fileName string, svcResp interface{}) {
	fileToAdd := filepath.Join(cph.persistDir, fileName)
	log.GetBaseLogger().Infof("Start to save cache to file %s", fileToAdd)
	msg, err := json.Marshal(svcResp)
	if err != nil {
		log.GetBaseLogger().Warnf("Fail to marshal the service response for %s", fileToAdd)
		return
	}
	for retryTimes := 0; retryTimes <= cph.maxWriteRetry; retryTimes++ {
		err = cph.doWriteFile(fileToAdd, []byte(msg))
		if err != nil {
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

// 实际写文件
func (cph *CachePersistHandler) doWriteFile(cacheFile string, msg []byte) error {
	tempFileName := cacheFile + ".tmp"
	tmpFile, err := os.OpenFile(tempFileName, os.O_WRONLY|os.O_CREATE, 0600)
	if err != nil {
		return model.NewSDKError(model.ErrCodeDiskError, err, "fail to open file %s to write", tempFileName)
	}
	n, err := tmpFile.Write(msg)
	if err == nil && n < len(msg) {
		return model.NewSDKError(model.ErrCodeDiskError, nil, "unable to write all bytes to file %s", tempFileName)
	}
	if err = cph.closeTmpFile(tmpFile, cacheFile); err != nil {
		_ = os.Remove(tempFileName)
		return err
	}
	return nil
}

// 关闭文件
func (cph *CachePersistHandler) closeTmpFile(tmpFile *os.File, cacheFile string) error {
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
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
