// Tencent is pleased to support the open source community by making polaris-go available.
//
// Copyright (C) 2021 Tencent. All rights reserved.
//
// Licensed under the BSD 3-Clause License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// https://opensource.org/licenses/BSD-3-Clause
//
// Unless required by applicable law or agreed to in writing, software distributed
// under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
// CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package polaris

import (
	skillmanage "github.com/polarismesh/specification/source/go/api/v1/skill_manage"

	"github.com/polarismesh/polaris-go/pkg/plugin/skillconnector"
)

func convertResource(resource *skillmanage.SkillResource) *skillconnector.Resource {
	if resource == nil {
		return nil
	}
	result := &skillconnector.Resource{
		ID:            resource.GetId(),
		Name:          resource.GetName(),
		Namespace:     resource.GetNamespace(),
		Description:   resource.GetDescription(),
		Status:        resource.GetStatus(),
		Tags:          append([]string(nil), resource.GetTags()...),
		Examples:      append([]string(nil), resource.GetExamples()...),
		Ext:           cloneStringMap(resource.GetExt()),
		Source:        resource.GetSource(),
		MetaVersion:   resource.GetMetaVersion(),
		Scope:         resource.GetScope(),
		Owner:         resource.GetOwner(),
		DownloadCount: resource.GetDownloadCount(),
		CreateTime:    resource.GetCreateTime(),
		ModifyTime:    resource.GetModifyTime(),
	}
	if info := resource.GetVersionInfo(); info != nil {
		result.VersionInfo = &skillconnector.VersionInfo{
			ActiveVersion: info.GetActiveVersion(),
			LatestVersion: info.GetLatestVersion(),
			TotalVersions: info.GetTotalVersions(),
		}
	}
	return result
}

func convertResourceVersion(version *skillmanage.SkillResourceVersion) *skillconnector.ResourceVersion {
	if version == nil {
		return nil
	}
	result := &skillconnector.ResourceVersion{
		ID:            version.GetId(),
		Name:          version.GetName(),
		Namespace:     version.GetNamespace(),
		Version:       version.GetVersion(),
		Author:        version.GetAuthor(),
		Description:   version.GetDescription(),
		Status:        version.GetStatus(),
		DownloadCount: version.GetDownloadCount(),
		CreateTime:    version.GetCreateTime(),
		ModifyTime:    version.GetModifyTime(),
	}
	if storage := version.GetStorage(); storage != nil {
		result.Storage = &skillconnector.StorageInfo{
			Provider:      storage.GetProvider(),
			Files:         append([]string(nil), storage.GetFiles()...),
			Scope:         storage.GetScope(),
			ContentDigest: storage.GetContentDigest(),
		}
	}
	return result
}

func cloneStringMap(value map[string]string) map[string]string {
	if value == nil {
		return nil
	}
	result := make(map[string]string, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}
