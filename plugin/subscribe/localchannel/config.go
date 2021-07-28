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

package localchannel

const (
	DefaultChannelBufferSize = 30
)

//上报缓存信息插件的配置
type Config struct {
	ChannelBufferSize uint32 `yaml:"channelBufferSize" json:"channelBufferSize"`
}

//校验配置
func (c *Config) Verify() error {
	return nil
}

//设置默认配置值
func (c *Config) SetDefault() {
	if c.ChannelBufferSize == 0 {
		c.ChannelBufferSize = DefaultChannelBufferSize
	}
}
