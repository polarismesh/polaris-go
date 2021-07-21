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
	"github.com/polarismesh/polaris-go/pkg/model"
	"time"

	"github.com/polarismesh/polaris-go/plugin/serverconnector/sidecar/dns"
)

// 同步发送收取包
func (c *Connector) SyncExchange(m *dns.Msg) (*RspData, time.Duration, error) {
	var err error
	co := c.GetSyncConnFunc()
	err = co.Dial("127.0.0.1", 53)
	if err != nil {
		return nil, 0, errors.New("udp conn dial error")
	}

	t := time.Now()
	// write with the appropriate write timeout
	co.SetWriteDeadline(t.Add(c.getTimeoutForRequest(c.writeTimeout())))
	if err = co.WriteMsg(m); err != nil {
		return nil, 0, model.NewSDKError(model.ErrCodeNetworkError, err, err.Error())
	}

	co.SetReadDeadline(time.Now().Add(c.getTimeoutForRequest(c.readTimeout())))
	rspData, err := c.syncReadLogicMsg(m.Id, co)
	if err != nil {
		return nil, 0, model.NewSDKError(model.ErrCodeNetworkError, err, err.Error())
	}
	rtt := time.Since(t)
	return rspData, rtt, err
}

// 同步读取包
func (c *Connector) syncReadLogicMsg(msgId uint16, co Conn) (*RspData, error) {
	packBuff := MsgBuffer{}
	packBuff.ID = msgId
	packBuff.ReceiveNum = 0
	init := false

	timeNow := time.Now().UnixNano()
	maxLoopTime := time.Millisecond * 1000
	for {
		m, err := co.ReadMsg()
		if err != nil || m.Id != msgId {
			err = errors.New("invalid id")
			return nil, err
		}
		// 检查是否有控制包来确认是否需要组包
		ctrlRR := m.GetPackControlRR()
		if ctrlRR == nil {
			rspData := AssembleLogicRspData([]*dns.Msg{m})
			return rspData, nil
		}

		if !init {
			packBuff.MsgArr = make([]*dns.Msg, ctrlRR.TotalCount)
			packBuff.ExpectedNum = ctrlRR.TotalCount
			init = true
		}
		packBuff.MsgArr[ctrlRR.PackageIndex] = m
		packBuff.ReceiveNum++
		if packBuff.ReceiveNum == packBuff.ExpectedNum {
			rspData := AssembleLogicRspData(packBuff.MsgArr)
			return rspData, nil
		}
		timeLoop := time.Now().UnixNano()
		if timeLoop-timeNow > int64(maxLoopTime) {
			return nil, errors.New("timeout")
		}
	}
}
