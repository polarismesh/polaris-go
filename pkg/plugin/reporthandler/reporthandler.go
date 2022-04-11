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

package reporthandler

import (
	"github.com/polarismesh/polaris-go/pkg/model"
	namingpb "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
)

// ReportHandlerChain report handler chain
type ReportHandlerChain struct {
	Chain []ReportHandler
}

// ReportHandler Request body and responsive body during reportClient
type ReportHandler interface {
	plugin.Plugin

	InitLocal(*namingpb.Client)

	// HandleRequest Handling Request body for Report
	HandleRequest(req *model.ReportClientRequest)

	// HandleResponse Handling Report Responsive Body
	HandleResponse(resp *model.ReportClientResponse, err error)
}

// init Register ReportHandler plugin
func init() {
	plugin.RegisterPluginInterface(common.TypeReportHandler, new(ReportHandler))
}
