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

const (
	// ConfigFileTagKeyDataKey 加密密钥 tag key
	ConfigFileTagKeyDataKey = "data_key"
	// ConfigFileTagKeyEncryptAlgo 加密算法 tag key
	ConfigFileTagKeyEncryptAlgo = "encrypt_algo"
)

// ConfigFile 配置文件
type ConfigFile struct {
	Namespace string
	FileGroup string
	FileName  string
	Content   string
	Version   uint64
	Md5       string
	Encrypted bool
	PublicKey string
	Tags      []*ConfigFileTag
}

type ConfigFileTag struct {
	Key   string
	Value string
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

// GetContent 获取配置文件内容
func (c *ConfigFile) GetContent() string {
	return c.Content
}

// GetVersion 获取配置文件版本号
func (c *ConfigFile) GetVersion() uint64 {
	return c.Version
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

// GetEncryptAlgo 获取配置文件数据加密算法
func (c *ConfigFile) GetEncryptAlgo() string {
	for _, tag := range c.Tags {
		if tag.Key == ConfigFileTagKeyEncryptAlgo {
			return tag.Value
		}
	}
	return ""
}
