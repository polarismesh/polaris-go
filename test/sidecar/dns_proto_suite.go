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
package sidecar

import (
	"bytes"
	_ "fmt"
	"github.com/polarismesh/polaris-go/pkg/model"
	v1 "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
	connectorComm "github.com/polarismesh/polaris-go/plugin/serverconnector/common"
	"github.com/polarismesh/polaris-go/plugin/serverconnector/sidecar/dns"

	"github.com/polarismesh/polaris-go/plugin/serverconnector/sidecar/model/pb"

	"github.com/golang/protobuf/proto"
	"github.com/golang/protobuf/ptypes/wrappers"
	"gopkg.in/check.v1"
	"net"
)

// dns协议 序列化测试
type DnsProtoSuit struct {
}

// SetUpSuite
func (s *DnsProtoSuit) SetUpSuite(c *check.C) {
}

// TearDownSuite
func (s *DnsProtoSuit) TearDownSuite(c *check.C) {

}
// 测试标准DNS请求
// RR A
func (s *DnsProtoSuit) TestNormalDnsReqPackUnpack(c *check.C)  {
	// 序列化
	msg := dns.Msg{
		MsgHdr:   dns.MsgHdr{},
	}
	msg.SetDefaultValue(0, 0)
	msg.SetDNSQuestion("www.oa.com", 0)
	buf, err := msg.Pack()
	c.Assert(err, check.IsNil)
	bytes := buf.Bytes()
	c.Assert(bytes, check.NotNil)
	// 反序列化
	msg1 := new(dns.Msg)
	err = msg1.Unpack(bytes)
	c.Assert(err, check.IsNil)
	c.Assert(msg.Opcode, check.Equals, 0)
	c.Assert(int(msg1.Qdcount), check.Equals, 1)
	dnsQuestion := msg1.Question[0].(*dns.DNSQuestion)
	c.Assert(dnsQuestion.Name, check.Equals, "www.oa.com")

	dnsQuestion.String()
}


// 测试DNS返回
// RR A
func (s *DnsProtoSuit) TestNormalDnsRspPackUnpack(c *check.C)  {
	// 序列化
	msg := dns.Msg{
		MsgHdr:   dns.MsgHdr{},
	}
	msg.MsgHdr.String()
	h2 := new(dns.MsgHdr)
	err := msg.MsgHdr.Copy(h2)
	c.Assert(err, check.IsNil)

	msg.SetDefaultValue(0, 0)
	msg.SetDNSQuestion("www.oa.com", 0)
	msg.Response = true
	// add A RR
	rr := dns.A{
		Hdr: dns.RR_Header{
			Name: "www.oa.com",
			Rrtype: dns.TypeA,
			Class: dns.ClassINET,
			Ttl: 3,
			Rdlength: uint16(4),
		},
		A:   net.ParseIP("127.0.0.1"),
	}
	msg.Ancount += 1
	msg.Answer = append(msg.Answer, &rr)
	buf, err := msg.Pack()
	c.Assert(err, check.IsNil)
	bytes := buf.Bytes()
	c.Assert(bytes, check.NotNil)
	// 反序列化
	msg1 := new(dns.Msg)
	err = msg1.Unpack(bytes)
	c.Assert(err, check.IsNil)
	c.Assert(msg.Opcode, check.Equals, 0)
	c.Assert(msg.Response, check.Equals, true)
	c.Assert(msg.Rcode, check.Equals, 0)
	c.Assert(int(msg.Ancount), check.Equals, 1)
	rr0 := msg.Answer[0].(*dns.A)
	c.Assert(int(rr0.Hdr.Rrtype), check.Equals, 1)
	c.Assert(int(rr0.Hdr.Class), check.Equals, 1)
	c.Assert(int(rr0.Hdr.Ttl), check.Equals, 3)
	c.Assert(int(rr0.Hdr.Rdlength), check.Equals, 4)
	c.Assert(rr0.A.String(), check.Equals, "127.0.0.1")
	// 需代码覆盖
	msg1.SetReplyWithoutQuestions(&msg)
	msg1.SetReply(&msg)
}

