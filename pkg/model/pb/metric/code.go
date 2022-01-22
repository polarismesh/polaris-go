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

package metric

import (
	"github.com/golang/protobuf/ptypes/wrappers"
)

type Code uint32

const (
	ExecuteSuccess      Code = 200000
	ParseException           = 400001
	InvalidRatelimitKey      = 400101
	InvalidServiceName       = 400102
	InvalidNamespace         = 400103
	InvalidTotalLimit        = 400104
	InvalidUsedLimit         = 400105
	InvalidTimestamp         = 400106
	NotFoundLimiter          = 404001
	CreateLimiterError       = 500001
	AcquireQuotaError        = 500002
)

// code -> apiCode
var WrapperCode = map[Code]*wrappers.UInt32Value{
	ExecuteSuccess:      {Value: uint32(ExecuteSuccess)},
	ParseException:      {Value: uint32(ParseException)},
	InvalidRatelimitKey: {Value: uint32(InvalidRatelimitKey)},
	InvalidServiceName:  {Value: uint32(InvalidServiceName)},
	InvalidNamespace:    {Value: uint32(InvalidNamespace)},
	InvalidTotalLimit:   {Value: uint32(InvalidTotalLimit)},
	InvalidUsedLimit:    {Value: uint32(InvalidUsedLimit)},
	InvalidTimestamp:    {Value: uint32(InvalidTimestamp)},
	NotFoundLimiter:     {Value: uint32(NotFoundLimiter)},
	CreateLimiterError:  {Value: uint32(CreateLimiterError)},
	AcquireQuotaError:   {Value: uint32(AcquireQuotaError)},
}

// code -> info
var Code2Info = map[Code]*wrappers.StringValue{
	ExecuteSuccess:      {Value: "execute success"},
	ParseException:      {Value: "parse request body exception"},
	InvalidRatelimitKey: {Value: "invalid ratelimiting key"},
	InvalidServiceName:  {Value: "invalid service name"},
	InvalidNamespace:    {Value: "invalid namespace"},
	InvalidTotalLimit:   {Value: "invalid total limit"},
	InvalidUsedLimit:    {Value: "invalid used limit"},
	InvalidTimestamp:    {Value: "invalid timestamp"},
	NotFoundLimiter:     {Value: "not found the key limiter, initialize firstly"},
	CreateLimiterError:  {Value: "create ratelimiter error"},
	AcquireQuotaError:   {Value: "acquire quota error"},
}

// Code2HTTPStatus 转为http status
func Code2HTTPStatus(code Code) int {
	return int(code / 1000)
}
