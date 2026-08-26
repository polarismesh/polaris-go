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
	"fmt"
	"sort"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/configconnector"
)

// TestConfigFileFlow_GetWatchedConfigFileMetadata 验证从 configFilePool 收集监听文件元数据，
// 包含 namespace/group/file_name/version/md5 五项，供 ReportClient 上报 config_metadata。
func TestConfigFileFlow_GetWatchedConfigFileMetadata(t *testing.T) {
	ref1 := &atomic.Value{}
	ref1.Store(&configconnector.ConfigFile{
		Namespace: "default", FileGroup: "g1", FileName: "f1", Version: 3, Md5: "md5_1",
	})
	ref2 := &atomic.Value{}
	ref2.Store(&configconnector.ConfigFile{
		Namespace: "default", FileGroup: "g2", FileName: "f2", Version: 5, Md5: "md5_2",
	})
	flow := &ConfigFileFlow{
		configFilePool: map[string]*ConfigFileRepo{
			"k1": {
				configFileMetadata: &model.DefaultConfigFileMetadata{
					Namespace: "default", FileGroup: "g1", FileName: "f1",
				},
				remoteConfigFileRef: ref1,
			},
			"k2": {
				configFileMetadata: &model.DefaultConfigFileMetadata{
					Namespace: "default", FileGroup: "g2", FileName: "f2",
				},
				remoteConfigFileRef: ref2,
			},
		},
		notifiedVersion: map[string]uint64{"k1": 3, "k2": 5},
	}

	items := flow.GetWatchedConfigFileMetadata()
	assert.Equal(t, 2, len(items))

	byKey := make(map[string]ConfigFileMetadataItem, len(items))
	for _, it := range items {
		byKey[it.Group+"/"+it.FileName] = it
	}
	it1 := byKey["g1/f1"]
	assert.Equal(t, "default", it1.Namespace)
	assert.Equal(t, "g1", it1.Group)
	assert.Equal(t, "f1", it1.FileName)
	assert.Equal(t, uint64(3), it1.Version)
	assert.Equal(t, "md5_1", it1.Md5)
	it2 := byKey["g2/f2"]
	assert.Equal(t, "md5_2", it2.Md5)
	assert.Equal(t, uint64(5), it2.Version)
}

// TestConfigFileFlow_GetWatchedConfigFileMetadata_EmptyRemoteFile 验证 repo 尚未拉取到远端配置文件时
// loadRemoteFile 返回 nil，md5 应留空而不 panic。
func TestConfigFileFlow_GetWatchedConfigFileMetadata_EmptyRemoteFile(t *testing.T) {
	flow := &ConfigFileFlow{
		configFilePool: map[string]*ConfigFileRepo{
			"k1": {
				configFileMetadata: &model.DefaultConfigFileMetadata{
					Namespace: "default", FileGroup: "g1", FileName: "f1",
				},
				remoteConfigFileRef: &atomic.Value{}, // 未 Store，loadRemoteFile 返回 nil
			},
		},
		notifiedVersion: map[string]uint64{"k1": 0},
	}
	items := flow.GetWatchedConfigFileMetadata()
	assert.Equal(t, 1, len(items))
	assert.Equal(t, "", items[0].Md5)
	assert.Equal(t, "default", items[0].Namespace)
	assert.Equal(t, "f1", items[0].FileName)
}

// TestConfigFileFlow_GetWatchedConfigFileContent_Encrypted 验证加密配置的内容查询项：
// 从同一次 loadRemoteFile 快照填充 Encrypted/EncryptAlgo/DataKey（取自 internal-encryptalgo /
// internal-datakey tag），与 version/md5/content 自一致；非加密配置三个字段保持零值。
func TestConfigFileFlow_GetWatchedConfigFileContent_Encrypted(t *testing.T) {
	encRef := &atomic.Value{}
	encRef.Store(&configconnector.ConfigFile{
		Namespace: "default", FileGroup: "g1", FileName: "aes.yaml",
		Version: 3, Md5: "md5_cipher", SourceContent: "Y2lwaGVyLWNvbnRlbnQ=", Encrypted: true,
		Tags: []*configconnector.ConfigFileTag{
			{Key: configconnector.ConfigFileTagKeyEncryptAlgo, Value: "AES"},
			{Key: configconnector.ConfigFileTagKeyUseEncrypted, Value: "true"},
			{Key: configconnector.ConfigFileTagKeyDataKey, Value: "UTEyMzQ1Njc4OTAxMjM0NQ=="},
		},
	})
	plainRef := &atomic.Value{}
	plainRef.Store(&configconnector.ConfigFile{
		Namespace: "default", FileGroup: "g1", FileName: "plain.yaml",
		Version: 1, Md5: "md5_plain", SourceContent: "plain-body",
	})
	flow := &ConfigFileFlow{
		configFilePool: map[string]*ConfigFileRepo{
			"default+g1+aes.yaml": {
				configFileMetadata: &model.DefaultConfigFileMetadata{
					Namespace: "default", FileGroup: "g1", FileName: "aes.yaml",
				},
				remoteConfigFileRef: encRef,
			},
			"default+g1+plain.yaml": {
				configFileMetadata: &model.DefaultConfigFileMetadata{
					Namespace: "default", FileGroup: "g1", FileName: "plain.yaml",
				},
				remoteConfigFileRef: plainRef,
			},
		},
		notifiedVersion: map[string]uint64{},
	}

	enc, ok := flow.GetWatchedConfigFileContent("default", "g1", "aes.yaml")
	assert.True(t, ok)
	assert.True(t, enc.Pulled)
	assert.True(t, enc.Encrypted, "加密配置应标记 Encrypted")
	assert.Equal(t, "AES", enc.EncryptAlgo, "应从 internal-encryptalgo tag 取加密算法")
	assert.Equal(t, "UTEyMzQ1Njc4OTAxMjM0NQ==", enc.DataKey, "应从 internal-datakey tag 取 base64 明文数据密钥")
	assert.Equal(t, "Y2lwaGVyLWNvbnRlbnQ=", enc.Content, "content 仍为源内容（密文）")
	assert.Equal(t, uint64(3), enc.Version)
	assert.Equal(t, "md5_cipher", enc.Md5)

	plain, ok := flow.GetWatchedConfigFileContent("default", "g1", "plain.yaml")
	assert.True(t, ok)
	assert.True(t, plain.Pulled)
	assert.False(t, plain.Encrypted, "非加密配置不应标记 Encrypted")
	assert.Empty(t, plain.EncryptAlgo)
	assert.Empty(t, plain.DataKey)
}

