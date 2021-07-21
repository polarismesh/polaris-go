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
package dns

import (
	"bytes"
	"encoding/binary"
)

// uint16序列化
func packUint16(i uint16, buffer *bytes.Buffer) error {
	err := binary.Write(buffer, binary.BigEndian, i)
	if err != nil {
		return err
	}
	return nil
}

// uint16反序列化
func unpackUint16(msg []byte, off int) (uint16, int, error) {
	if off+2 > len(msg) {
		return 0, len(msg), &Error{err: "overflow unpacking uint16"}
	}
	return binary.BigEndian.Uint16(msg[off:]), off + 2, nil
}

// uint32序列化
func packUint32(i uint32, buffer *bytes.Buffer) error {
	err := binary.Write(buffer, binary.BigEndian, i)
	if err != nil {
		return err
	}
	return nil
}

// uint32反序列化
func unpackUint32(msg []byte, off int) (uint32, int, error) {
	if off+4 > len(msg) {
		return 0, len(msg), &Error{err: "overflow unpacking uint32"}
	}
	return binary.BigEndian.Uint32(msg[off:]), off + 4, nil
}

// polaris 序列化（有4层分包逻辑）
func PackStreamDataToDnsProto(data []byte, id uint16, opCode int, rrType uint16) ([]*Msg, error) {
	// maxStreamRRDataSize 需要精确计算，重新定义
	maxStreamRRDataSize := 50000
	var msgArr []*Msg
	off := 0
	for {
		msg := new(Msg)
		msg.MsgHdr.SetDefaultValue(id, opCode)
		rr := StreamRR{
			Hdr: RR_Header{
				Name:   "streamRR",
				Rrtype: TypePolarisStream,
				Class:  ClassINET,
				Ttl:    0,
			},
		}
		if off+maxStreamRRDataSize >= len(data) {
			rr.Bytes = data[off:]
			msg.Answer = append(msg.Answer, &rr)
			msgArr = append(msgArr, msg)
			msg.Ancount = 1
			break
		} else {
			rr.Bytes = data[off : off+maxStreamRRDataSize]
			off += maxStreamRRDataSize
			msg.Answer = append(msg.Answer, &rr)
			msgArr = append(msgArr, msg)
			msg.Ancount = 1
			continue
		}
	}
	// 添加控制 RR
	for idx, v := range msgArr {
		ctrlRR := PackageCtrlRR{
			Hdr: RR_Header{
				Name:     PackCtrlRRName,
				Rrtype:   TypePackCtrl,
				Class:    1101,
				Ttl:      0,
				Rdlength: 4,
			},
			TotalCount:   uint16(len(msgArr)),
			PackageIndex: uint16(idx),
		}
		v.Arcount++
		v.Extra = append(v.Extra, &ctrlRR)
	}
	return msgArr, nil
}
