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

const (
	// 最大标准域名长度
	maxDomainNameWireOctets = 255 // See RFC 1035 section 2.3.4

	// （Question RR中）name字段的最大长度
	maxDomainNamePresentationLength = 61*4 + 1 + 63*4 + 1 + 63*4 + 1 + 63*4 + 1
)

const (
	// year68 is the year represented by "68", which is used in RFC3597.
	year68 = 1 << 31 // For RFC1982 (Serial Arithmetic) calculations in 32 bits.
	// defaultTTL is the default TTL value to use when none is given in the message.
	defaultTTL = 3600 // Default internal TTL.

	// MinMsgSize UDP最小包长度
	MinMsgSize = 512
	// MaxMsgSize UDP最大包长度
	MaxMsgSize = 65535
)

// Errors defined in this package.
var (
	// ErrBuf ErrAuth indicates an error in the TSIG authentication.
	ErrBuf error = &Error{err: "buffer size too small"}
	// ErrConnEmpty indicates a connection is being used before it is initialized.
	ErrConnEmpty error = &Error{err: "conn has no connection"}
	// ErrExtendedRcode ...
	ErrExtendedRcode error = &Error{err: "bad extended rcode"}
	// ErrId indicates there is a mismatch with the message's ID.
	ErrId         error = &Error{err: "id mismatch"}
	ErrLongDomain error = &Error{err: fmt.Sprintf("domain name exceeded %d wire-format octets",
		maxDomainNameWireOctets)}
	ErrRCode error = &Error{err: "bad rcode"}
	ErrRData error = &Error{err: "bad rdata"}
	ErrRRSet error = &Error{err: "bad rrset"}
)

// MsgHdr dns协议头
type MsgHdr struct {
	// dns session id
	Id uint16
	// 是否为应答
	Response bool
	// 操作code
	Opcode int
	// 是否为权威应答
	Authoritative bool
	// UDP应答是否被截断
	Truncated bool
	// 请求是否要求递归查询
	RecursionDesired bool
	// 递归查询是否启用
	RecursionAvailable bool
	Zero               bool
	AuthenticatedData  bool
	CheckingDisabled   bool
	// 返回code
	Rcode int
	// question数量， answer RR数量， 权威信息 RR数量， 额外信息 RR数量
	Qdcount, Ancount, Nscount, Arcount uint16
}

// 返回 header的可读字符串，便于调式
func (h *MsgHdr) String() string {
	if h == nil {
		return "<nil> MsgHdr"
	}
	s := ";; opcode: " + OpcodeToString[h.Opcode]
	s += ", status: " + RcodeToString[h.Rcode]
	s += ", id: " + strconv.Itoa(int(h.Id)) + "\n"

	s += ";; flags:"
	if h.Response {
		s += " qr"
	}
	if h.Authoritative {
		s += " aa"
	}
	if h.Truncated {
		s += " tc"
	}
	if h.RecursionDesired {
		s += " rd"
	}
	if h.RecursionAvailable {
		s += " ra"
	}
	if h.Zero { // Hmm
		s += " z"
	}
	if h.AuthenticatedData {
		s += " ad"
	}
	if h.CheckingDisabled {
		s += " cd"
	}

	s += ";"
	return s
}

// Unpack header反序列化
func (h *MsgHdr) Unpack(msg []byte) (int, error) {
	var off int
	var err error
	h.Id, off, err = unpackUint16(msg, 0)
	if err != nil {
		return off, err
	}
	var bits uint16
	bits, off, err = unpackUint16(msg, off)
	if err != nil {
		return off, err
	}
	h.Response = bits&QR_ != 0
	h.Opcode = int(bits>>11) & 0xF
	h.Authoritative = bits&AA_ != 0
	h.Truncated = bits&TC_ != 0
	h.RecursionDesired = bits&RD_ != 0
	h.RecursionAvailable = bits&RA_ != 0
	h.Zero = bits&Z_ != 0 // _Z covers the zero bit, which should be zero; not sure why we set it to the opposite.
	h.AuthenticatedData = bits&AD_ != 0
	h.CheckingDisabled = bits&CD_ != 0
	h.Rcode = int(bits & 0xF)

	h.Qdcount, off, err = unpackUint16(msg, off)
	if err != nil {
		return off, err
	}
	h.Ancount, off, err = unpackUint16(msg, off)
	if err != nil {
		return off, err
	}
	h.Nscount, off, err = unpackUint16(msg, off)
	if err != nil {
		return off, err
	}
	h.Arcount, off, err = unpackUint16(msg, off)
	if err != nil {
		return off, err
	}
	return off, nil
}