// 测试DNS AAAA返回
// RR AAAA
func (s *DnsProtoSuit) TestNormalDnsRspPackUnpack01(c *check.C)  {
	// 序列化
	msg := dns.Msg{
		MsgHdr:   dns.MsgHdr{},
	}
	msg.SetDefaultValue(0, 0)
	msg.SetDNSQuestion("www.oa.com", 0)
	msg.Response = true
	// add AAAA RR
	rr := dns.AAAA{
		Hdr: dns.RR_Header{
			Name: "www.oa.com",
			Rrtype: dns.TypeAAAA,
			Class: dns.ClassINET,
			Ttl: 3,
			Rdlength: uint16(16),
		},
		AAAA:   net.ParseIP("0:0:0:0:0:ffff:cbcd:8d2b"),
	}
	msg.Ancount += 1
	msg.Answer = append(msg.Answer, &rr)
	buf, err := msg.Pack()
	c.Assert(err, check.IsNil)
	bytes := buf.Bytes()
	c.Assert(bytes, check.NotNil)
	// 反序列化
	msg1 := new(dns.Msg)
	err = msg1.Unpack(bytes)
	c.Assert(err, check.IsNil)
	c.Assert(msg.Opcode, check.Equals, 0)
	c.Assert(msg.Response, check.Equals, true)
	c.Assert(msg.Rcode, check.Equals, 0)
	c.Assert(int(msg.Ancount), check.Equals, 1)
	rr0 := msg.Answer[0].(*dns.AAAA)
	c.Assert(uint16(rr0.Hdr.Rrtype), check.Equals, dns.TypeAAAA)
	c.Assert(int(rr0.Hdr.Class), check.Equals, 1)
	c.Assert(int(rr0.Hdr.Ttl), check.Equals, 3)
	c.Assert(int(rr0.Hdr.Rdlength), check.Equals, 16)
	c.Assert(rr0.AAAA.String(), check.Equals,  net.ParseIP("0:0:0:0:0:ffff:cbcd:8d2b").String())
}

// 测试Instance类请求
func (s *DnsProtoSuit) TestPolarisInstanceReqPackUnpack(c *check.C)   {
	// 序列化
	msg := dns.Msg{
		MsgHdr:   dns.MsgHdr{},
	}
	request := new(model.InstanceRegisterRequest)
	request.Service = "angevil_test"
	request.Namespace = "Test"
	request.Host = "127.0.0.1"
	request.Port = 12345
	msg.SetDefaultValue(0, dns.OpCodePolarisRegisterInstance)
	msg.Qdcount = 1
	//add PolarisInstanceQuestion
	question := dns.PolarisInstanceQuestion{}
	question.Qtype = dns.TypePolarisInstance
	question.Qclass = dns.ClassINET
	question.Req = connectorComm.RegisterRequestToProto(request)
	msg.Question = append(msg.Question, &question)
	buf, err := msg.Pack()
	c.Assert(err, check.IsNil)
	bytes := buf.Bytes()
	c.Assert(bytes, check.NotNil)
	// 反序列化
	msg1 := new(dns.Msg)
	err = msg1.Unpack(bytes)
	c.Assert(err, check.IsNil)
	c.Assert(msg1.Opcode, check.Equals, dns.OpCodePolarisRegisterInstance)
	q := msg.Question[0].(*dns.PolarisInstanceQuestion)
	c.Assert(q.Qtype, check.Equals, dns.TypePolarisInstance)
	c.Assert(q.Req, check.NotNil)
	c.Assert(q.Req.Service.GetValue(), check.Equals, "angevil_test")
	c.Assert(q.Req.Namespace.GetValue(), check.Equals, "Test")
	c.Assert(q.Req.Host.GetValue(), check.Equals, "127.0.0.1")
	c.Assert(q.Req.Port.GetValue(), check.Equals, uint32(12345))

	// 设置 opcode 测试
	msg.SetDefaultValue(0, dns.OpCodePolarisDeregisterInstance)
	buf, err = msg.Pack()
	c.Assert(err, check.IsNil)
	bytes = buf.Bytes()
	c.Assert(bytes, check.NotNil)
	msg2 := new(dns.Msg)
	err = msg2.Unpack(bytes)
	c.Assert(err, check.IsNil)
	c.Assert(msg2.Opcode, check.Equals, dns.OpCodePolarisDeregisterInstance)

	// 设置 opcode 测试
	msg.SetDefaultValue(0, dns.OpCodePolarisReportClient)
	buf, err = msg.Pack()
	c.Assert(err, check.IsNil)
	bytes = buf.Bytes()
	c.Assert(bytes, check.NotNil)
	msg3 := new(dns.Msg)
	err = msg3.Unpack(bytes)
	c.Assert(err, check.IsNil)
	c.Assert(msg3.Opcode, check.Equals, dns.OpCodePolarisReportClient)
}


