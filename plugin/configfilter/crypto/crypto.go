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

package crypto

import (
	"fmt"
	"sync"

	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/configconnector"
	"github.com/polarismesh/polaris-go/pkg/plugin/configfilter"
	"github.com/polarismesh/polaris-go/plugin/configfilter/crypto/rsa"
)

const (
	// PluginName crypto
	PluginName = "crypto"
	separator  = "+"
)

func init() {
	plugin.RegisterConfigurablePlugin(&CryptoFilter{}, &Config{})
}

// CryptoFilter crypto filter plugin
type CryptoFilter struct {
	*plugin.PluginBase
	cfg          *Config
	cryptos      map[string]Crypto
	dataKeyCache *sync.Map
}

// Type plugin type
func (c *CryptoFilter) Type() common.Type {
	return common.TypeConfigFilter
}

// Name plugin name
func (c *CryptoFilter) Name() string {
	return PluginName
}

// Init plugin
func (c *CryptoFilter) Init(ctx *plugin.InitContext) error {
	c.PluginBase = plugin.NewPluginBase(ctx)
	c.cryptos = make(map[string]Crypto)
	c.dataKeyCache = new(sync.Map)

	cfgValue := ctx.Config.GetConfigFile().GetConfigFilterConfig().GetPluginConfig(c.Name())
	if cfgValue != nil {
		c.cfg = cfgValue.(*Config)
	}
	for i := range c.cfg.Entries {
		entry := c.cfg.Entries[i]
		item, exist := cryptorSet[entry.Name]
		if !exist {
			log.GetBaseLogger().Errorf("plugin Crypto not found target: %s", entry.Name)
			continue
		}
		crypto, ok := item.(Crypto)
		if !ok {
			log.GetBaseLogger().Errorf("plugin target: %s not Crypto", entry.Name)
			continue
		}
		c.cryptos[entry.Name] = crypto
	}
	return nil
}

// Destroy plugin
func (c *CryptoFilter) Destroy() error {
	return nil
}

// IsEnable enable
func (c *CryptoFilter) IsEnable(cfg config.Configuration) bool {
	return cfg.GetGlobal().GetSystem().GetMode() != model.ModeWithAgent
}

// DoFilter do crypto filter
func (c *CryptoFilter) DoFilter(configFile *configconnector.ConfigFile, next configfilter.ConfigFileHandleFunc) configfilter.ConfigFileHandleFunc {
	return func(configFile *configconnector.ConfigFile) (*configconnector.ConfigFileResponse, error) {
		// 查询缓存的数据密钥
		cacheKey := genCacheKey(configFile.Namespace, configFile.FileGroup, configFile.FileName)
		cacheEncryptInfo := c.getEncryptInfo(cacheKey)

		var privateKey *rsa.RSAKey
		var err error
		// 如果是加密配置并且缓存密钥为空
		if configFile.GetEncrypted() && cacheEncryptInfo == nil {
			// 生成公钥和私钥请求数据密钥
			privateKey, err = rsa.GenerateRSAKey()
			if err != nil {
				return nil, err
			}
			configFile.PublicKey = privateKey.PublicKey
		}

		resp, err := next(configFile)
		if err != nil {
			return resp, err
		}
		// 如果是加密配置
		if resp.GetConfigFile().GetEncrypted() && resp.GetConfigFile().GetContent() != "" {
			cipherContent := resp.GetConfigFile().GetContent()
			cipherDataKey := resp.GetConfigFile().GetDataKey()
			encryptAlgo := resp.GetConfigFile().GetEncryptAlgo()

			// 返回了数据密钥，解密配置
			if cipherDataKey != "" && privateKey != nil {
				crypto, err := c.GetCrypto(encryptAlgo)
				if err != nil {
					return nil, err
				}
				dataKey, err := rsa.DecryptFromBase64(cipherDataKey, privateKey.PrivateKey)
				if err != nil {
					return nil, err
				}
				plainContent, err := crypto.Decrypt(cipherContent, dataKey)
				if err != nil {
					return nil, err
				}
				resp.ConfigFile.Content = string(plainContent)
				// 缓存数据密钥
				c.setEncryptInfo(cacheKey, &encryptInfo{
					Key:  dataKey,
					Algo: encryptAlgo,
				})
			} else if cacheEncryptInfo != nil {
				// 有缓存的数据密钥和加密算法
				crypto, err := c.GetCrypto(cacheEncryptInfo.Algo)
				if err != nil {
					return nil, err
				}
				plainContent, err := crypto.Decrypt(cipherContent, cacheEncryptInfo.Key)
				if err != nil {
					return nil, err
				}
				resp.ConfigFile.Content = string(plainContent)
			} else {
				// 没有返回数据密钥，设置为加密配置重新请求
				configFile.Encrypted = true
				return c.DoFilter(configFile, next)(configFile)
			}
		}
		return resp, err
	}
}

// GetCrypto get crypto by algorithm
func (c *CryptoFilter) GetCrypto(algo string) (Crypto, error) {
	crypto, ok := c.cryptos[algo]
	if !ok {
		log.GetBaseLogger().Errorf("plugin Crypto not found target: %s", algo)
		return nil, fmt.Errorf("plugin Crypto not found target: %s", algo)
	}
	return crypto, nil
}

type encryptInfo struct {
	Key  []byte
	Algo string
}

func (c *CryptoFilter) getEncryptInfo(key string) *encryptInfo {
	obj, ok := c.dataKeyCache.Load(key)
	if ok {
		return obj.(*encryptInfo)
	}
	return nil
}

func (c *CryptoFilter) setEncryptInfo(key string, value *encryptInfo) {
	c.dataKeyCache.Store(key, value)
}

func genCacheKey(namespace, fileGroup, fileName string) string {
	return namespace + separator + fileGroup + separator + fileName
}

// Crypto Crypto interface
type Crypto interface {
	GenerateKey() ([]byte, error)
	Encrypt(plaintext string, key []byte) (cryptotext string, err error)
	Decrypt(cryptotext string, key []byte) (string, error)
}

var cryptorSet = make(map[string]Crypto)

// RegisterCrypto register crypto
func RegisterCrypto(name string, crypto Crypto) {
	if _, exist := cryptorSet[name]; exist {
		panic(fmt.Sprintf("existed cryptor: name=%v", name))
	}
	cryptorSet[name] = crypto
}
