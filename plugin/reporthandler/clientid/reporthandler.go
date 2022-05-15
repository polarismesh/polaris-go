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

package clientid

import (
	"github.com/google/uuid"

	"github.com/polarismesh/polaris-go/pkg/model"
	namingpb "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/reporthandler"
)

var (
	ReportHandlerForInjectClientID = "clientIdInjectReport"

	_ reporthandler.ReportHandler = (*ReportHandler)(nil)
)

// init 注册插件
func init() {
	plugin.RegisterPlugin(&ReportHandler{})
}

// ReportHandler 处于ReportClient请求的前后置处理器
// 该插件主要用于注入 ClientID 信息
type ReportHandler struct {
	clientID string
	*plugin.PluginBase
}

// Type 插件类型
func (h *ReportHandler) Type() common.Type {
	return common.TypeReportHandler
}

// Name 插件名，一个类型下插件名唯一
func (h *ReportHandler) Name() string {
	return ReportHandlerForInjectClientID
}

// Init 初始化插件
func (h *ReportHandler) Init(ctx *plugin.InitContext) error {
	h.PluginBase = plugin.NewPluginBase(ctx)
	h.clientID = uuid.NewString()
	return nil
}

// Destroy 销毁插件，可用于释放资源
func (h *ReportHandler) Destroy() error {
	return nil
}

// InitLocal 初始化本地插件
func (h *ReportHandler) InitLocal(_ *namingpb.Client) {

}

// HandleRequest Handling Request body for Report
func (h *ReportHandler) HandleRequest(req *model.ReportClientRequest) {
	req.ID = h.clientID
}

// HandleResponse Handling Report Responsive Body
func (h *ReportHandler) HandleResponse(resp *model.ReportClientResponse, err error) {
}
