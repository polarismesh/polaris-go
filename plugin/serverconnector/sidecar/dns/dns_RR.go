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
	"errors"
	"fmt"
	"strconv"
)

// RR new Map.
var TypeToRR = map[uint16]func() RR{
	TypeA:                      func() RR { return new(A) },
	TypeAAAA:                   func() RR { return new(AAAA) },
	TypePackCtrl:               func() RR { return new(PackageCtrlRR) },
	TypePolarisStream:          func() RR { return new(StreamRR) },
	TypePolarisSideCarLocation: func() RR { return new(LocationRR) },
	TypePolarisDetailErrInfo:   func() RR { return new(DetailErrInfoRR) },
	TypePolarisResponse:        func() RR { return new(ResponseRR) },
	TypePolarsHead:             func() RR { return new(PolarisHeaderRR) },
	TypeOPT:                    func() RR { return new(OPT) },
}

//RR的header
type RR_Header struct {
	Name     string `dns:"cdomain-name"`
	Rrtype   uint16
	Class    uint16
	Ttl      uint32
	Rdlength uint16 // Length of data after header.
}

// 返回RR header
func (h *RR_Header) Header() *RR_Header { return h }

// 深拷贝函数，未实现
func (h *RR_Header) copy() RR { return nil }

//为了调试输出，非必要实现
func (h *RR_Header) String() string {
	var s string

	if h.Rrtype == TypeOPT {
		s = ";"
		// and maybe other things
	}

	s += h.Name + "\t"
	s += strconv.FormatInt(int64(h.Ttl), 10) + "\t"
	s += Class(h.Class).String() + "\t"
	s += Type(h.Rrtype).String() + "\t"
	return s
}

//没有用处，单纯为了实现interface
func (h *RR_Header) packData(buff *bytes.Buffer) (int, error) {
	// RR_Header has no RDATA to pack.
	return 0, nil
}

//RR header反序列化，此函数未实现，见下面unpackRRHeader
func (h *RR_Header) unpack(msg []byte, off int) (int, error) {
	panic("dns: internal error: unpack should never be called on RR_Header")
}

// RR header序列化
func (hdr RR_Header) packHeader(buff *bytes.Buffer, dataLength uint16) (int, error) {
	oldLen := buff.Len()
	err := packDomainName(hdr.Name, buff)
	if err != nil {
		return 0, err
	}
	err = packUint16(hdr.Rrtype, buff)
	if err != nil {
		return 0, err
	}
	err = packUint16(hdr.Class, buff)
	if err != nil {
		return 0, err
	}
	err = packUint32(hdr.Ttl, buff)
	if err != nil {
		return 0, err
	}
	err = packUint16(dataLength, buff) // The RDLENGTH field will be set later in packRR.
	if err != nil {
		return 0, err
	}
	return buff.Len() - oldLen, nil
}

// RR header反序列化
func unpackRRHeader(msg []byte, off int) (rr RR_Header, off1 int, err error) {
	hdr := RR_Header{}
	if off == len(msg) {
		return hdr, off, nil
	}

	hdr.Name, off, err = UnpackDomainName(msg, off)
	if err != nil {
		return hdr, len(msg), err
	}
	hdr.Rrtype, off, err = unpackUint16(msg, off)
	if err != nil {
		return hdr, len(msg), err
	}
	hdr.Class, off, err = unpackUint16(msg, off)
	if err != nil {
		return hdr, len(msg), err
	}
	hdr.Ttl, off, err = unpackUint32(msg, off)
	if err != nil {
		return hdr, len(msg), err
	}
	hdr.Rdlength, off, err = unpackUint16(msg, off)
	if err != nil {
		return hdr, len(msg), err
	}
	//msg, err = truncateMsgFromRdlength(msg, off, hdr.Rdlength)
	return hdr, off, err
}

// RR interface定义
type RR interface {
	// 返回RR header
	Header() *RR_Header
	// RR中data的序列化
	PackData(buff *bytes.Buffer) (int, error)
	// RR中data的反序列化
	UnPackData(msg []byte, off int) (int, error)
	// 获取RR data的二进制数据
	GetData() []byte
}

// RR序列化
func PackRR(rr RR, buff *bytes.Buffer) (int, error) {
	if rr == nil {
		return 0, &Error{err: "nil rr"}
	}

	oldLen := buff.Len()
	dataBuff := new(bytes.Buffer)
	length, err := rr.PackData(dataBuff)
	if err != nil {
		return 0, err
	}
	_, err = rr.Header().packHeader(buff, uint16(length))
	if err != nil {
		return 0, err
	}
	_, err = buff.Write(dataBuff.Bytes())

	return buff.Len() - oldLen, nil
}

// RR反序列化
func UnpackRR(msg []byte, off int) (RR, int, error) {
	h, off, err := unpackRRHeader(msg, off)
	if err != nil {
		return nil, len(msg), err
	}
	return UnpackRRWithRRHeader(h, msg, off)
}

// 通过RR header信息反序列化RR其他部分
func UnpackRRWithRRHeader(h RR_Header, msg []byte, off int) (RR, int, error) {
	var err error
	var rr RR
	if newFn, ok := TypeToRR[h.Rrtype]; ok {
		rr = newFn()
		*rr.Header() = h
	} else {
		return nil, 0, errors.New(fmt.Sprintf("invalid RR rtype:%d", h.Rrtype))
	}

	if h.Rdlength == 0 {
		return rr, off, nil
	}

	end := off + int(h.Rdlength)

	off, err = rr.UnPackData(msg, off)
	if err != nil {
		return nil, end, err
	}
	if off != end {
		return rr, end, &Error{err: "bad rdlength"}
	}

	return rr, off, nil
}

// 反序列化RR的列表
func unpackRRslice(l int, msg []byte, off int) ([]RR, int, error) {
	var err error
	var r RR
	// Don't pre-allocate, l may be under attacker control
	var dst []RR
	for i := 0; i < l; i++ {
		off1 := off
		r, off, err = UnpackRR(msg, off)
		if err != nil {
			off = len(msg)
			break
		}
		// If offset does not increase anymore, l is a lie
		if off1 == off {
			break
		}
		dst = append(dst, r)
	}
	if err != nil && off == len(msg) {
		dst = nil
	}
	return dst, off, err
}