// 测试report client的请求
func (s *DnsProtoSuit) TestReportClientReq(c *check.C)  {
	// 序列化
	msg := dns.Msg{
		MsgHdr:   dns.MsgHdr{},
	}
	msg.SetDefaultValue(0, dns.OpCodePolarisReportClient)
	msg.Qdcount = 1
	// add PolarisReportClientQuestion
	question := dns.PolarisReportClientQuestion{}
	question.Req = &v1.Client{
		Host:                 &wrappers.StringValue{Value: "127.0.0.1"},
		Type:                 1,
		Version:              &wrappers.StringValue{Value: "t12"},
		Location:             &v1.Location{
			Region:               &wrappers.StringValue{Value: "southChina"},
			Zone:                 &wrappers.StringValue{Value: "shenzhen"},
			Campus:               &wrappers.StringValue{Value: "campus_test"},
		},
	}
	question.BasePolarisQuestion.String()
	msg.Question = append(msg.Question, &question)
	buf, err := msg.Pack()
	c.Assert(err, check.IsNil)
	bytes := buf.Bytes()
	c.Assert(bytes, check.NotNil)
	// 反序列化
	msg1 := new(dns.Msg)
	err = msg1.Unpack(bytes)
	c.Assert(err, check.IsNil)
	c.Assert(msg1.Opcode, check.Equals, dns.OpCodePolarisReportClient)
	q := msg.Question[0].(*dns.PolarisReportClientQuestion)
	c.Assert(q.Req, check.NotNil)
	c.Assert(q.Req.Host.GetValue(), check.Equals, "127.0.0.1")
	c.Assert(q.Req.Type, check.Equals, v1.Client_ClientType(1))
	c.Assert(q.Req.Version.GetValue(), check.Equals, "t12")
	c.Assert(q.Req.Location.Region.GetValue(), check.Equals, "southChina")
	c.Assert(q.Req.Location.Zone.GetValue(), check.Equals, "shenzhen")
	c.Assert(q.Req.Location.Campus.GetValue(), check.Equals, "campus_test")
}


// 测试 report client 返回
func (s *DnsProtoSuit) TestReportClientRsp(c* check.C) {
	// 序列化
	msg := dns.Msg{
		MsgHdr:   dns.MsgHdr{},
	}
	msg.SetDefaultValue(0, dns.OpCodePolarisReportClient)
	// add LocationRR
	rr := dns.LocationRR{
		Hdr:      dns.RR_Header{
			Name: "www.oa.com",
			Rrtype: dns.TypePolarisSideCarLocation,
			Class: dns.ClassINET,
		},
		SideCar:  &v1.Client{
			Host:                 &wrappers.StringValue{Value: "127.0.0.1"},
			Type:                 1,
			Version:              &wrappers.StringValue{Value: "t12"},
			Location:             &v1.Location{
				Region:               &wrappers.StringValue{Value: "southChina"},
				Zone:                 &wrappers.StringValue{Value: "shenzhen"},
				Campus:               &wrappers.StringValue{Value: "campus_test"},
			},
		},
	}
	msg.Ancount += 1
	msg.Answer = append(msg.Answer, &rr)
	buf, err := msg.Pack()
	c.Assert(err, check.IsNil)
	bytes := buf.Bytes()
	c.Assert(bytes, check.NotNil)
	// 反序列化
	msg1 := new(dns.Msg)
	err = msg1.Unpack(bytes)
	c.Assert(err, check.IsNil)
	c.Assert(msg1.Opcode, check.Equals, dns.OpCodePolarisReportClient)
	rr0 := msg1.Answer[0].(*dns.LocationRR)
	c.Assert(rr0.Hdr.Rrtype, check.Equals, dns.TypePolarisSideCarLocation)
}

