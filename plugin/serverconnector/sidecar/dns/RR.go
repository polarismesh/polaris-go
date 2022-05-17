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
	"net"

	"github.com/golang/protobuf/proto"

	namingpb "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
	sidecarPb "github.com/polarismesh/polaris-go/plugin/serverconnector/sidecar/model/pb"
)

// A 标准的DNS IPv4 RR
type A struct {
	Hdr RR_Header
	A   net.IP `dns:"a"`
}

// Header 返回header
func (rr *A) Header() *RR_Header {
	return &rr.Hdr
}

// String 打印string
func (rr *A) String() string {
	if rr.A == nil {
		return rr.Hdr.String()
	}
	return rr.Hdr.String() + rr.A.String()
}

// Copy 深拷贝
func (rr *A) Copy() RR {
	return &A{rr.Hdr, copyIP(rr.A)}
}

// GetData 获取RR data
func (rr *A) GetData() []byte {
	return rr.A
}

// PackData 序列化 RR data
func (rr *A) PackData(buff *bytes.Buffer) (int, error) {
	switch len(rr.A) {
	case net.IPv4len, net.IPv6len:
		_, err := buff.Write(rr.A.To4())
		if err != nil {
			return 0, err
		}
	case 0:
		// Allowed, for dynamic updates.
	default:
		return 0, &Error{err: "overflow packing a"}
	}
	return 4, nil
}

// UnPackData 反序列化RR data
func (rr *A) UnPackData(msg []byte, off int) (int, error) {
	if off+net.IPv4len > len(msg) {
		return len(msg), &Error{err: "overflow unpacking a"}
	}
	rr.A = append(make(net.IP, 0, net.IPv4len), msg[off:off+net.IPv4len]...)
	off += net.IPv4len
	return off, nil
}

// AAAA 标准的DNS IPv6 RR
type AAAA struct {
	Hdr  RR_Header
	AAAA net.IP `dns:"aaaa"`
}

// Header 返回header
func (rr *AAAA) Header() *RR_Header {
	return &rr.Hdr
}

// String 打印string
func (rr *AAAA) String() string {
	if rr.AAAA == nil {
		return rr.Hdr.String()
	}
	return rr.Hdr.String() + rr.AAAA.String()
}

// Copy 深拷贝
func (rr *AAAA) Copy() RR {
	return &AAAA{rr.Hdr, copyIP(rr.AAAA)}
}

// PackData 序列化 RR data
func (rr *AAAA) PackData(buff *bytes.Buffer) (int, error) {
	switch len(rr.AAAA) {
	case net.IPv6len:
		_, err := buff.Write(rr.AAAA.To16())
		if err != nil {
			return 0, err
		}
	case 0:
		// Allowed, dynamic updates.
	default:
		return 0, &Error{err: "overflow packing aaaa"}
	}
	return net.IPv6len, nil
}

// UnPackData 反序列化RR data
func (rr *AAAA) UnPackData(msg []byte, off int) (int, error) {
	if off+net.IPv6len > len(msg) {
		return len(msg), &Error{err: "overflow unpacking aaaa"}
	}
	rr.AAAA = append(make(net.IP, 0, net.IPv6len), msg[off:off+net.IPv6len]...)
	off += net.IPv6len
	return off, nil
}

// GetData 获取RR data
func (rr *AAAA) GetData() []byte {
	return rr.AAAA
}

// SRV RR. See RFC 2782.
type SRV struct {
	Hdr      RR_Header
	Priority uint16
	Weight   uint16
	Port     uint16
	Target   string `dns:"domain-name"`
}

// PackageCtrlRR 用于大量数据，UDP分包控制
type PackageCtrlRR struct {
	Hdr          RR_Header
	TotalCount   uint16
	PackageIndex uint16
	SplitMode    uint8
}

// Header 返回header
func (rr *PackageCtrlRR) Header() *RR_Header {
	return &rr.Hdr
}

// String 打印string
func (rr *PackageCtrlRR) String() string {
	return ""
}

// Copy 深拷贝
func (rr *PackageCtrlRR) Copy() RR {
	return &PackageCtrlRR{
		TotalCount:   rr.TotalCount,
		PackageIndex: rr.PackageIndex,
	}
}

// PackData 序列化 RR data
func (rr *PackageCtrlRR) PackData(buff *bytes.Buffer) (int, error) {
	oldLen := buff.Len()
	err := packUint16(rr.TotalCount, buff)
	if err != nil {
		return 0, err
	}
	err = packUint16(rr.PackageIndex, buff)
	if err != nil {
		return 0, err
	}
	err = binary.Write(buff, binary.BigEndian, rr.SplitMode)
	if err != nil {
		return 0, err
	}
	return buff.Len() - oldLen, nil
}

// UnPackData 反序列化RR data
func (rr *PackageCtrlRR) UnPackData(msg []byte, off int) (int, error) {
	var err error
	if off+4 > len(msg) {
		return len(msg), &Error{err: "overflow unpacking PackageCtrlRR"}
	}
	rr.TotalCount, off, err = unpackUint16(msg, off)
	if err != nil {
		return off, err
	}
	rr.PackageIndex, off, err = unpackUint16(msg, off)
	if err != nil {
		return off, err
	}
	if off+1 > len(msg) {
		return 0, errors.New("overflow unpacking uint16")
	}
	rr.SplitMode = uint8(msg[off])
	off++
	return off, nil
}

