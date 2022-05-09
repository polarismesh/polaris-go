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

package statreporter

import (
	"github.com/polarismesh/polaris-go/pkg/flow/data"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	namingpb "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/reporthandler"
	"github.com/polarismesh/polaris-go/pkg/plugin/statreporter"
)

var (
	ReportHandlerForStatReporter = "statreporterReport"

	_ reporthandler.ReportHandler = (*ReportHandler)(nil)
)

// init 注册插件
func init() {
	plugin.RegisterPlugin(&ReportHandler{})
}

// ReportHandler 处于ReportClient请求的前后置处理器
type ReportHandler struct {
	first bool
	*plugin.PluginBase
	globalCtx     model.ValueContext
	reporterChain []statreporter.StatReporter
}

// Type 插件类型
func (h *ReportHandler) Type() common.Type {
	return common.TypeReportHandler
}

// Name 插件名，一个类型下插件名唯一
func (h *ReportHandler) Name() string {
	return ReportHandlerForStatReporter
}

// Init 初始化插件
func (h *ReportHandler) Init(ctx *plugin.InitContext) error {
	h.first = true
	h.PluginBase = plugin.NewPluginBase(ctx)
	h.globalCtx = ctx.ValueCtx
	reporterChain, err := data.GetStatReporterChain(ctx.Config, ctx.Plugins)
	if err != nil {
		return err
	}
	h.reporterChain = reporterChain
	return nil
}

// Destroy 销毁插件，可用于释放资源
func (h *ReportHandler) Destroy() error {
	return nil
}

func (h *ReportHandler) InitLocal(_ *namingpb.Client) {

}

// HandleRequest Handling Request body for Report
func (h *ReportHandler) HandleRequest(req *model.ReportClientRequest) {
	infos := make([]model.StatInfo, 0, len(h.reporterChain))

	// 收集当前的所有metric插件链的元信息
	for i := range h.reporterChain {
		stat := h.reporterChain[i].Info()
		if stat.Empty() {
			continue
		}
		infos = append(infos, stat)
	}

	if h.first {
		log.GetBaseLogger().Infof("[ReportHandler] report statreport metadata info : %+v", infos)
		h.first = false
	}

	req.StatInfos = infos
}

// HandleResponse Handling Report Responsive Body
func (h *ReportHandler) HandleResponse(resp *model.ReportClientResponse, err error) {
}