// 测试Response DNS协议
func (s *DnsProtoSuit) TestPolarisResponseRspPackUnpack(c *check.C)  {
	// 序列化
	msg := dns.Msg{
		MsgHdr:   dns.MsgHdr{},
	}
	msg.SetDefaultValue(0, dns.OpCodePolarisRegisterInstance)
	// add ResponseRR
	rr := dns.ResponseRR{
		Hdr:      dns.RR_Header{
			Name: "www.oa.com",
			Rrtype: dns.TypePolarisResponse,
			Class: dns.ClassINET,
		},
		Response: &v1.Response{
			Code:                &wrappers.UInt32Value{Value: 0},
			Info:                &wrappers.StringValue{Value: "no err"},
			Client:              nil,
			Namespace:           &v1.Namespace{
				Name:                 &wrappers.StringValue{Value: "Test"},
			},
			Service:             &v1.Service{
				Name:                 &wrappers.StringValue{Value: "angevil_test"},
				Namespace:            &wrappers.StringValue{Value: "Test"},
			},
			Instance:            &v1.Instance{
				Id:                   &wrappers.StringValue{Value: "instanceId"},
				Service:              &wrappers.StringValue{Value: "angevil_test"},
				Namespace:            &wrappers.StringValue{Value: "Test"},
				Host:                 &wrappers.StringValue{Value: "127.0.0.1"},
				Port:                 &wrappers.UInt32Value{Value: 12345},
			},
		},
	}
	msg.Answer = append(msg.Answer, &rr)
	msg.Ancount += 1
	buf, err := msg.Pack()
	c.Assert(err, check.IsNil)
	bytes := buf.Bytes()
	c.Assert(bytes, check.NotNil)
	// 反序列化
	msg1 := new(dns.Msg)
	err = msg1.Unpack(bytes)
	c.Assert(err, check.IsNil)
	c.Assert(msg1.Opcode, check.Equals, dns.OpCodePolarisRegisterInstance)
	rr0 := msg1.Answer[0].(*dns.ResponseRR)
	c.Assert(rr0.Hdr.Rrtype, check.Equals, dns.TypePolarisResponse)
	c.Assert(rr0.Response.GetCode().GetValue(),check.Equals, uint32(0))
	c.Assert(rr0.Response.GetInstance().GetNamespace().GetValue(), check.Equals, "Test")
	c.Assert(rr0.Response.GetInstance().GetService().GetValue(), check.Equals, "angevil_test")
	c.Assert(rr0.Response.GetInstance().GetHost().GetValue(), check.Equals, "127.0.0.1")
	c.Assert(rr0.Response.GetInstance().GetPort().GetValue(), check.Equals, uint32(12345))
	data := rr0.GetData()
	c.Assert(data, check.NotNil)
}
// 测试获取资源的请求
func (s *DnsProtoSuit) TestPolarisGetResourceReqPackUnpack(c *check.C)  {
	// 序列化
	msg := dns.Msg{
		MsgHdr:   dns.MsgHdr{},
	}
	msg.SetDefaultValue(0, dns.OpCodePolarisGetResource)
	msg.Qdcount = 1
	// add PolarisGetResourceQuestion
	question := dns.PolarisGetResourceQuestion{}
	question.Req = &v1.DiscoverRequest{
		Type: v1.DiscoverRequest_INSTANCE,
		Service: &v1.Service{
			Name:                 &wrappers.StringValue{Value: "angevil_test"},
			Namespace:            &wrappers.StringValue{Value: "Test"},
		},
	}
	msg.Question = append(msg.Question, &question)
	buf, err := msg.Pack()
	c.Assert(err, check.IsNil)
	bytes := buf.Bytes()
	c.Assert(bytes, check.NotNil)
	// 反序列化
	msg1 := new(dns.Msg)
	err = msg1.Unpack(bytes)
	c.Assert(err, check.IsNil)
	c.Assert(msg1.Opcode, check.Equals, dns.OpCodePolarisGetResource)
	q := msg.Question[0].(*dns.PolarisGetResourceQuestion)
	c.Assert(q.Req, check.NotNil)
	c.Assert(q.Req.Type, check.Equals, v1.DiscoverRequest_INSTANCE)
	c.Assert(q.Req.Service.GetName().GetValue(), check.Equals, "angevil_test")
	c.Assert(q.Req.Service.GetNamespace().GetValue(), check.Equals, "Test")
}
// 测试资源返回包
func (s *DnsProtoSuit) TestPolarisGetResourceRspPackUnpack(c *check.C)  {
	// 序列化
	var err error
	msg := dns.Msg{
		MsgHdr:   dns.MsgHdr{},
	}
	msg.SetDefaultValue(0, dns.OpCodePolarisGetResource)
	// add StreamRR
	rr := dns.StreamRR{
		Hdr:   dns.RR_Header{
			Name: "www.oa.com",
			Rrtype: dns.TypePolarisStream,
			Class: dns.ClassINET,
		},
		Bytes: nil,
	}
	data := v1.DiscoverResponse{
		Code: &wrappers.UInt32Value{Value: 0},
		Type: v1.DiscoverResponse_INSTANCE,
	}
	msg.Ancount += 1
	msg.Answer = append(msg.Answer, &rr)
	rr.Bytes, err = proto.Marshal(&data)
	c.Assert(err, check.IsNil)
	// add PackageCtrlRR
	ctrlRR := dns.PackageCtrlRR{
		Hdr:          dns.RR_Header{
			Name: "www.oa.com",
			Rrtype: dns.TypePackCtrl,
			Class: dns.ClassINET,
		},
		TotalCount:   1,
		PackageIndex: 1,
		SplitMode:    0,
	}
	msg.Extra = append(msg.Extra, &ctrlRR)
	// add DetailErrInfoRR
	errDetailRR := dns.DetailErrInfoRR{
		Hdr:          dns.RR_Header{
			Name: "www.oa.com",
			Rrtype: dns.TypePolarisDetailErrInfo,
			Class: dns.ClassINET,
		},
		ErrInfo: &pb.DetailErrInfo{
			ErrCode: 1,
			ErrMsg:  "error",
		},
	}
	msg.Extra = append(msg.Extra, &errDetailRR)
	msg.Arcount = 2
	buf, err := msg.Pack()
	c.Assert(err, check.IsNil)
	bytes := buf.Bytes()
	c.Assert(bytes, check.NotNil)
	// 反序列化
	msg1 := new(dns.Msg)
	err = msg1.Unpack(bytes)
	c.Assert(err, check.IsNil)
	c.Assert(msg1.Opcode, check.Equals, dns.OpCodePolarisGetResource)
	c.Assert(len(msg1.Extra), check.Equals, 2)
}