// Pack header序列化
func (h *MsgHdr) Pack(buff *bytes.Buffer) (int, error) {
	oldLen := buff.Len()
	err := packUint16(h.Id, buff)
	if err != nil {
		return 0, err
	}
	var bits uint16
	bits = uint16(h.Opcode)<<11 | uint16(h.Rcode&0xF)
	if h.Response {
		bits |= QR_
	}
	if h.Authoritative {
		bits |= AA_
	}
	if h.Truncated {
		bits |= TC_
	}
	if h.RecursionDesired {
		bits |= RD_
	}
	if h.RecursionAvailable {
		bits |= RA_
	}
	if h.Zero {
		bits |= Z_
	}
	if h.AuthenticatedData {
		bits |= AD_
	}
	if h.CheckingDisabled {
		bits |= CD_
	}
	err = packUint16(bits, buff)
	if err != nil {
		return 0, err
	}
	err = packUint16(h.Qdcount, buff)
	if err != nil {
		return 0, err
	}
	err = packUint16(h.Ancount, buff)
	if err != nil {
		return 0, err
	}
	err = packUint16(h.Nscount, buff)
	if err != nil {
		return 0, err
	}
	err = packUint16(h.Arcount, buff)
	if err != nil {
		return 0, err
	}
	length := buff.Len() - oldLen
	return length, nil
}

// Copy header 深拷贝函数
func (h *MsgHdr) Copy(dst *MsgHdr) error {
	dst.Id = h.Id
	dst.Response = h.Response
	dst.Opcode = h.Opcode
	dst.Authoritative = h.Authoritative
	dst.Truncated = h.Truncated
	dst.RecursionDesired = h.RecursionDesired
	dst.RecursionAvailable = h.RecursionAvailable
	dst.Zero = h.Zero
	dst.AuthenticatedData = h.AuthenticatedData
	dst.CheckingDisabled = h.CheckingDisabled
	dst.Rcode = h.Rcode
	dst.Qdcount = h.Qdcount
	dst.Ancount = h.Ancount
	dst.Nscount = h.Nscount
	dst.Arcount = h.Arcount
	return nil
}

// SetDefaultValue header设置默认值
func (h *MsgHdr) SetDefaultValue(id uint16, opCode int) {
	h.Id = id
	h.Response = false
	h.Opcode = opCode
	h.Authoritative = true
	h.Truncated = false
	h.RecursionDesired = false
	h.RecursionAvailable = false
	h.Zero = false
	h.AuthenticatedData = false
	h.CheckingDisabled = false
	h.Rcode = 0
	h.Qdcount = 0
	h.Ancount = 0
	h.Nscount = 0
	h.Arcount = 0
}

// Msg 完整的DNS协议
type Msg struct {
	MsgHdr
	// Compress bool       `json:"-"` // If true, the message will be compressed when converted to wire format.
	Question []Question // Holds the RR(s) of the question section.
	Answer   []RR       // Holds the RR(s) of the answer section.
	Ns       []RR       // Holds the RR(s) of the authority section.
	Extra    []RR       // Holds the RR(s) of the additional section.
}

// Pack 序列化
func (dns *Msg) Pack() (*bytes.Buffer, error) {
	if dns.Opcode == OpcodeQuery {
		buf := new(bytes.Buffer)
		err := dns.dnsPack(buf)
		if err != nil {
			return nil, err
		}
		return buf, nil
	}
	buf := new(bytes.Buffer)
	err := dns.polarisStreamPack(buf)
	if err != nil {
		return nil, err
	}
	return buf, nil
}

