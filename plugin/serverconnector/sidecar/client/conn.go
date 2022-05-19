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
	"errors"
	"net"
	"time"

	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/plugin/serverconnector/sidecar/dns"
)

// Conn conn interface
type Conn interface {
	Dial(dstIP string, port int) error
	Close()
	ReadMsg() (*dns.Msg, error)
	WriteMsg(m *dns.Msg) error
	SetWriteDeadline(t time.Time) error
	SetReadDeadline(t time.Time) error
}

// ConnBase 发送Conn
type ConnBase struct {
	UdpConn *net.UDPConn
	UDPSize uint16
}

// Dial UDP dial
func (co *ConnBase) Dial(dstIP string, port int) error {
	var err error
	srcAddr := &net.UDPAddr{IP: net.IPv4zero, Port: 0}
	dst := net.ParseIP(dstIP)
	dstAddr := &net.UDPAddr{IP: dst, Port: port}

	co.UdpConn, err = net.DialUDP("udp", srcAddr, dstAddr)
	if err != nil {
		return err
	}

	return nil
}

// Close 关闭
func (co *ConnBase) Close() {
	if co.UdpConn != nil {
		_ = co.UdpConn.Close()
	}
}

// ReadMsg 接收消息
func (co *ConnBase) ReadMsg() (*dns.Msg, error) {
	p, err := co.readUdpPack()
	if err != nil {
		return nil, err
	}
	m := new(dns.Msg)
	if err := m.Unpack(p); err != nil {
		return m, err
	}
	return m, err
}

// 读包
func (co *ConnBase) readUdpPack() ([]byte, error) {
	data := make([]byte, UDPSize)
	n, remoteAddr, err := co.UdpConn.ReadFromUDP(data)
	_ = remoteAddr
	if err != nil {
		return nil, err
	} else if n < dns.HeaderSize {
		return nil, errors.New("dns header invalid")
	}

	return data[:n], err
}

// WriteMsg 发包
func (co *ConnBase) WriteMsg(m *dns.Msg) (err error) {
	buf, err := m.Pack()
	if err != nil {
		log.GetBaseLogger().Warnf("dns msg pack error:%s", err.Error())
		return err
	}
	out := buf.Bytes()
	_, err = co.UdpConn.Write(out)
	return err
}

// SetWriteDeadline 设置写超时
func (co *ConnBase) SetWriteDeadline(t time.Time) error {
	if co.UdpConn != nil {
		return co.UdpConn.SetWriteDeadline(t)
	}
	return errors.New("udpConn is nil")
}

// SetReadDeadline 设置读超时
func (co *ConnBase) SetReadDeadline(t time.Time) error {
	if co.UdpConn != nil {
		return co.UdpConn.SetReadDeadline(t)
	}
	return errors.New("udpConn is nil")
}
