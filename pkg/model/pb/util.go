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

package pb

import (
	"github.com/golang/protobuf/ptypes"
	"github.com/golang/protobuf/ptypes/duration"
	"time"
)

// convertDuration converts to golang duration and logs errors
func ConvertDuration(d *duration.Duration) (time.Duration, error) {
	if d == nil {
		return 0, nil
	}
	return ptypes.Duration(d)
}

//pb时间段转毫秒
func ProtoDurationToMS(dur *duration.Duration) int64 {
	timeDuration, _ := ConvertDuration(dur)
	return int64(timeDuration / time.Millisecond)
}
