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

package configconnector

import (
	"reflect"
	"strings"
	"testing"
)

// TestMaskTags 验证 maskTags 在加密/非加密场景下对敏感 tag 的掩码行为。
func TestMaskTags(t *testing.T) {
	// 使用一个真实形态的明文数据密钥 Base64，确保断言能捕获到"明文密钥泄露"
	plainDataKey := "Q61w5uXxxXsJa9xkeYt4DA=="

	tests := []struct {
		name      string
		tags      []*ConfigFileTag
		encrypted bool
		want      []*ConfigFileTag
	}{
		{
			name: "非加密场景原样返回含明文密钥的tags",
			tags: []*ConfigFileTag{
				{Key: ConfigFileTagKeyDataKey, Value: plainDataKey},
				{Key: ConfigFileTagKeyEncryptAlgo, Value: "AES"},
			},
			encrypted: false,
			want: []*ConfigFileTag{
				{Key: ConfigFileTagKeyDataKey, Value: plainDataKey},
				{Key: ConfigFileTagKeyEncryptAlgo, Value: "AES"},
			},
		},
		{
			name: "加密场景掩码datakey与encryptalgo保留其余",
			tags: []*ConfigFileTag{
				{Key: ConfigFileTagKeyEncryptAlgo, Value: "AES"},
				{Key: ConfigFileTagKeyUseEncrypted, Value: "true"},
				{Key: ConfigFileTagKeyDataKey, Value: plainDataKey},
				{Key: "env", Value: "prod"},
			},
			encrypted: true,
			want: []*ConfigFileTag{
				{Key: ConfigFileTagKeyEncryptAlgo, Value: maskedTagValue},
				{Key: ConfigFileTagKeyUseEncrypted, Value: "true"},
				{Key: ConfigFileTagKeyDataKey, Value: maskedTagValue},
				{Key: "env", Value: "prod"},
			},
		},
		{
			name:      "加密场景nil tags返回空切片",
			tags:      nil,
			encrypted: true,
			want:      []*ConfigFileTag{},
		},
		{
			name:      "非加密场景nil tags返回nil",
			tags:      nil,
			encrypted: false,
			want:      nil,
		},
		{
			name:      "加密场景包含nil元素不panic且原样保留",
			tags:      []*ConfigFileTag{nil, {Key: ConfigFileTagKeyDataKey, Value: plainDataKey}},
			encrypted: true,
			want:      []*ConfigFileTag{nil, {Key: ConfigFileTagKeyDataKey, Value: maskedTagValue}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := maskTags(tt.tags, tt.encrypted)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("maskTags() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

// TestMaskTags_NotMutateInput 验证 maskTags 加密场景不修改入参元素，
// 避免污染缓存/上游共享对象。
func TestMaskTags_NotMutateInput(t *testing.T) {
	plainDataKey := "Q61w5uXxxXsJa9xkeYt4DA=="
	dataKeyTag := &ConfigFileTag{Key: ConfigFileTagKeyDataKey, Value: plainDataKey}
	tags := []*ConfigFileTag{dataKeyTag}

	_ = maskTags(tags, true)

	if dataKeyTag.Value != plainDataKey {
		t.Errorf("maskTags 修改了入参 tag 值: got %q, want %q", dataKeyTag.Value, plainDataKey)
	}
}

// TestConfigFile_String 验证 String() 在加密场景不泄露明文数据密钥，非加密场景原样输出。
func TestConfigFile_String(t *testing.T) {
	plainDataKey := "Q61w5uXxxXsJa9xkeYt4DA=="

	t.Run("加密场景datakey被掩码不含明文", func(t *testing.T) {
		cf := &ConfigFile{
			Namespace: "default",
			FileGroup: "polaris-config-example",
			FileName:  "aes.yaml",
			Version:   2,
			Encrypted: true,
			Tags: []*ConfigFileTag{
				{Key: ConfigFileTagKeyEncryptAlgo, Value: "AES"},
				{Key: ConfigFileTagKeyUseEncrypted, Value: "true"},
				{Key: ConfigFileTagKeyDataKey, Value: plainDataKey},
			},
		}
		s := cf.String()
		if strings.Contains(s, plainDataKey) {
			t.Errorf("String() 泄露了明文数据密钥: %s", s)
		}
		if !strings.Contains(s, maskedTagValue) {
			t.Errorf("String() 未对敏感 tag 掩码: %s", s)
		}
		if !strings.Contains(s, "encrypt=true") {
			t.Errorf("String() 缺少 encrypt 标志: %s", s)
		}
	})

	t.Run("非加密场景tag原样输出", func(t *testing.T) {
		cf := &ConfigFile{
			Namespace: "default",
			FileGroup: "polaris-config-example",
			FileName:  "example.yaml",
			Version:   2,
			Encrypted: false,
			Tags: []*ConfigFileTag{
				{Key: "env", Value: "prod"},
			},
		}
		s := cf.String()
		if !strings.Contains(s, "prod") {
			t.Errorf("非加密场景 tag 值应原样输出: %s", s)
		}
		if strings.Contains(s, maskedTagValue) {
			t.Errorf("非加密场景不应出现掩码: %s", s)
		}
	})

	t.Run("加密场景nil_tags输出空数组而非null", func(t *testing.T) {
		// 加密 + tags==nil 时 maskTags 返回长度为 0 的非 nil 切片，
		// 故 String() 的 tags 段为 "[]"（区别于非加密 nil 场景的 "null"），此差异是预期行为。
		cf := &ConfigFile{
			Namespace: "default",
			FileGroup: "polaris-config-example",
			FileName:  "aes.yaml",
			Version:   1,
			Encrypted: true,
			Tags:      nil,
		}
		s := cf.String()
		if !strings.Contains(s, "tags=[]") {
			t.Errorf("加密场景 nil tags 应输出 tags=[]: %s", s)
		}
	})
}
