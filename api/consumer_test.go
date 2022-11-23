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

package api

import (
	"testing"

	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/stretchr/testify/assert"
)

func TestGetInstancesRequest_convert(t *testing.T) {
	type fields struct {
		GetInstancesRequest model.GetInstancesRequest
	}
	tests := []struct {
		name   string
		fields fields
		ret    map[string]string
	}{
		{
			fields: fields{
				GetInstancesRequest: model.GetInstancesRequest{
					SourceService: &model.ServiceInfo{
						Metadata: map[string]string{
							"uid": "123",
						},
					},
					Arguments: []model.Argument{
						model.BuildHeaderArgument("uid", "123"),
					},
				},
			},
			ret: map[string]string{
				"uid":         "123",
				"$header.uid": "123",
			},
		},
		{
			fields: fields{
				GetInstancesRequest: model.GetInstancesRequest{
					Arguments: []model.Argument{
						model.BuildHeaderArgument("uid", "123"),
					},
				},
			},
			ret: map[string]string{
				"$header.uid": "123",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &GetInstancesRequest{
				GetInstancesRequest: tt.fields.GetInstancesRequest,
			}
			r.convert()
			assert.Equal(t, tt.ret, r.SourceService.Metadata)
		})
	}
}
