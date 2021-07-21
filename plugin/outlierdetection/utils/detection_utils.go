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

package utils

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strconv"

	"github.com/polarismesh/polaris-go/pkg/model"
)

// GetAddressByInstance 根据model.Instance得到address(ip:port格式)
func GetAddressByInstance(ins model.Instance) (address string, err error) {
	if ins == nil {
		return "", fmt.Errorf("Nil Instance")
	}
	ip := ins.GetHost()
	if ip == "" {
		return "", fmt.Errorf("Instance Host Should Not Be Empty")
	}
	port := ins.GetPort()
	if port == 0 {
		return "", fmt.Errorf("Instance Port Illegal")
	}
	b := bytes.Buffer{}
	b.WriteString(ip)
	b.WriteString(":")
	b.WriteString(strconv.FormatUint(uint64(port), 10))
	return b.String(), nil
}

//将配置的发送接收package转化为[]byte
func ConvertPackageConf(sendPackageConf string, checkPackageConf string) ([]byte, []byte) {
	var SendPackageBytes, CheckPackageBytes []byte
	if sendPackageConf != "" && checkPackageConf != "" {
		SendPackageBytes = make([]byte, 4)
		CheckPackageBytes = make([]byte, 4)
		send, _ := strconv.ParseUint(sendPackageConf, 0, 32)
		check, _ := strconv.ParseUint(checkPackageConf, 0, 32)
		binary.BigEndian.PutUint32(SendPackageBytes, uint32(send))
		binary.BigEndian.PutUint32(CheckPackageBytes, uint32(check))
	}
	return SendPackageBytes, CheckPackageBytes
}
