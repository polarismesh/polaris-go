/**
 * Tencent is pleased to support the open source community by making Polaris available.
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

package util

import (
	"github.com/polarismesh/polaris-go/pkg/model"
	"strings"
)

const (
	separator = "+"
)

// GenConfigFileCacheKey 生成配置文件缓存的 Key
func GenConfigFileCacheKey(namespace, fileGroup, fileName string) string {
	return namespace + separator + fileGroup + separator + fileName
}

// GenConfigFileCacheKeyByMetadata 生成配置文件缓存的 Key
func GenConfigFileCacheKeyByMetadata(configFileMetadata model.ConfigFileMetadata) string {
	return GenConfigFileCacheKey(configFileMetadata.GetNamespace(), configFileMetadata.GetFileGroup(), configFileMetadata.GetFileName())
}

// ExtractConfigFileMetadataFromKey 从配置文件 Key 解析出配置文件元数据
func ExtractConfigFileMetadataFromKey(key string) model.ConfigFileMetadata {
	info := strings.Split(key, separator)
	return &model.DefaultConfigFileMetadata{
		Namespace: info[0],
		FileGroup: info[1],
		FileName: info[2],
	}
}
