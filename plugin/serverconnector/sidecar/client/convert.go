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
package client

import (
	"bytes"
	"fmt"

	"github.com/golang/protobuf/proto"
	"github.com/golang/protobuf/ptypes/wrappers"

	"github.com/polarismesh/polaris-go/pkg/model"
	namingpb "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
	"github.com/polarismesh/polaris-go/plugin/serverconnector/sidecar/dns"
)

// 创建dns msg
func newDefaultDnsMsg(id uint16) *dns.Msg {
	dnsMsg := dns.Msg{
		MsgHdr: dns.MsgHdr{
			Id:                 id,
			Response:           false,
			Opcode:             0,
			Authoritative:      false,
			Truncated:          false,
			RecursionDesired:   false,
			RecursionAvailable: false,
			Zero:               false,
			AuthenticatedData:  false,
			CheckingDisabled:   false,
			Rcode:              0,
			Qdcount:            0,
			Ancount:            0,
			Nscount:            0,
			Arcount:            0,
		},
		Question: nil,
		Answer:   nil,
		Ns:       nil,
		Extra:    nil,
	}
	return &dnsMsg
}

// DiscoverRequest -->  dns msg
func convertDiscoverRequestToDnsMsg(request *namingpb.DiscoverRequest, id uint16) (*dns.Msg, error) {
	if request == nil {
		return nil, model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil, "invalid namingpb.DiscoverRequest")
	}
	dnsReqMsg := dns.Msg{
		MsgHdr: dns.MsgHdr{
			Id:                 id,
			Response:           false,
			Opcode:             dns.OpCodePolarisGetResource,
			Authoritative:      false,
			Truncated:          false,
			RecursionDesired:   false,
			RecursionAvailable: false,
			Zero:               false,
			AuthenticatedData:  false,
			CheckingDisabled:   false,
			Rcode:              0,
			Qdcount:            1,
			Ancount:            0,
			Nscount:            0,
			Arcount:            0,
		},
	}
	question := dns.PolarisGetResourceQuestion{
		BasePolarisQuestion: dns.BasePolarisQuestion{},
		Req:                 request,
	}
	dnsReqMsg.Question = append(dnsReqMsg.Question, &question)
	return &dnsReqMsg, nil
}

// RspData ---> DiscoverResponse
func convertRspDataToDiscoverResponse(rspData *RspData) (*namingpb.DiscoverResponse, error) {
	rsp := namingpb.DiscoverResponse{}
	if rspData.RCode != 0 {
		if rspData.DetailErrInfo != nil {
			rsp.Code = &wrappers.UInt32Value{Value: rspData.DetailErrInfo.ErrInfo.GetErrCode()}
			rsp.Info = &wrappers.StringValue{Value: rspData.DetailErrInfo.ErrInfo.GetErrMsg()}
			return &rsp, model.NewSDKError(model.ErrCodeInvalidResponse, nil, fmt.Sprintf("code:%d err:%s",
				rspData.DetailErrInfo.ErrInfo.GetErrCode(), rspData.DetailErrInfo.ErrInfo.GetErrMsg()))
		}
		return &rsp, model.NewSDKError(model.ErrCodeInvalidResponse, nil, fmt.Sprintf(
			"convertRspDatToDiscoverResponse error rspData Rcode:%d", rspData.RCode))
	}
	data := bytes.Buffer{}
	for _, v := range rspData.RRArr {
		_, _ = data.Write(v.GetData())
	}
	err := proto.Unmarshal(data.Bytes(), &rsp)
	if err != nil {
		return nil, model.NewSDKError(model.ErrCodeInvalidResponse, nil, err.Error())
	}
	return &rsp, nil
}

// RspData ---> ReportClientResponse
func convertRspDataToReportClientResp(rspData *RspData) (*model.ReportClientResponse, error) {
	if rspData.RCode != 0 {
		if rspData.DetailErrInfo != nil {
			return nil, model.NewSDKError(model.ErrCodeInvalidResponse, nil, fmt.Sprintf("code:%d err:%s",
				rspData.DetailErrInfo.ErrInfo.GetErrCode(), rspData.DetailErrInfo.ErrInfo.GetErrMsg()))
		}
		return nil, model.NewSDKError(model.ErrCodeInvalidResponse, nil, fmt.Sprintf(
			"convertRspDataToReportClientResponse error rspData Rcode:%d", rspData.RCode))
	}

	rr := rspData.RRArr[0]
	clientRR := rr.(*dns.LocationRR)
	reportRsp := new(model.ReportClientResponse)
	reportRsp.Region = clientRR.SideCar.GetLocation().GetRegion().GetValue()
	reportRsp.Zone = clientRR.SideCar.GetLocation().GetZone().GetValue()
	reportRsp.Campus = clientRR.SideCar.GetLocation().GetCampus().GetValue()
	reportRsp.Version = clientRR.SideCar.GetVersion().GetValue()
	return reportRsp, nil
}

// RspData --> InstanceRegisterResponse
func convertRspDataToRegisterResp(rspData *RspData) (*model.InstanceRegisterResponse, error) {
	if rspData.RCode != 0 {
		if rspData.DetailErrInfo != nil {
			return nil, model.NewSDKError(model.ErrCodeInvalidResponse, nil, fmt.Sprintf("code:%d err:%s",
				rspData.DetailErrInfo.ErrInfo.GetErrCode(), rspData.DetailErrInfo.ErrInfo.GetErrMsg()))
		}
		return nil, model.NewSDKError(model.ErrCodeInvalidResponse, nil, fmt.Sprintf(
			"convertRspDataToReportClientResponse error rspData Rcode:%d", rspData.RCode))
	}
	if len(rspData.RRArr) > 0 {
		rr := rspData.RRArr[0]
		instancRR := rr.(*dns.ResponseRR)

		registerRsp := new(model.InstanceRegisterResponse)
		registerRsp.InstanceID = instancRR.Response.GetInstance().GetId().GetValue()
		return registerRsp, nil
	}

	return nil, nil
}
