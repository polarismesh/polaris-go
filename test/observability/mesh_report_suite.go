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

package observability

import (
	"time"

	"github.com/golang/protobuf/ptypes/wrappers"
	"gopkg.in/check.v1"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
	namingpb "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
	monitorpb "github.com/polarismesh/polaris-go/plugin/statreporter/pb/v1"
)

const (
	meshNamespace = "meshNs"
	meshType      = "meshType"
	meshID        = "meshID"
	meshName1     = "meshRes1"
	meshName2     = "meshRes2"
)

func copyMeshConfig(orig *namingpb.MeshConfig) *namingpb.MeshConfig {
	res := &namingpb.MeshConfig{
		Resources: nil,
		Revision:  orig.Revision,
	}
	for _, r := range orig.GetResources() {
		res.Resources = append(res.Resources, &namingpb.MeshResource{
			Id:            r.Id,
			MeshNamespace: r.MeshNamespace,
			Name:          r.Name,
			MeshId:        r.MeshId,
			TypeUrl:       r.TypeUrl,
			Revision:      r.Revision,
			MeshToken:     r.MeshToken,
			Ctime:         r.Ctime,
			Mtime:         r.Mtime,
			Body:          r.Body,
		})
		res.MeshId = r.MeshId
	}
	return res
}

func (m *MonitorReportSuite) TestMeshConfigReport(c *check.C) {
	cfg, err := getCacheInfoConfiguration()
	c.Assert(err, check.IsNil)
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()
	meshRequest := api.GetMeshConfigRequest{}
	meshRequest.Namespace = meshNamespace
	meshRequest.MeshType = meshType
	meshRequest.MeshId = meshID
	firstRevision := time.Now().String()
	meshConfig := &namingpb.MeshConfig{
		Resources: nil,
		Revision:  &wrappers.StringValue{Value: firstRevision},
		MeshId:    &wrappers.StringValue{Value: meshID},
	}
	meshConfig.Resources = append(meshConfig.Resources, &namingpb.MeshResource{
		Id:            &wrappers.StringValue{Value: "firstResource"},
		MeshNamespace: &wrappers.StringValue{Value: meshNamespace},
		Name:          &wrappers.StringValue{Value: meshName1},
		MeshId:        &wrappers.StringValue{Value: meshID},
		TypeUrl:       &wrappers.StringValue{Value: meshType},
		Revision:      &wrappers.StringValue{Value: firstRevision},
		Body:          &wrappers.StringValue{Value: "body of first"},
	})
	meshService := &namingpb.Service{
		Name:      &wrappers.StringValue{Value: model.MeshPrefix + meshID + meshType},
		Namespace: &wrappers.StringValue{Value: meshNamespace},
	}
	// 初始只有一个resource
	m.mockServer.RegisterMeshConfig(meshService, meshType, copyMeshConfig(meshConfig))
	_, err = consumer.GetMeshConfig(&meshRequest)
	c.Assert(err, check.IsNil)
	time.Sleep(5 * time.Second)
	secondRevision := time.Now().String()
	// 添加第二个resource
	meshConfig.Resources = append(meshConfig.Resources, &namingpb.MeshResource{
		Id:            &wrappers.StringValue{Value: "secondResource"},
		MeshNamespace: &wrappers.StringValue{Value: meshNamespace},
		Name:          &wrappers.StringValue{Value: meshName2},
		MeshId:        &wrappers.StringValue{Value: meshID},
		TypeUrl:       &wrappers.StringValue{Value: meshType},
		Revision:      &wrappers.StringValue{Value: secondRevision},
		Body:          &wrappers.StringValue{Value: "body of second"},
	})
	meshConfig.Revision = &wrappers.StringValue{Value: secondRevision}
	m.mockServer.RegisterMeshConfig(meshService, meshType, copyMeshConfig(meshConfig))
	time.Sleep(5 * time.Second)
	thirdRevision := time.Now().String()
	// 修改了第一个资源的内容
	meshConfig.Resources[0].Body = &wrappers.StringValue{Value: "body of first modified"}
	meshConfig.Resources[0].Revision = &wrappers.StringValue{Value: thirdRevision}
	meshConfig.Revision = &wrappers.StringValue{Value: thirdRevision}
	m.mockServer.RegisterMeshConfig(meshService, meshType, copyMeshConfig(meshConfig))
	time.Sleep(5 * time.Second)
	forthRevision := time.Now().String()
	// 删除第一个资源
	meshConfig.Resources = meshConfig.Resources[1:]
	meshConfig.Revision = &wrappers.StringValue{Value: forthRevision}
	m.mockServer.RegisterMeshConfig(meshService, meshType, copyMeshConfig(meshConfig))
	time.Sleep(5 * time.Second)
	// 注销这个网格配置
	m.mockServer.DeRegisterMeshConfig(meshService, meshConfig.MeshId.GetValue(), meshType)
	// 等待上传
	time.Sleep(12 * time.Second)
}

func checkRevisions(expect, actual []string, c *check.C) {
	c.Assert(len(expect), check.Equals, len(actual))
	for idx, str := range expect {
		c.Assert(str, check.Equals, actual[idx])
	}
}
