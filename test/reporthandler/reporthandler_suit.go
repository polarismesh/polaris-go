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
	"log"
	"os"

	"gopkg.in/check.v1"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/flow/data"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/test/util"
)

// ReporthandlerTestingSuite is a test suite for testing the report handler
type ReporthandlerTestingSuite struct {
}

// GetName 套件名字
func (t *ReporthandlerTestingSuite) GetName() string {
	return "Reporthandler"
}

// SetUpSuite 启动测试套程序
func (t *ReporthandlerTestingSuite) SetUpSuite(c *check.C) {

}

// TestHandlerClientReport 测试HandlerClientReport
func (t *ReporthandlerTestingSuite) TestHandlerClientReport(c *check.C) {
	log.Printf("Start TestHandlerClientReport")

	envKeyRegion := "POLARIS_INSTANCE_REGION"
	envKeyZone := "POLARIS_INSTANCE_ZONE"
	envKeyCampus := "POLARIS_INSTANCE_CAMPUS"

	if err := os.Setenv(envKeyRegion, envKeyRegion); err != nil {
		c.Fatal(err)
	}
	if err := os.Setenv(envKeyZone, envKeyZone); err != nil {
		c.Fatal(err)
	}
	if err := os.Setenv(envKeyCampus, envKeyCampus); err != nil {
		c.Fatal(err)
	}

	cfg, err := config.LoadConfigurationByFile("testdata/consumer.yaml")
	c.Assert(err, check.IsNil)
	sdkCtx, err := api.InitContextByConfig(cfg)
	c.Assert(err, check.IsNil)

	reportChain, err := data.GetReportChain(nil, sdkCtx.GetPlugins())
	c.Assert(err, check.IsNil)
	c.Assert(reportChain, check.NotNil)

	// 总共有3个 ReportHandler 拦截器
	c.Assert(len(reportChain.Chain), check.Equals, 3)

	req := &model.ReportClientRequest{}
	resp := &model.ReportClientResponse{}

	for i := range reportChain.Chain {
		handler := reportChain.Chain[i]
		handler.HandleRequest(req)
		handler.HandleResponse(resp, nil)
	}

	c.Assert(resp.Region, check.Equals, envKeyRegion)
	c.Assert(resp.Zone, check.Equals, envKeyZone)
	c.Assert(resp.Campus, check.Equals, envKeyCampus)
}

// TearDownSuite 结束测试套程序
func (t *ReporthandlerTestingSuite) TearDownSuite(c *check.C) {
	util.InsertLog(t, c.GetTestLog())
}
