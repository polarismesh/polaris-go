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

package aes

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_aesCryptor_GenerateKey(t *testing.T) {
	tests := []struct {
		name   string
		keyLen int
		err    error
	}{
		{
			name:   "genrate aes key",
			keyLen: 16,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &aesCryptor{}
			got, err := c.GenerateKey()
			assert.Nil(t, err)
			assert.Equal(t, tt.keyLen, len(got))
		})
	}
}

func Test_aesCryptor_Encrypt(t *testing.T) {
	type args struct {
		plaintext string
	}
	tests := []struct {
		name string
		args args
		err  error
	}{
		{
			name: "encrypt",
			args: args{
				plaintext: "1234abcd!@#$",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &aesCryptor{}
			key, err := c.GenerateKey()
			assert.Nil(t, err)
			ciphertext, err := c.Encrypt(tt.args.plaintext, key)
			assert.Nil(t, err)
			plaintext, err := c.Decrypt(ciphertext, key)
			assert.Nil(t, err)
			assert.Equal(t, plaintext, tt.args.plaintext)
		})
	}
}
