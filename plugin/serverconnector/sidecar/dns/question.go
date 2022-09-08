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

package dns

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"

	"github.com/golang/protobuf/proto"

	v1 "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
)

var TypeToQuestion = map[int]func() Question{
	OpcodeQuery:                     func() Question { return new(DNSQuestion) },
	OpCodePolarisGetOneInstance:     func() Question { return new(PolarisGetResourceQuestion) },
	OpCodePolarisGetResource:        func() Question { return new(PolarisGetResourceQuestion) },
	OpCodePolarisRegisterInstance:   func() Question { return new(PolarisInstanceQuestion) },
	OpCodePolarisDeregisterInstance: func() Question { return new(PolarisInstanceQuestion) },
	OpCodePolarisHeartbeat:          func() Question { return new(PolarisInstanceQuestion) },
	OpCodePolarisReportClient:       func() Question { return new(PolarisReportClientQuestion) },
}

// Question dns Question interface定义
type Question interface {
	String() string
	pack(buffer *bytes.Buffer) (int, error)
	unPack(data []byte, off int) (int, error)
	unPackPolarisReq() error
}

// DNSQuestion 标准的dns question
type DNSQuestion struct {
	Name   string `dns:"cdomain-name"` // "cdomain-name" specifies encoding (and may be compressed)
	Qtype  uint16
	Qclass uint16
}

// 序列化
func (q *DNSQuestion) pack(buffer *bytes.Buffer) (int, error) {
	oldLen := buffer.Len()
	err := packDomainName(q.Name, buffer)
	if err != nil {
		return 0, err
	}
	err = binary.Write(buffer, binary.BigEndian, q.Qtype)
	if err != nil {
		return 0, err
	}
	err = binary.Write(buffer, binary.BigEndian, q.Qclass)
	if err != nil {
		return 0, err
	}
	length := buffer.Len() - oldLen
	return length, nil
}

// String 打印，便于调式等
func (q *DNSQuestion) String() (s string) {
	// prefix with ; (as in dig)
	s = ";" + q.Name + "\t"
	s += Class(q.Qclass).String() + "\t"
	s += " " + Type(q.Qtype).String()
	return s
}

// 反序列化
func (q *DNSQuestion) unPack(msg []byte, off int) (int, error) {
	var err error
	q.Name, off, err = UnpackDomainName(msg, off)
	if err != nil {
		return off, err
	}
	if off == len(msg) {
		return off, nil
	}
	q.Qtype, off, err = unpackUint16(msg, off)
	if err != nil {
		return off, err
	}
	if off == len(msg) {
		return off, nil
	}
	q.Qclass, off, err = unpackUint16(msg, off)
	if off == len(msg) {
		return off, nil
	}
	return off, err
}

// 单纯为实现接口
func (q *DNSQuestion) unPackPolarisReq() error {
	return nil
}

// 序列化域名  如：www.oa.com  0x3www0x2oa0x3com0
func packDomainName(s string, buff *bytes.Buffer) error {
	ls := len(s)
	if ls == 0 { // Ok, for instance when dealing with update RR without any rdata.
		return nil
	}
	strs := strings.Split(s, ".")
	var err error
	for _, v := range strs {
		if len(v) == 0 {
			continue
		}
		err = binary.Write(buff, binary.BigEndian, uint8(len(v)))
		if err != nil {
			return err
		}
		_, err = buff.WriteString(v)
		if err != nil {
			return err
		}
	}
	err = binary.Write(buff, binary.BigEndian, uint8(0))
	if err != nil {
		return err
	}
	return nil
}

// 反序列化域名
func UnpackDomainName(msg []byte, off int) (string, int, error) {
	s := make([]byte, 0, maxDomainNamePresentationLength)
	off1 := off
	var size uint8
	for {
		size = uint8(msg[off1])
		off1++
		if size == 0 {
			break
		}
		s = append(s, msg[off1:off1+int(size)]...)
		s = append(s, '.')
		off1 += int(size)
	}
	if len(s) == 0 {
		return ".", off1, nil
	}
	s = s[0 : len(s)-1]
	return string(s), off1, nil
}

// BasePolarisQuestion base Polaris question
type BasePolarisQuestion struct {
	Name   string `dns:"cdomain-name"` // "cdomain-name" specifies encoding (and may be compressed)
	Qtype  uint16
	Qclass uint16

	data []byte
}

