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

package utils

import (
	"fmt"
	"github.com/polarismesh/polaris-go/pkg/model"
)

// GetAddressByInstance 根据model.Instance得到address(ip:port格式)
func GetAddressByInstance(ins model.Instance) string {
	return fmt.Sprintf("%s:%d", ins.GetHost(), ins.GetPort())
}

// ConvertPackageConf 将配置的发送接收package转化为[]byte
//func ConvertPackageConf(sendPackageConf string, recvPackageConf []string) ([]byte, [][]byte) {
//	var sendPackageBytes []byte
//	if len(sendPackageConf) > 0 {
//		sendPackageBytes = make([]byte, 4)
//		send, _ := strconv.ParseUint(sendPackageConf, 0, 32)
//		binary.BigEndian.PutUint32(sendPackageBytes, uint32(send))
//	}
//	var recvPackageByteSlice [][]byte
//	if len(recvPackageConf) > 0 {
//		recvPackageByteSlice = make([][]byte, 0, len(recvPackageConf))
//		for _, recv := range recvPackageConf {
//			recvPackageBytes := make([]byte, 4)
//			recvInt, _ := strconv.ParseUint(recv, 0, 32)
//			binary.BigEndian.PutUint32(recvPackageBytes, uint32(recvInt))
//			recvPackageByteSlice = append(recvPackageByteSlice, recvPackageBytes)
//		}
//	}
//	return sendPackageBytes, recvPackageByteSlice
//}