// GetData 获取RR data
func (rr *PackageCtrlRR) GetData() []byte {
	return nil
}

// PolarisHeaderRR polaris 自定义header RR (additional RR)
type PolarisHeaderRR struct {
	Hdr RR_Header
}

// Header 返回header
func (rr *PolarisHeaderRR) Header() *RR_Header {
	return &rr.Hdr
}

// PackData 序列化 RR data
func (rr *PolarisHeaderRR) PackData(buff *bytes.Buffer) (int, error) {
	return buff.Len(), nil
}

// UnPackData 反序列化RR data
func (rr *PolarisHeaderRR) UnPackData(msg []byte, off int) (int, error) {
	return off, nil
}

// GetData GetData
func (rr *PolarisHeaderRR) GetData() []byte {
	return nil
}

// LocationRR location RR
type LocationRR struct {
	StreamRR
	Hdr     RR_Header
	SideCar *namingpb.Client
}

// Header 返回header
func (rr *LocationRR) Header() *RR_Header {
	return &rr.Hdr
}

// PackData 序列化 RR data
func (rr *LocationRR) PackData(buff *bytes.Buffer) (int, error) {
	oldLen := buff.Len()
	bytes, err := proto.Marshal(rr.SideCar)
	if err != nil {
		return 0, err
	}
	_, err = buff.Write(bytes)
	if err != nil {
		return 0, err
	}
	return buff.Len() - oldLen, nil
}

// UnPackData 反序列化RR data
func (rr *LocationRR) UnPackData(msg []byte, off int) (int, error) {
	var err error
	size := rr.Hdr.Rdlength
	rr.SideCar = new(namingpb.Client)
	err = proto.Unmarshal(msg[off:off+int(size)], rr.SideCar)
	if err != nil {
		return off, err
	}
	off += int(size)
	return off, nil
}

// StreamRR polaris 二进制流RR 用于4层分包
type StreamRR struct {
	Hdr   RR_Header
	Bytes []byte
}

// Header 返回header
func (rr *StreamRR) Header() *RR_Header {
	return &rr.Hdr
}

// PackData 序列化 RR data
func (rr *StreamRR) PackData(buff *bytes.Buffer) (int, error) {
	oldLen := buff.Len()
	_, err := buff.Write(rr.Bytes)
	if err != nil {
		return 0, err
	}
	return buff.Len() - oldLen, nil
}

// UnPackData 反序列化RR data
func (rr *StreamRR) UnPackData(msg []byte, off int) (int, error) {
	length := rr.Hdr.Rdlength
	rr.Bytes = append(msg[off : off+int(length)])
	off += int(length)
	return off, nil
}

// GetData 获取RR data
func (rr *StreamRR) GetData() []byte {
	return rr.Bytes
}

// DetailErrInfoRR polaris 详细错误 RR
type DetailErrInfoRR struct {
	Hdr     RR_Header
	ErrInfo *sidecarPb.DetailErrInfo
}

// Header 返回header
func (rr *DetailErrInfoRR) Header() *RR_Header {
	return &rr.Hdr
}

// PackData 序列化 RR data
func (rr *DetailErrInfoRR) PackData(buff *bytes.Buffer) (int, error) {
	oldLen := buff.Len()

	bytes, err := proto.Marshal(rr.ErrInfo)
	if err != nil {
		return 0, err
	}
	_, err = buff.Write(bytes)
	if err != nil {
		return 0, err
	}
	return buff.Len() - oldLen, nil
}

// UnPackData 反序列化RR data
func (rr *DetailErrInfoRR) UnPackData(msg []byte, off int) (int, error) {
	length := rr.Hdr.Rdlength
	bytes := msg[off : off+int(length)]
	off += int(length)

	rr.ErrInfo = new(sidecarPb.DetailErrInfo)
	err := proto.Unmarshal(bytes, rr.ErrInfo)
	if err != nil {
		return off, err
	}
	return off, nil
}

// GetData 获取RR data
func (rr *DetailErrInfoRR) GetData() []byte {
	return nil
}

// ResponseRR polaris 应答RR
type ResponseRR struct {
	StreamRR
	Hdr      RR_Header
	Response *namingpb.Response
}

// Header 返回header
func (rr *ResponseRR) Header() *RR_Header {
	return &rr.Hdr
}

// PackData 序列化 RR data
func (rr *ResponseRR) PackData(buff *bytes.Buffer) (int, error) {
	oldLen := buff.Len()

	bytes, err := proto.Marshal(rr.Response)
	if err != nil {
		return 0, err
	}
	_, err = buff.Write(bytes)
	if err != nil {
		return 0, err
	}
	return buff.Len() - oldLen, nil
}

// UnPackData 反序列化RR data
func (rr *ResponseRR) UnPackData(msg []byte, off int) (int, error) {
	length := rr.Hdr.Rdlength
	rr.Bytes = append(msg[off : off+int(length)])
	off += int(length)

	rr.Response = new(namingpb.Response)
	err := proto.Unmarshal(rr.Bytes, rr.Response)
	if err != nil {
		return off, err
	}
	return off, nil
}

// GetData 获取RR data
func (rr *ResponseRR) GetData() []byte {
	return rr.Bytes
}
