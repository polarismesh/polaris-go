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
	"time"

	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/specification/source/go/api/v1/config_manage"
)

// ConfigFileResponse 配置文件响应体
type ConfigFileResponse struct {
	Code       uint32
	Message    string
	ConfigFile *ConfigFile
}

// GetCode 获取配置文件响应体code
func (c *ConfigFileResponse) GetCode() uint32 {
	return c.Code
}

// GetMessage 获取配置文件响应体信息
func (c *ConfigFileResponse) GetMessage() string {
	return c.Message
}

// GetConfigFile 获取配置文件响应体内容
func (c *ConfigFileResponse) GetConfigFile() *ConfigFile {
	return c.ConfigFile
}

type ConfigGroupResponse struct {
	Code         uint32
	Namespace    string
	Group        string
	Revision     string
	ReleaseFiles []*model.SimpleConfigFile
}

func (r *ConfigGroupResponse) ParseFromSpec(resp *config_manage.ConfigClientListResponse) error {
	r.Code = resp.GetCode().GetValue()
	r.Namespace = resp.GetNamespace()
	r.Group = resp.GetGroup()
	r.Revision = resp.GetRevision().GetValue()
	r.ReleaseFiles = make([]*model.SimpleConfigFile, 0, len(resp.GetConfigFileInfos()))
	for i := range resp.GetConfigFileInfos() {
		item := resp.GetConfigFileInfos()[i]

		releaseTime, err := time.Parse("2006-01-02 15:04:05", item.GetReleaseTime().GetValue())
		if err != nil {
			return err
		}

		r.ReleaseFiles = append(r.ReleaseFiles, &model.SimpleConfigFile{
			Namespace:   item.GetNamespace().GetValue(),
			FileGroup:   item.GetGroup().GetValue(),
			FileName:    item.GetFileName().GetValue(),
			Version:     item.GetVersion().GetValue(),
			Md5:         item.GetMd5().GetValue(),
			ReleaseTime: releaseTime,
		})
	}
	return nil
}
