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

package flow

import (
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/authenticator"
)

// SyncAuthenticate 同步执行服务鉴权流程。按 provider.auth.chain 顺序依次调用各鉴权插件，
// 任一插件返回 Forbidden 即短路返回 AuthResultForbidden；全部通过则返回 AuthResultOk。
// 当鉴权未启用或插件链为空时，直接返回 AuthResultOk（零开销）。
func (e *Engine) SyncAuthenticate(req *model.AuthenticateRequest) (*model.AuthenticateResponse, error) {
	if len(e.authenticators) == 0 {
		return &model.AuthenticateResponse{Code: model.AuthResultOk}, nil
	}
	info := buildAuthInfo(req)
	for _, auth := range e.authenticators {
		result := auth.Authenticate(info)
		if result == nil {
			continue
		}
		if result.Code == authenticator.AuthResultForbidden {
			return &model.AuthenticateResponse{
				Code: model.AuthResultForbidden,
				Info: result.Info,
			}, nil
		}
	}
	return &model.AuthenticateResponse{Code: model.AuthResultOk}, nil
}

// buildAuthInfo 将外部 AuthenticateRequest 转换为插件层 AuthInfo
func buildAuthInfo(req *model.AuthenticateRequest) *authenticator.AuthInfo {
	info := &authenticator.AuthInfo{
		Namespace:     req.Namespace,
		Service:       req.Service,
		Method:        req.Method,
		Path:          req.Path,
		Protocol:      req.Protocol,
		SourceService: req.SourceService,
	}
	if len(req.Arguments) > 0 {
		info.Arguments = make([]model.Argument, len(req.Arguments))
		copy(info.Arguments, req.Arguments)
	}
	return info
}
