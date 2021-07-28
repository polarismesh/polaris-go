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

package common

import (
	"context"
	namingpb "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
	"github.com/polarismesh/polaris-go/pkg/network"
	"time"
)

//服务发现客户端接口
type DiscoverClient interface {
	//发送服务发现请求
	Send(*namingpb.DiscoverRequest) error
	//接收服务发现应答
	Recv() (*namingpb.DiscoverResponse, error)
	//发送EOF
	CloseSend() error
}

//创建client的函数
type DiscoverClientCreator func(
	reqId string, connection *network.Connection, timeout time.Duration) (DiscoverClient, context.CancelFunc, error)
