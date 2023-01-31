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

package remoteservice

import (
	"context"

	"google.golang.org/grpc"

	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/plugin/location/remoteservice/proto"
)

const (
	locationProviderName string = "remoteService"
)

func New(ctx *plugin.InitContext) (*LocationProviderImpl, error) {
	impl := &LocationProviderImpl{}
	return impl, impl.Init(ctx)
}

// LocationProviderImpl 通过gRPC服务获取
type LocationProviderImpl struct {
	address  string
	clientIp string
}

// Init 初始化插件
func (p *LocationProviderImpl) Init(ctx *plugin.InitContext) error {
	log.GetBaseLogger().Infof("start remoteService location provider")

	p.clientIp = ctx.Config.GetGlobal().GetAPI().GetBindIP()
	provider := ctx.Config.GetGlobal().GetLocation().GetProvider(locationProviderName)
	p.address, _ = provider.GetOptions()["target"].(string)
	return nil
}

// Name 插件名称
func (p *LocationProviderImpl) Name() string {
	return locationProviderName
}

// GetLocation 获取地理位置信息
func (p *LocationProviderImpl) GetLocation() (*model.Location, error) {
	coon, err := grpc.Dial(p.address)
	if err != nil {
		log.GetBaseLogger().Errorf("grpc connect error %+v", err)
		return nil, err
	}
	c := proto.NewLocationClient(coon)

	req := &proto.LocationRequest{ClientIp: p.clientIp}
	rsp, err := c.GetLocation(context.Background(), req)
	if err != nil {
		log.GetBaseLogger().Errorf("Get Location Error %+v", err)
		return nil, err
	}

	return convertToLocation(rsp), nil
}

func convertToLocation(rsp *proto.LocationResponse) *model.Location {
	return &model.Location{
		Region: rsp.Region,
		Zone:   rsp.Zone,
		Campus: rsp.Campus,
	}
}