// 序列化
func (q *BasePolarisQuestion) pack(buffer *bytes.Buffer) (int, error) {
	oldLen := buffer.Len()
	err := packUint16(uint16(len(q.data)), buffer)
	if err != nil {
		return 0, err
	}
	_, err = buffer.Write(q.data)
	if err != nil {
		return 0, err
	}
	err = binary.Write(buffer, binary.BigEndian, q.Qtype)
	if err != nil {
		return 0, err
	}
	err = binary.Write(buffer, binary.BigEndian, q.Qclass)
	if err != nil {
		return 0, err
	}
	length := buffer.Len() - oldLen
	return length, nil
}

// string打印
func (q *BasePolarisQuestion) String() (s string) {
	// prefix with ; (as in dig)
	s = ";" + q.Name + "\t"
	s += Class(q.Qclass).String() + "\t"
	s += " " + Type(q.Qtype).String()
	return s
}

// 反序列化
func (q *BasePolarisQuestion) unPack(msg []byte, off int) (int, error) {
	var size uint16
	size, off1, err := unpackUint16(msg, off)
	if err != nil {
		return off, err
	}
	q.data = msg[off1 : off1+int(size)]
	off1 = off1 + int(size)
	q.Qtype, off1, err = unpackUint16(msg, off1)
	if err != nil {
		return off1, err
	}

	q.Qclass, off1, err = unpackUint16(msg, off1)
	if err != nil {
		return off1, err
	}
	return off1, nil
}

// 反序列化 Polaris req
func (q *BasePolarisQuestion) unPackPolarisReq() error {
	return nil
}

// PolarisGetResourceQuestion Polaris 获取Resource的question
type PolarisGetResourceQuestion struct {
	BasePolarisQuestion
	Req *v1.DiscoverRequest
}

// 序列化
func (q *PolarisGetResourceQuestion) pack(buffer *bytes.Buffer) (int, error) {
	var err error
	q.data, err = proto.Marshal(q.Req)
	if err != nil {
		return 0, err
	}
	return q.BasePolarisQuestion.pack(buffer)
}

// 反序列化 Polaris req
func (q *PolarisGetResourceQuestion) unPackPolarisReq() error {
	q.Req = new(v1.DiscoverRequest)
	err := proto.Unmarshal(q.data, q.Req)
	if err != nil {
		return err
	}
	return nil
}

// PolarisReportClientQuestion Polaris 客户端上报（获取SideCar地理位置）
type PolarisReportClientQuestion struct {
	BasePolarisQuestion
	Req *v1.Client
}

// 序列化
func (q *PolarisReportClientQuestion) pack(buffer *bytes.Buffer) (int, error) {
	var err error
	q.data, err = proto.Marshal(q.Req)
	if err != nil {
		return 0, err
	}
	return q.BasePolarisQuestion.pack(buffer)
}

// 反序列化 Polaris req
func (q *PolarisReportClientQuestion) unPackPolarisReq() error {
	q.Req = new(v1.Client)
	err := proto.Unmarshal(q.data, q.Req)
	if err != nil {
		return err
	}
	return nil
}

// PolarisInstanceQuestion Polaris 注册、反注册、心跳 Question
type PolarisInstanceQuestion struct {
	BasePolarisQuestion
	Req *v1.Instance
}

// 序列化
func (q *PolarisInstanceQuestion) pack(buffer *bytes.Buffer) (int, error) {
	var err error
	q.data, err = proto.Marshal(q.Req)
	if err != nil {
		return 0, err
	}
	return q.BasePolarisQuestion.pack(buffer)
}

// 反序列化 Polaris req
func (q *PolarisInstanceQuestion) unPackPolarisReq() error {
	q.Req = new(v1.Instance)
	err := proto.Unmarshal(q.data, q.Req)
	if err != nil {
		return err
	}
	return nil
}

// 反序列化 Question
func unpackQuestion(msg []byte, off int, opCode int) (Question, int, error) {
	if fun, ok := TypeToQuestion[opCode]; ok {
		question := fun()
		off, err := question.unPack(msg, off)
		if err != nil {
			return nil, off, err
		}
		err = question.unPackPolarisReq()
		if err != nil {
			return nil, off, err
		}
		return question, off, nil
	}
	return nil, 0, errors.New("invalid opcode")
}