// test DNS RR
func (s *DnsProtoSuit) TestDNSRR(c *check.C)  {
	// 序列化
	rr := dns.A{
		Hdr: dns.RR_Header{
			Name: "www.oa.com",
			Rrtype: dns.TypeA,
			Class: dns.ClassINET,
			Ttl: 3,
			Rdlength: uint16(4),
		},
		A:   net.ParseIP("127.0.0.1"),
	}
	rr.String()
	h := rr.Header()
	c.Assert(h, check.NotNil)
	t := rr.Copy()
	c.Assert(t, check.NotNil)
	// AAAA RR
	rr1 := dns.AAAA{
		Hdr:  dns.RR_Header{
			Name: "www.oa.com",
			Rrtype: dns.TypeAAAA,
			Class: dns.ClassINET,
			Ttl: 3,
			Rdlength: uint16(16),
		},
		AAAA: net.ParseIP("0:0:0:0:0:ffff:cbcd:8d2b"),
	}
	rr1.String()
	h = rr1.Header()
	c.Assert(h, check.NotNil)
	t = rr1.Copy()
	c.Assert(t, check.NotNil)
	// PackageCtrlRR
	rr2 := dns.PackageCtrlRR{
		Hdr:  dns.RR_Header{
			Name: "www.oa.com",
			Rrtype: dns.TypePackCtrl,
			Class: dns.ClassINET,
			Ttl: 3,
			Rdlength: uint16(5),
		},
		TotalCount: 2,
		PackageIndex: 0,
	}
	rr2.String()
	h = rr2.Header()
	c.Assert(h, check.NotNil)
	t = rr2.Copy()
	c.Assert(t, check.NotNil)
	data := rr2.GetData()
	c.Assert(data, check.IsNil)
	// PolarisHeaderRR
	rr3 := dns.PolarisHeaderRR{
		Hdr:  dns.RR_Header{
			Name: "www.oa.com",
			Rrtype: dns.TypePackCtrl,
			Class: dns.ClassINET,
			Ttl: 3,
			Rdlength: uint16(5),
		},
	}
	h = rr3.Header()
	c.Assert(h, check.NotNil)
}

