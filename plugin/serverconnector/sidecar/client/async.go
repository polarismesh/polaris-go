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

package client

import (
	"errors"
	"github.com/polarismesh/polaris-go/plugin/serverconnector/sidecar/dns"
	"net"
	"sync"
	"time"
)

// msg buffer 用于合并包
type MsgBuffer struct {
	ID          uint16
	ExpectedNum uint16
	ReceiveNum  uint16

	MsgArr    []*dns.Msg
	BeginTime int64
}

// 异步Conn
type AsyncConn struct {
	ConnBase

	lock      sync.Mutex
	MsgBufMap sync.Map

	WriteChan chan *dns.Msg
	writStop  chan int

	ReadChan chan *RspData
	readStop chan int
}

// init
func (co *AsyncConn) Init() error {
	var err error
	localIp := []byte{uint8(127), 0, 0, uint8(1)}
	srcAddr := &net.UDPAddr{IP: net.IPv4zero, Port: 0}
	dstAddr := &net.UDPAddr{IP: localIp, Port: 12345}

	co.UdpConn, err = net.DialUDP("udp", srcAddr, dstAddr)
	if err != nil {
		return err
	}
	co.WriteChan = make(chan *dns.Msg, 100)
	co.writStop = make(chan int)
	co.ReadChan = make(chan *RspData, 100)
	co.readStop = make(chan int)
	go co.sendUdpPack()
	go co.ReceiveUdpPack()
	return nil
}

// 清理资源
func (co *AsyncConn) Close() {
	co.writStop <- 1
	co.readStop <- 1
	co.ConnBase.Close()
}

// 发送消息给chan
func (c *AsyncConn) pushToWriteChan(msg *dns.Msg) error {
	select {
	case c.WriteChan <- msg:
		return nil
	default:
		return errors.New("channel full")
	}
}

// 发送消息接口
func (c *AsyncConn) pushToWriteChanWrapper(msg *dns.Msg) error {
	maxTryTimes := 10
	for i := 0; i < maxTryTimes; i++ {
		err := c.pushToWriteChan(msg)
		if err != nil {
			time.Sleep(time.Millisecond * 10)
		} else {
			return nil
		}
	}
	return errors.New("channel full try 10 times")
}

// 发送UDP包
func (c *AsyncConn) sendUdpPack() {
	for {
		select {
		case msg := <-c.WriteChan:
			c.WriteMsg(msg)
		default:
			time.Sleep(time.Millisecond * 10)
		}
	}
}

// push read channel
func (c *AsyncConn) pushToReadChan(msg *RspData) error {
	select {
	case c.ReadChan <- msg:
		return nil
	default:
		return errors.New("channel full")
	}
}

// pushToReadChanWrapper
func (c *AsyncConn) pushToReadChanWrapper(rspData *RspData) error {
	maxTryTimes := 10
	for i := 0; i < maxTryTimes; i++ {
		err := c.pushToReadChan(rspData)
		if err != nil {
			time.Sleep(time.Millisecond * 10)
		} else {
			return nil
		}
	}
	return errors.New("channel full try 10 times")
}

// 异步收包
func (c *AsyncConn) ReceiveUdpPack() {
	for {
		msg, err := c.ReadMsg()
		if err != nil {
			continue
		}

		ctrlRR := msg.GetPackControlRR()
		if ctrlRR == nil {
			rspData := AssembleLogicRspData([]*dns.Msg{msg})
			_ = c.pushToReadChanWrapper(rspData)
			continue
		}

		v, ok := c.MsgBufMap.Load(msg.Id)
		msgBuf := v.(*MsgBuffer)
		if ok {
			msgBuf.MsgArr = append(msgBuf.MsgArr, msg)
			msgBuf.ReceiveNum++
			if msgBuf.ExpectedNum == msgBuf.ReceiveNum {
				msgBuf.MsgArr = append(msgBuf.MsgArr, msg)
				rspData := AssembleLogicRspData(msgBuf.MsgArr)
				_ = c.pushToReadChanWrapper(rspData)
				c.MsgBufMap.Delete(msg.Id)
			} else {
				c.MsgBufMap.Store(msg.Id, &msgBuf)
			}
		} else {
			msgBuf := MsgBuffer{
				ID:          msg.Id,
				BeginTime:   time.Now().Unix(),
				ExpectedNum: ctrlRR.TotalCount,
				ReceiveNum:  uint16(0),
			}
			msgBuf.MsgArr = make([]*dns.Msg, ctrlRR.TotalCount)
			msgBuf.MsgArr[ctrlRR.PackageIndex] = msg
			msgBuf.ReceiveNum++
			c.MsgBufMap.Store(msg.Id, &msgBuf)
		}
	}
}

// 组包逻辑
func AssembleLogicRspData(msgArr []*dns.Msg) *RspData {
	if len(msgArr) == 0 {
		return nil
	}
	rspData := RspData{
		OpCode: msgArr[0].Opcode,
		RCode:  msgArr[0].Rcode,
	}
	if msgArr[0].Rcode != 0 {
		rspData.DetailErrInfo = msgArr[0].GetDetailErrRR()
		return &rspData
	}
	for _, v := range msgArr {
		rspData.RRArr = append(rspData.RRArr, v.Answer...)
	}
	return &rspData
}
