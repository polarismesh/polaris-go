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

package model

import (
	"syscall"
	"time"
)

// CurrentMicrosecond obtains the current microsecond, use syscall for better performance
func CurrentNanosecond() int64 {
	return CurrentMicrosecond() * 1e3
}

//获取微秒时间
func CurrentMicrosecond() int64 {
	var tv syscall.Timeval
	if err := syscall.Gettimeofday(&tv); err != nil {
		return time.Now().UnixNano() / 1e3
	}
	return int64(tv.Sec)*1e6 + int64(tv.Usec)
}

//获取微秒时间
func CurrentMillisecond() int64 {
	var tv syscall.Timeval
	if err := syscall.Gettimeofday(&tv); err != nil {
		return time.Now().UnixNano() / 1e6
	}
	return int64(tv.Sec)*1e3 + int64(tv.Usec)/1e3
}

//格式化时间戳
func TimestampMsToUtcIso8601(timestamp int64) string {
	t := time.Unix(0, timestamp*1000000+1)
	s := t.Format(time.RFC3339Nano)
	if len(s) != 35 { // 2019-05-17T15:07:08.941000001+08:00
		return "1970-01-01T00:00:00.000+0800"
	}
	return s[:23] + s[29:32] + s[33:]
}