func (s *DnsProtoSuit) TestDNSRR1(c *check.C)   {
	// StreamRR
	rr4 := dns.StreamRR{
		Hdr:  dns.RR_Header{
			Name: "www.oa.com",
			Rrtype: dns.TypePolarisStream,
			Class: dns.ClassINET,
			Ttl: 3,
			Rdlength: uint16(5),
		},
		Bytes:  []byte("12345"),
	}
	h := rr4.Header()
	c.Assert(h, check.NotNil)
	data := rr4.GetData()
	c.Assert(data, check.NotNil)
	// DetailErrInfoRR
	rr5 := dns.DetailErrInfoRR{
		Hdr:     dns.RR_Header{
			Name: "www.oa.com",
			Rrtype: dns.TypePolarisStream,
			Class: dns.ClassINET,
			Ttl: 3,
			Rdlength: uint16(5),
		},
		ErrInfo: nil,
	}
	h = rr5.Header()
	c.Assert(h, check.NotNil)
	// PolarisHeaderRR
	rr6 := dns.PolarisHeaderRR{
		Hdr:     dns.RR_Header{
			Name: "www.oa.com",
			Rrtype: dns.TypePolarisStream,
			Class: dns.ClassINET,
			Ttl: 3,
			Rdlength: uint16(5),
		},
	}
	h = rr6.Header()
	c.Assert(h, check.NotNil)
	data = rr6.GetData()
	c.Assert(data, check.IsNil)

	// test NewARR
	a := dns.NewARR("www.oa.com", net.ParseIP("127.0.0.1"), 3)
	c.Assert(a, check.NotNil)
	// test NewAAAARR
	b := dns.NewAAAARR("www.oa.com", net.ParseIP("127.0.0.1"), 3)
	c.Assert(b, check.NotNil)
}

// 测试DNS header
func (s *DnsProtoSuit) TestDnsMsgHdr(c *check.C)  {
	// 序列化
	hdr := dns.MsgHdr{
		Id:                 0,
		Response:           true,
		Opcode:             0,
		Authoritative:      true,
		Truncated:          true,
		RecursionDesired:   true,
		RecursionAvailable: true,
		Zero:               true,
		AuthenticatedData:  true,
		CheckingDisabled:   true,
		Rcode:              0,
		Qdcount:            0,
		Ancount:            0,
		Nscount:            0,
		Arcount:            0,
	}
	buf := new(bytes.Buffer)
	_, err := hdr.Pack(buf)
	c.Assert(err, check.IsNil)
}

// 测试 bytes转为多包DNS
func (s *DnsProtoSuit) TestPackStreamDataToDnsProto(c *check.C)  {
	var bytes []byte
	for i:=0; i<100000; i++ {
		bytes = append(bytes, 'a')
	}
	dnsArr, err := dns.PackStreamDataToDnsProto(bytes, 1, dns.OpCodePolarisGetResource, dns.TypePolarisStream)
	c.Assert(err, check.IsNil)
	c.Assert(len(dnsArr), check.Equals, 2)
}

// 测试error struct
func (s *DnsProtoSuit) TestOtherStruct(c *check.C)  {
	err := dns.Error{}
	err.Error()
}

// edns RR test
func (s *DnsProtoSuit) TestEDNSRR(c *check.C)  {
	// 序列化
	var err error
	msg := dns.Msg{
		MsgHdr:   dns.MsgHdr{},
	}
	msg.SetDefaultValue(0, dns.OpcodeQuery)
	// add BaseEDNSData
	optData := dns.BaseEDNSData{
		Code:       0,
		OptionData: []byte("123"),
	}
	// add OPT RR
	rr := dns.OPT{
		Hdr:    dns.RR_Header{
			Name: "www.oa.com",
			Rrtype: dns.TypeOPT,
			Class: dns.ClassINET,
			Ttl: 3,
			Rdlength: uint16(7),
		},
	}
	rr.Option = append(rr.Option, &optData)
	msg.Extra = append(msg.Extra, &rr)
	msg.Arcount = 1
	buf, err := msg.Pack()
	c.Assert(err, check.IsNil)

	// 反序列化
	bytesData := buf.Bytes()
	msg1 := new(dns.Msg)
	err = msg1.Unpack(bytesData)
	c.Assert(err, check.IsNil)
	c.Assert(int(msg1.Arcount), check.Equals, 1)
}