// polaris dns序列化 （answer RR 为 stream RR，用于4层分包, 尚未做严格校验）
func (dns *Msg) polarisStreamPack(buff *bytes.Buffer) error {
	if buff == nil {
		return errors.New("pack buff is nil")
	}
	_, err := dns.MsgHdr.Pack(buff)
	if err != nil {
		return err
	}

	for _, r := range dns.Question {
		_, err = r.pack(buff)
		if err != nil {
			return err
		}
	}
	for _, r := range dns.Answer {
		_, err = PackRR(r, buff)
		if err != nil {
			return err
		}
	}
	for _, r := range dns.Ns {
		_, err = PackRR(r, buff)
		if err != nil {
			return err
		}
	}
	for _, r := range dns.Extra {
		_, err = PackRR(r, buff)
		if err != nil {
			return err
		}
	}
	return nil
}

// 标准的dns序列化
func (dns *Msg) dnsPack(buff *bytes.Buffer) error {
	err := dns.polarisStreamPack(buff)
	if err != nil {
		return err
	}
	if buff.Len() > MinMsgSize {
		buff.Reset()
		dns.MsgHdr.Truncated = true
		if _, err := dns.MsgHdr.Pack(buff); err != nil {
			return err
		}
	}
	return nil
}

// UnpackBody 反序列化body
func (dns *Msg) UnpackBody(msg []byte, off int) (err error) {
	// If we are at the end of the message we should return *just* the
	// header. This can still be useful to the caller. 9.9.9.9 sends these
	// when responding with REFUSED for instance.
	if off == len(msg) {
		// reset sections before returning
		dns.Question, dns.Answer, dns.Ns, dns.Extra = nil, nil, nil, nil
		return nil
	}
	dns.Question = nil
	for i := 0; i < int(dns.MsgHdr.Qdcount); i++ {
		off1 := off
		var q Question
		q, off, err = unpackQuestion(msg, off, dns.MsgHdr.Opcode)
		if err != nil {
			return err
		}
		if off1 == off { // Offset does not increase anymore, dh.Qdcount is a lie!
			dns.MsgHdr.Qdcount = uint16(i)
			break
		}
		dns.Question = append(dns.Question, q)
	}

	dns.Answer, off, err = unpackRRslice(int(dns.MsgHdr.Ancount), msg, off)
	if err != nil {
		return err
	}

	dns.Ns, off, err = unpackRRslice(int(dns.MsgHdr.Nscount), msg, off)
	if err != nil {
		return err
	}
	dns.Extra, off, err = unpackRRslice(int(dns.MsgHdr.Arcount), msg, off)
	if err != nil {
		return err
	}
	if off != len(msg) {
		// use PackOpt to let people tell how detailed the error reporting should be?
		// println("dns: extra bytes in dns packet", off, "<", len(msg))
	}
	return err
}

// Unpack dns反序列化
func (dns *Msg) Unpack(msg []byte) (err error) {
	off, err := dns.MsgHdr.Unpack(msg)
	if err != nil {
		return err
	}
	return dns.UnpackBody(msg, off)
}

// SetReply 设置返回
func (dns *Msg) SetReply(request *Msg) {
	dns.Id = request.Id
	dns.Response = true
	dns.Opcode = request.Opcode
	if dns.Opcode == OpcodeQuery || true {
		dns.RecursionDesired = request.RecursionDesired // Copy rd bit
		dns.CheckingDisabled = request.CheckingDisabled // Copy cd bit
	}
	dns.Rcode = RcodeSuccess
	if len(request.Question) > 0 {
		dns.Question = make([]Question, 1)
		dns.Question[0] = request.Question[0]
		dns.Qdcount = 1
	}
}

// SetReplyWithoutQuestions 设置返回，不拷贝question
func (dns *Msg) SetReplyWithoutQuestions(request *Msg) {
	dns.Id = request.Id
	dns.Response = true
	dns.Opcode = request.Opcode
	if dns.Opcode == OpcodeQuery {
		dns.RecursionDesired = request.RecursionDesired // Copy rd bit
		dns.CheckingDisabled = request.CheckingDisabled // Copy cd bit
	}
	dns.Rcode = RcodeSuccess
}

