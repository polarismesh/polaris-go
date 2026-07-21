/*
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
 *  under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
 * CONDITIONS OF ANY KIND, either express or implied. See the License for the
 * specific language governing permissions and limitations under the License.
 *
 */

package configconnector

import (
	"bytes"
	"encoding/json"
	"strconv"

	"github.com/polarismesh/specification/source/go/api/v1/config_manage"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/polarismesh/polaris-go/pkg/model"
)

const (
	// ConfigFileTagKeyUseEncrypted 配置加密开关标识，value 为 boolean
	ConfigFileTagKeyUseEncrypted = "internal-encrypted"
	// ConfigFileTagKeyDataKey 加密密钥 tag key
	ConfigFileTagKeyDataKey = "internal-datakey"
	// ConfigFileTagKeyEncryptAlgo 加密算法 tag key
	ConfigFileTagKeyEncryptAlgo = "internal-encryptalgo"
)

// ConfigFile 配置文件
type ConfigFile struct {
	Namespace     string
	FileGroup     string
	FileName      string
	SourceContent string
	Version       uint64
	Md5           string
	Encrypted     bool
	PublicKey     string
	Tags          []*ConfigFileTag
	VersionName   string
	// 实际暴露给应用的配置内容数据
	content string
	// 该配置文件是否为不存在的场景下的占位信息
	NotExist bool
	// mode=0，默认模式，获取SDK使用的配置文件, mode=1，SDK模式，同mode=1,mode=2，Agent模式，获取Agent使用的配置文件
	Mode model.GetConfigFileRequestMode
	// 文件持久化配置
	Persistent model.Persistent
}

func (c *ConfigFile) String() string {
	var bf bytes.Buffer
	_, _ = bf.WriteString("namespace=" + c.Namespace)
	_, _ = bf.WriteString("group=" + c.FileGroup)
	_, _ = bf.WriteString("file_name=" + c.FileName)
	_, _ = bf.WriteString("version=" + strconv.FormatUint(c.Version, 10))
	_, _ = bf.WriteString("encrypt=" + strconv.FormatBool(c.Encrypted))
	// 加密场景下对敏感 tag 值做掩码，避免解密后的明文数据密钥等敏感信息进入日志
	maskedTags := maskTags(c.Tags, c.Encrypted)
	//nolint: errchkjson
	data, _ := json.Marshal(maskedTags)
	_, _ = bf.WriteString("tags=" + string(data))
	return bf.String()
}

// maskedTagValue 加密场景下敏感 tag 掩码后的占位值
const maskedTagValue = "******"

// maskTags 对加密场景下的敏感 Tag 值进行掩码处理。
// tags      待处理的配置文件标签切片，允许为 nil（非加密场景原样返回，加密场景返回长度一致的新切片）。
// encrypted 是否为加密配置；仅加密场景才对敏感 tag 做掩码，非加密场景直接返回入参 tags。
// 返回值    加密场景返回掩码后的新切片（不修改入参元素，避免污染缓存/上游对象），非加密场景返回原 tags。
// 掩码目标  internal-datakey（解密后的明文数据密钥）与 internal-encryptalgo（加密算法标识）。
func maskTags(tags []*ConfigFileTag, encrypted bool) []*ConfigFileTag {
	if !encrypted {
		return tags
	}
	masked := make([]*ConfigFileTag, len(tags))
	for i, tag := range tags {
		if tag != nil && (tag.Key == ConfigFileTagKeyDataKey || tag.Key == ConfigFileTagKeyEncryptAlgo) {
			masked[i] = &ConfigFileTag{Key: tag.Key, Value: maskedTagValue}
			continue
		}
		masked[i] = tag
	}
	return masked
}

type ConfigFileTag struct {
	Key   string
	Value string
}

func (c *ConfigFile) GetLabels() map[string]string {
	ret := make(map[string]string, len(c.Tags))
	for i := range c.Tags {
		ret[c.Tags[i].Key] = c.Tags[i].Value
	}
	return ret
}

// GetNamespace 获取配置文件命名空间
func (c *ConfigFile) GetNamespace() string {
	return c.Namespace
}

// GetFileGroup 获取配置文件组
func (c *ConfigFile) GetFileGroup() string {
	return c.FileGroup
}

// GetFileName 获取配置文件名
func (c *ConfigFile) GetFileName() string {
	return c.FileName
}

// GetSourceContent 获取配置文件内容
func (c *ConfigFile) GetSourceContent() string {
	return c.SourceContent
}

func (c *ConfigFile) SetContent(v string) {
	c.content = v
}

// GetContent 获取配置文件内容
func (c *ConfigFile) GetContent() string {
	return c.content
}

// GetVersion 获取配置文件版本号
func (c *ConfigFile) GetVersion() uint64 {
	return c.Version
}

func (c *ConfigFile) GetVersionName() string {
	return c.VersionName
}

// GetMd5 获取配置文件MD5值
func (c *ConfigFile) GetMd5() string {
	return c.Md5
}

// GetEncrypted 获取配置文件是否为加密文件
func (c *ConfigFile) GetEncrypted() bool {
	return c.Encrypted
}

// GetPublicKey 获取配置文件公钥
func (c *ConfigFile) GetPublicKey() string {
	return c.PublicKey
}

// GetDataKey 获取配置文件数据加密密钥
func (c *ConfigFile) GetDataKey() string {
	for _, tag := range c.Tags {
		if tag.Key == ConfigFileTagKeyDataKey {
			return tag.Value
		}
	}
	return ""
}

// GetPersistent 获取文件持久化数据
func (c *ConfigFile) GetPersistent() model.Persistent {
	return c.Persistent
}

// GetFileMode 获取文件Mode
func (c *ConfigFile) GetFileMode() model.GetConfigFileRequestMode {
	return c.Mode
}

// GetEncryptAlgo 获取配置文件数据加密算法
func (c *ConfigFile) GetEncryptAlgo() string {
	for _, tag := range c.Tags {
		if tag.Key == ConfigFileTagKeyEncryptAlgo {
			return tag.Value
		}
	}
	return ""
}

type ConfigGroup struct {
	Namespace    string
	Group        string
	Revision     string
	Mode         model.GetConfigFileRequestMode
	ReleaseFiles []*model.SimpleConfigFile
}

func (c *ConfigGroup) ToSpecQuery() *config_manage.ConfigFileGroupRequest {
	return &config_manage.ConfigFileGroupRequest{
		Revision: wrapperspb.String(c.Revision),
		ConfigFileGroup: &config_manage.ConfigFileGroup{
			Name:      wrapperspb.String(c.Group),
			Namespace: wrapperspb.String(c.Namespace),
		},
		ClientType: config_manage.ConfigClientType(c.Mode),
	}
}
