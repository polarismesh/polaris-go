/**
 * Tencent is pleased to support the open source community by making CL5 available.
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
package sidecar

import (
	namingpb "github.com/polarismesh/polaris-go/pkg/model/pb/v1"

	"github.com/polarismesh/polaris-go/plugin/serverconnector/sidecar/client"
	"github.com/polarismesh/polaris-go/plugin/serverconnector/sidecar/dns"
	sidecarPb "github.com/polarismesh/polaris-go/plugin/serverconnector/sidecar/model/pb"

	"github.com/golang/protobuf/proto"
	"github.com/golang/protobuf/ptypes/wrappers"
	"net"
	"time"
)

const (
	SideCarTestNamespace = "Test"
	SideCarTestServiceName = "angevil_test"

	SideCarTestInsPort = 12345
	SideCarTestFirstInsIp = "127.0.0.1"

	ShouldErrHost = "127.0.0.2"
	ShouldErrHostWithoutDetailErr = "127.0.0.3"

)

// sidecar UDP server mock
type MockSideCarConn struct {
	udpConn net.UDPConn
	reqMsg *dns.Msg
}

// 设置Conn
func GetSideCarMockConn() client.Conn  {
	conn := new(MockSideCarConn)
	return conn
}

// dail
func (mock *MockSideCarConn) Dial(dstIp string, port int) error  {
	return nil
}

// close
func (mock *MockSideCarConn) Close()  {
	return
}

// write msg
func (mock *MockSideCarConn) WriteMsg(msg *dns.Msg) error  {
	mock.reqMsg = msg
	return nil
}

// read msg
func (mock *MockSideCarConn) ReadMsg() (*dns.Msg, error)  {
	retMsg := dns.Msg{}
	if mock.reqMsg.Opcode == dns.OpcodeQuery {
		qustion := mock.reqMsg.Question[0].(*dns.DNSQuestion)
		name := qustion.Name
		retMsg.SetReply(mock.reqMsg)
		rr := dns.A{
			Hdr: dns.RR_Header{
				Name: name,
				Rrtype: dns.TypeA,
				Class: dns.ClassINET,
				Ttl: 5,
				Rdlength: 4,
			},
			A:   net.ParseIP("127.0.0.1"),
		}
		retMsg.Ancount += 1
		retMsg.Answer = append(retMsg.Answer, &rr)
		return &retMsg, nil
	} else if mock.reqMsg.Opcode == dns.OpCodePolarisRegisterInstance {
		mock.handleRegisterInstance(&retMsg)
		return &retMsg, nil
	} else if mock.reqMsg.Opcode == dns.OpCodePolarisDeregisterInstance {
		mock.handleDeregisterInstance(&retMsg)
		return &retMsg, nil
	} else if mock.reqMsg.Opcode == dns.OpCodePolarisHeartbeat {
		mock.handleHeartBeat(&retMsg)
		return &retMsg, nil
	} else if mock.reqMsg.Opcode == dns.OpCodePolarisGetResource {
		q := mock.reqMsg.Question[0].(*dns.PolarisGetResourceQuestion)
		if q.Req.Type == namingpb.DiscoverRequest_INSTANCE {
			mock.handleGetResourceInstances(&retMsg)
			return &retMsg, nil
		}
	} else if mock.reqMsg.Opcode == dns.OpCodePolarisReportClient {
		mock.handleReportClient(&retMsg)
		return &retMsg, nil
	}
	return nil, nil
}

// mock 处理注册
func (mock *MockSideCarConn) handleRegisterInstance(retMsg *dns.Msg)  {
	retMsg.SetReplyWithoutQuestions(mock.reqMsg)
	q := mock.reqMsg.Question[0].(*dns.PolarisInstanceQuestion)
	if q.Req.Host.GetValue() == ShouldErrHost {
		retMsg.Rcode = dns.RcodeServerFailure
		rr := dns.DetailErrInfoRR{
			Hdr:     dns.RR_Header{
				Name:     "DetailError",
				Rrtype:   dns.TypePolarisDetailErrInfo,
				Class:    dns.ClassINET,
				Ttl:      0,
				Rdlength: 0,
			},
			ErrInfo: &sidecarPb.DetailErrInfo{
				ErrCode: 1,
				ErrMsg:  "err msg",
			},
		}
		retMsg.Arcount = 1
		retMsg.Extra = append(retMsg.Extra, &rr)
	} else {
		retMsg.SetReplyWithoutQuestions(mock.reqMsg)
		rr := dns.ResponseRR{
			Hdr: dns.RR_Header{
				Name:     "ResponseRR",
				Rrtype:   dns.TypePolarisResponse,
				Class:    dns.ClassINET,
				Ttl:      0,
				Rdlength: 0,
			},
		}
		rr.Response = new(namingpb.Response)
		rr.Response.Code = &wrappers.UInt32Value{Value: 0}
		rr.Response.Instance = &namingpb.Instance{
			Id: &wrappers.StringValue{Value: "testInstanceId"},
		}
		retMsg.Ancount = 1
		retMsg.Answer = append(retMsg.Answer, &rr)
	}
}

// 反注册mock
func (mock *MockSideCarConn) handleDeregisterInstance(retMsg *dns.Msg)  {
	retMsg.SetReplyWithoutQuestions(mock.reqMsg)
	q := mock.reqMsg.Question[0].(*dns.PolarisInstanceQuestion)
	if q.Req.Host.GetValue() == ShouldErrHost {
		rr := dns.DetailErrInfoRR{
			Hdr:     dns.RR_Header{
				Name:     "DetailError",
				Rrtype:   dns.TypePolarisDetailErrInfo,
				Class:    dns.ClassINET,
				Ttl:      0,
				Rdlength: 0,
			},
			ErrInfo: &sidecarPb.DetailErrInfo{
				ErrCode: 1,
				ErrMsg:  "err msg",
			},
		}
		retMsg.Arcount = 1
		retMsg.Extra = append(retMsg.Extra, &rr)
	} else {
		rr := dns.ResponseRR{
			Hdr: dns.RR_Header{
				Name:     "ResponseRR",
				Rrtype:   dns.TypePolarisResponse,
				Class:    dns.ClassINET,
				Ttl:      0,
				Rdlength: 0,
			},
		}
		rr.Response = new(namingpb.Response)
		rr.Response.Code = &wrappers.UInt32Value{Value: 0}
		rr.Response.Instance = &namingpb.Instance{
			Id: &wrappers.StringValue{Value: "testInstanceId"},
		}
		retMsg.Ancount = 1
		retMsg.Answer = append(retMsg.Answer, &rr)
	}
}

//心跳mock
func (mock *MockSideCarConn) handleHeartBeat(retMsg *dns.Msg) {
	retMsg.SetReplyWithoutQuestions(mock.reqMsg)
	rr := dns.ResponseRR{
		Hdr: dns.RR_Header{
			Name:     "ResponseRR",
			Rrtype:   dns.TypePolarisResponse,
			Class:    dns.ClassINET,
			Ttl:      0,
			Rdlength: 0,
		},
	}
	rr.Response = new(namingpb.Response)
	rr.Response.Code = &wrappers.UInt32Value{Value: 0}
	rr.Response.Instance = &namingpb.Instance{
		Id: &wrappers.StringValue{Value: "testInstanceId"},
	}
	retMsg.Ancount = 1
	retMsg.Answer = append(retMsg.Answer, &rr)
}

// GetInstances mock
func (mock *MockSideCarConn) handleGetResourceInstances(retMsg *dns.Msg)  {
	retMsg.SetReplyWithoutQuestions(mock.reqMsg)
	rr := dns.StreamRR{
		Hdr: dns.RR_Header{
			Name:     "ResponseRR",
			Rrtype:   dns.TypePolarisStream,
			Class:    dns.ClassINET,
			Ttl:      0,
			Rdlength: 0,
		},
	}
	disRsp := new(namingpb.DiscoverResponse)
	disRsp.Type = namingpb.DiscoverResponse_INSTANCE
	ins := namingpb.Instance{
		Id:                   &wrappers.StringValue{Value: "testInsId1"},
		Service:              &wrappers.StringValue{Value: "angevil_test"},
		Namespace:            &wrappers.StringValue{Value: "Test"},
		Host:                 &wrappers.StringValue{Value: "127.0.0.1"},
		Port:                 &wrappers.UInt32Value{Value: 12345},
	}
	disRsp.Instances = append(disRsp.Instances, &ins)
	bytes, err := proto.Marshal(disRsp)
	if err != nil {
		panic(err)
	}
	rr.Bytes = bytes
	retMsg.Ancount = 1
	retMsg.Answer = append(retMsg.Answer, &rr)

	packCtrlRR := dns.PackageCtrlRR{
		Hdr:          dns.RR_Header{
			Name:     "ResponseRR",
			Rrtype:   dns.TypePackCtrl,
			Class:    dns.ClassINET,
		},
		TotalCount:   1,
		PackageIndex: 0,
		SplitMode:    0,
	}
	retMsg.Arcount = 1
	retMsg.Extra = append(retMsg.Extra, &packCtrlRR)
}

// mock 处理report client
func (mock *MockSideCarConn) handleReportClient(retMsg *dns.Msg)  {
	retMsg.SetReplyWithoutQuestions(mock.reqMsg)
	rr := dns.LocationRR{
		Hdr: dns.RR_Header{
			Name:     "LocationRR",
			Rrtype:   dns.TypePolarisSideCarLocation,
			Class:    dns.ClassINET,
			Ttl:      0,
			Rdlength: 0,
		},
	}
	retMsg.Answer = append(retMsg.Answer, &rr)
	retMsg.Ancount = 1
}


// SetWriteDeadline
func (mock *MockSideCarConn) SetWriteDeadline(t time.Time) error  {
	return nil
}

// SetReadDeadline
func (mock *MockSideCarConn) SetReadDeadline(t time.Time) error  {
	return nil
}