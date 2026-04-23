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

package lane

// BaseLaneMode 基线泳道模式
type BaseLaneMode int32

const (
	// OnlyUntaggedInstance 只选取没有任何泳道标签的实例作为基线
	OnlyUntaggedInstance BaseLaneMode = 0
	// ExcludeEnabledLaneInstance 排除已启用泳道规则所关联的实例，其余实例作为基线
	ExcludeEnabledLaneInstance BaseLaneMode = 1
)

// Config 泳道路由插件配置
type Config struct {
	// BaseLaneMode 基线泳道模式，默认 OnlyUntaggedInstance
	BaseLaneMode BaseLaneMode `yaml:"baseLaneMode"`
}

// SetDefault 设置默认值
//
// BaseLaneMode 的默认值即 Go 零值(OnlyUntaggedInstance=0),因此无需显式赋值。
// 注意:这里 **不能**调用 `c.BaseLaneMode = OnlyUntaggedInstance`,否则会在 YAML 解析
// 后的 SetDefault 环节把用户显式设置的非零值(如 ExcludeEnabledLaneInstance=1)覆盖回 0,
// 导致配置失效。参见 pkg/config/plugin.go:convertFromTextValues + PluginConfigs.SetDefault
// 的执行顺序:Marshal→Unmarshal→SetDefault。
func (c *Config) SetDefault() {
	// intentionally empty: zero value of BaseLaneMode is OnlyUntaggedInstance
}

// Verify 校验配置
func (c *Config) Verify() error {
	return nil
}
