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

package nearbybase

import (
	"fmt"
	"github.com/polarismesh/polaris-go/pkg/config"
)

//就近路由的配置
type nearbyConfig struct {
	MatchLevel                      string `yaml:"matchLevel" json:"matchLevel"`
	MaxMatchLevel                   string `yaml:"maxMatchLevel" json:"maxMatchLevel"`
	StrictNearby                    bool   `yaml:"strictNearby" json:"strictNearby"`
	EnableDegradeByUnhealthyPercent *bool  `yaml:"enableDegradeByUnhealthyPercent" json:"enableDegradeByUnhealthyPercent"`
	UnhealthyPercentToDegrade       int    `yaml:"unhealthyPercentToDegrade" json:"unhealthyPercentToDegrade"`
}

//设置配置级别
func (n *nearbyConfig) SetMatchLevel(level string) {
	n.MatchLevel = level
}

//返回配置级别
func (n *nearbyConfig) GetMatchLevel() string {
	return n.MatchLevel
}

//设置配置级别
func (n *nearbyConfig) SetLowestMatchLevel(level string) {
	n.MaxMatchLevel = level
}

//返回配置级别
func (n *nearbyConfig) GetLowestMatchLevel() string {
	return n.MaxMatchLevel
}

//设置最大降级匹配级别
func (n *nearbyConfig) SetMaxMatchLevel(level string) {
	n.MaxMatchLevel = level
}

//返回最大降级匹配级别
func (n *nearbyConfig) GetMaxMatchLevel() string {
	return n.MaxMatchLevel
}

//
func (n *nearbyConfig) SetStrictNearby(s bool) {
	n.StrictNearby = s
}

//
func (n *nearbyConfig) IsStrictNearby() bool {
	return n.StrictNearby
}

//
func (n *nearbyConfig) IsEnableDegradeByUnhealthyPercent() bool {
	if n.EnableDegradeByUnhealthyPercent == nil {
		return true
	}
	return *n.EnableDegradeByUnhealthyPercent
}

//
func (n *nearbyConfig) SetEnableDegradeByUnhealthyPercent(e bool) {
	n.EnableDegradeByUnhealthyPercent = &e
}

//
func (n *nearbyConfig) GetUnhealthyPercentToDegrade() int {
	return n.UnhealthyPercentToDegrade
}

//
func (n *nearbyConfig) SetUnhealthyPercentToDegrade(u int) {
	n.UnhealthyPercentToDegrade = u
}

//设置默认值
func (n *nearbyConfig) SetDefault() {
	if "" == n.MatchLevel {
		n.MatchLevel = config.DefaultMatchLevel
	}
	if 0 == n.UnhealthyPercentToDegrade {
		n.UnhealthyPercentToDegrade = 100
	}
	if nil == n.EnableDegradeByUnhealthyPercent {
		defaultEnable := true
		n.EnableDegradeByUnhealthyPercent = &defaultEnable
	}
}

//就近级别转换
var nearbyLevels = map[string]int{
	"":                 priorityLevelAll,
	config.RegionLevel: priorityLevelRegion,
	config.ZoneLevel:   priorityLevelZone,
	config.CampusLevel: priorityLevelCampus,
}

//校验
func (n *nearbyConfig) Verify() error {
	if config.RegionLevel != n.MatchLevel && config.ZoneLevel != n.MatchLevel && config.CampusLevel != n.MatchLevel {
		return fmt.Errorf("invalud match level for nearby router: %s, it must be one of %s, %s and %s",
			n.MatchLevel, config.RegionLevel, config.ZoneLevel, config.CampusLevel)
	}
	if config.RegionLevel != n.MaxMatchLevel && config.ZoneLevel != n.MaxMatchLevel &&
		config.CampusLevel != n.MaxMatchLevel && config.AllLevel != n.MaxMatchLevel {
		return fmt.Errorf("invalud highest match level for nearby router: %s, it must be one of %s, %s and %s",
			n.MaxMatchLevel, config.RegionLevel, config.ZoneLevel, config.CampusLevel)
	}
	if nearbyLevels[n.MaxMatchLevel] > nearbyLevels[n.MatchLevel] {
		return fmt.Errorf("maxMatchLevel \"%s\" is less than matchLevel \"%s\"",
			n.MaxMatchLevel, n.MatchLevel)
	}
	if n.UnhealthyPercentToDegrade > 100 || n.UnhealthyPercentToDegrade <= 0 {
		return fmt.Errorf("unhealthyPercentToDegrade must be in the range of (0,100],"+
			" but provided value is %v", n.UnhealthyPercentToDegrade)
	}
	return nil
}
