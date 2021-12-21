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
)

// EDNS0 Option codes.
const (
	EDNS0LLQ          = 0x1     // long lived queries: http://tools.ietf.org/html/draft-sekar-dns-llq-01
	EDNS0UL           = 0x2     // update lease draft: http://files.dns-sd.org/draft-sekar-dns-ul.txt
	EDNS0NSID         = 0x3     // nsid (See RFC 5001)
	EDNS0DAU          = 0x5     // DNSSEC Algorithm Understood
	EDNS0DHU          = 0x6     // DS Hash Understood
	EDNS0N3U          = 0x7     // NSEC3 Hash Understood
	EDNS0SUBNET       = 0x8     // client-subnet (See RFC 7871)
	EDNS0EXPIRE       = 0x9     // EDNS0 expire
	EDNS0COOKIE       = 0xa     // EDNS0 Cookie
	EDNS0TCPKEEPALIVE = 0xb     // EDNS0 tcp keep alive (See RFC 7828)
	EDNS0PADDING      = 0xc     // EDNS0 padding (See RFC 7830)
	EDNS0LOCALSTART   = 0xFDE9  // Beginning of range reserved for local/experimental use (See RFC 6891)
	EDNS0LOCALEND     = 0xFFFE  // End of range reserved for local/experimental use (See RFC 6891)
	_DO               = 1 << 15 // DNSSEC OK
)

// OPT is the EDNS0 RR appended to messages to convey extra (meta) information.
// See RFC 6891.
type OPT struct {
	Hdr    RR_Header
	Option []EDNS0 `dns:"opt"`
}

// Header
func (o *OPT) Header() *RR_Header {
	return &o.Hdr
}

// PackData
func (o *OPT) PackData(buff *bytes.Buffer) (int, error) {
	var dataCommon EDNSDataCommon
	oldLen := buff.Len()
	for _, v := range o.Option {
		dataBytes, err := v.PackData()
		if err != nil {
			return 0, err
		}
		dataCommon.OptionCode = v.Option()
		dataCommon.OptionLength = uint16(len(dataBytes))
		err = dataCommon.Pack(buff)
		if err != nil {
			return 0, err
		}
		_, err = buff.Write(dataBytes)
		if err != nil {
			return 0, err
		}
	}
	return buff.Len() - oldLen, nil
}

// UnPackData
func (o *OPT) UnPackData(msg []byte, off int) (int, error) {
	var err error
	var dataCommon EDNSDataCommon
	size := 0
	for {
		off, err = dataCommon.Unpack(msg, off)
		if err != nil {
			return off, err
		}
		size += 4
		// just use base
		base := new(BaseEDNSData)
		err = base.UnpackData(msg[off : off+int(dataCommon.OptionLength)])
		if err != nil {
			return off, err
		}
		o.Option = append(o.Option, base)
		size += len(base.OptionData)
		off += len(base.OptionData)
		if size == int(o.Hdr.Rdlength) {
			break
		}
		if size > int(o.Hdr.Rdlength) {
			return 0, errors.New(fmt.Sprintf("OPT unpack size > int(o.Hdr.Rdlength"))
		}
	}
	return off, nil
}

// GetData
func (o *OPT) GetData() []byte {
	return nil
}

// EDNS0 defines an EDNS0 Option. An OPT RR can have multiple options appended to it.
type EDNS0 interface {
	// Option returns the option code for the option.
	Option() uint16
	// pack returns the bytes of the option data.
	PackData() ([]byte, error)
	// unpack sets the data as found in the buffer. Is also sets
	// the length of the slice as the length of the option data.
	UnpackData([]byte) error
	// String returns the string representation of the option.
	// String() string
	// copy returns a deep-copy of the option.
	// copy() EDNS0
}

// Base EDNS data common
type EDNSDataCommon struct {
	OptionCode   uint16
	OptionLength uint16
}

// Option
func (d *EDNSDataCommon) Option() uint16 {
	return 0
}

// Pack
func (d *EDNSDataCommon) Pack(buff *bytes.Buffer) error {
	err := packUint16(d.OptionCode, buff)
	if err != nil {
		return err
	}
	err = packUint16(d.OptionLength, buff)
	if err != nil {
		return err
	}
	return nil
}

// Unpack
func (d *EDNSDataCommon) Unpack(msg []byte, offset int) (int, error) {
	var err error
	d.OptionCode, offset, err = unpackUint16(msg, offset)
	if err != nil {
		return offset, err
	}
	d.OptionLength, offset, err = unpackUint16(msg, offset)
	if err != nil {
		return offset, err
	}
	return offset, nil
}

// Base edns data
type BaseEDNSData struct {
	Code       uint16
	OptionData []byte
}

// PackData
func (b *BaseEDNSData) PackData() ([]byte, error) {
	return b.OptionData, nil
}

// UnpackData
func (b *BaseEDNSData) UnpackData(data []byte) error {
	b.OptionData = data
	return nil
}

// Option() uint16
func (b *BaseEDNSData) Option() uint16 {
	return 0
}