// SetDNSQuestion Set Question
func (dns *Msg) SetDNSQuestion(z string, t uint16) *Msg {
	dns.RecursionDesired = true
	dns.Question = make([]Question, 1)
	dns.Question[0] = &DNSQuestion{z, t, ClassINET}
	dns.MsgHdr.Qdcount++
	return dns
}

// GetPackControlRR 获取包控制RR
func (dns *Msg) GetPackControlRR() *PackageCtrlRR {
	if dns.Arcount == 0 {
		return nil
	}
	for _, v := range dns.Extra {
		if v.Header().Rrtype == TypePackCtrl {
			return v.(*PackageCtrlRR)
		}
	}
	return nil
}

// GetDetailErrRR 获取详细错误RR（Polaris使用）
func (dns *Msg) GetDetailErrRR() *DetailErrInfoRR {
	if dns.Arcount == 0 {
		return nil
	}
	for _, v := range dns.Extra {
		if v.Header().Rrtype == TypePolarisDetailErrInfo {
			return v.(*DetailErrInfoRR)
		}
	}
	return nil
}

/*
// dns 7层分包，暂时没有用途
func (dns *Msg) polarisPack() ([]*bytes.Buffer, error)  {
	var err error
	var RRBufArr []*bytes.Buffer
	var totalRR []RR
	//lenAnswer := len(dns.Answer)
	//lenNs := len(dns.Ns)
	totalRR = append(totalRR, dns.Answer...)
	totalRR = append(totalRR, dns.Ns...)

	var RRCountRecord []uint16
	packControlRRSize := 8 + 10 + 4
	buf := new(bytes.Buffer)
	RRBufArr = append(RRBufArr, buf)
	idx := 0
	RRCountRecord = append(RRCountRecord, 0)
	for i:=0; i<len(totalRR); {
		lastLen := RRBufArr[idx].Len()
		_, err = PackRR(totalRR[i], RRBufArr[idx])
		if err != nil {
			return nil, err
		}
		RRCountRecord[idx] += 1
		if RRBufArr[idx].Len() > MaxMsgSize - HeaderSize - packControlRRSize {
			RRCountRecord[idx] -= 1
			RRBufArr[idx].Truncate(lastLen)
			buf := new(bytes.Buffer)
			RRBufArr = append(RRBufArr, buf)
			RRCountRecord = append(RRCountRecord, 0)
			idx += 1
		} else {
			i += 1
		}
	}

	_ = err
	totalPackCount := idx+1
	var retBuffArr []*bytes.Buffer
	for i:=0; i<totalPackCount; i++ {
		h := new(MsgHdr)
		_ = dns.MsgHdr.Copy(h)
		h.Ancount = RRCountRecord[i]
		ctrlRR := PackageCtrlRR{
			Hdr: RRHeader{
				Name:     PackCtrlRRName,
				Rrtype:   TypePackCtrl,
				Class:    1101,
				Ttl:      0,
				Rdlength: 8,
			},
			TotalCount:   uint16(totalPackCount),
			PackageIndex: uint16(i),
		}
		h.Arcount = 1
		buf := new(bytes.Buffer)
		_, err := h.Pack(buf)
		if err != nil {
			return nil, err
		}

		_, err = buf.Write(RRBufArr[i].Bytes())
		if err != nil {
			return nil, err
		}

		_, err = PackRR(&ctrlRR, buf)
		if err != nil {
			return nil, err
		}
		retBuffArr = append(retBuffArr, buf)
	}

	return retBuffArr, nil
}

// 另一种序列化方式，暂时没有用途
func (dns *Msg) polarisDispersedPack() ([]*bytes.Buffer, error)  {
	var buffArr []*bytes.Buffer
	if dns.Opcode == OpcodeQuery {
		buff := new(bytes.Buffer)
		err := dns.dnsPack(buff)
		if err != nil {
			return nil, err
		}
		buffArr = append(buffArr, buff)
		return buffArr, nil
	} else {
		return dns.polarisPack()
	}
	return nil, nil
}
*/