// TestConfigFileFlow_GetWatchedConfigFileContent_EncryptedMissingTags 边角：Encrypted=true 但
// 加密 tag 缺失时，EncryptAlgo/DataKey 留空（omitempty 省略），不影响 version/md5/content 主字段。
func TestConfigFileFlow_GetWatchedConfigFileContent_EncryptedMissingTags(t *testing.T) {
	ref := &atomic.Value{}
	ref.Store(&configconnector.ConfigFile{
		Namespace: "default", FileGroup: "g1", FileName: "aes.yaml",
		Version: 3, Md5: "md5_cipher", SourceContent: "cipher", Encrypted: true,
	})
	flow := &ConfigFileFlow{
		configFilePool: map[string]*ConfigFileRepo{
			"default+g1+aes.yaml": {
				configFileMetadata: &model.DefaultConfigFileMetadata{
					Namespace: "default", FileGroup: "g1", FileName: "aes.yaml",
				},
				remoteConfigFileRef: ref,
			},
		},
		notifiedVersion: map[string]uint64{},
	}
	item, ok := flow.GetWatchedConfigFileContent("default", "g1", "aes.yaml")
	assert.True(t, ok)
	assert.True(t, item.Encrypted)
	assert.Empty(t, item.EncryptAlgo, "tag 缺失时算法留空，由接收方按缺省处理")
	assert.Empty(t, item.DataKey)
}

// TestConfigFileFlow_GetWatchedConfigFileMetadata_EmptyPool 验证空池返回非 nil 空切片。
func TestConfigFileFlow_GetWatchedConfigFileMetadata_EmptyPool(t *testing.T) {
	flow := &ConfigFileFlow{
		configFilePool:  map[string]*ConfigFileRepo{},
		notifiedVersion: map[string]uint64{},
	}
	items := flow.GetWatchedConfigFileMetadata()
	assert.NotNil(t, items)
	assert.Equal(t, 0, len(items))
}

// TestConfigFileFlow_GetWatchedConfigFileMetadata_DeterministicOrder 回归「map 遍历序随机导致
// config_metadata 每次序列化顺序不同、误判订阅变化而反复落盘」的问题：
// 多次调用返回的列表必须按 (namespace, group, file_name) 排序且各次完全一致。
func TestConfigFileFlow_GetWatchedConfigFileMetadata_DeterministicOrder(t *testing.T) {
	// 构造多个监听文件，命名刻意乱序，放大 map 遍历的随机性
	pool := map[string]*ConfigFileRepo{}
	notified := map[string]uint64{}
	files := []struct{ ns, group, name string }{
		{"default", "g2", "f10"}, {"default", "g1", "f2"}, {"default", "g1", "f1"},
		{"default", "g1", "f20"}, {"default", "g3", "f1"}, {"default", "g1", "f3"},
	}
	for i, f := range files {
		key := fmt.Sprintf("k%d", i)
		ref := &atomic.Value{}
		ref.Store(&configconnector.ConfigFile{
			Namespace: f.ns, FileGroup: f.group, FileName: f.name, Version: uint64(i + 1), Md5: "md5",
		})
		pool[key] = &ConfigFileRepo{
			configFileMetadata:  &model.DefaultConfigFileMetadata{Namespace: f.ns, FileGroup: f.group, FileName: f.name},
			remoteConfigFileRef: ref,
		}
		notified[key] = uint64(i + 1)
	}
	flow := &ConfigFileFlow{configFilePool: pool, notifiedVersion: notified}

	// 多次调用，断言每次都有序且彼此完全一致
	var prev []ConfigFileMetadataItem
	for round := 0; round < 30; round++ {
		items := flow.GetWatchedConfigFileMetadata()
		assert.Equal(t, len(files), len(items))
		sorted := sort.SliceIsSorted(items, func(i, j int) bool {
			if items[i].Namespace != items[j].Namespace {
				return items[i].Namespace < items[j].Namespace
			}
			if items[i].Group != items[j].Group {
				return items[i].Group < items[j].Group
			}
			return items[i].FileName < items[j].FileName
		})
		assert.True(t, sorted, "第 %d 次调用返回应有序", round)
		if prev != nil {
			assert.Equal(t, prev, items, "第 %d 次调用顺序应与首次一致", round)
		}
		prev = items
	}
}
