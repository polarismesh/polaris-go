/**
 * Tencent is pleased to support the open source community by making Polaris available.
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
	"fmt"
	"strings"
)

const (
	ArgumentTypeCustom = iota
	ArgumentTypeMethod
	ArgumentTypeHeader
	ArgumentTypeQuery
	ArgumentTypeCallerService
	ArgumentTypeCallerIP
	ArgumentTypePath
	ArgumentTypeCookie
)

var argumentTypeToName = map[int]string{
	ArgumentTypeCustom:        "CUSTOM",
	ArgumentTypeMethod:        "METHOD",
	ArgumentTypeHeader:        "HEADER",
	ArgumentTypeQuery:         "QUERY",
	ArgumentTypeCallerService: "CALLER_SERVICE",
	ArgumentTypeCallerIP:      "CALLER_IP",
	ArgumentTypePath:          "PATH",
	ArgumentTypeCookie:        "COOKIE",
}

const (
	LabelKeyMethod        = "$method"
	LabelKeyHeader        = "$header."
	LabelKeyQuery         = "$query."
	LabelKeyCallerService = "$caller_service."
	LabelKeyCallerIp      = "$caller_ip"
	LabelKeyPath          = "$path"
	LabelKeyCookie        = "$cookie."
)

// Argument 限流/路由参数
type Argument struct {
	argumentType int

	key string

	value string
}

func (a Argument) ArgumentType() int {
	return a.argumentType
}

func (a Argument) Key() string {
	return a.key
}

func (a Argument) Value() string {
	return a.value
}

func (a Argument) String() string {
	return fmt.Sprintf("%s:%s:%s", argumentTypeToName[a.argumentType], a.key, a.value)
}

func BuildCustomArgument(key string, value string) Argument {
	return Argument{
		argumentType: ArgumentTypeCustom,
		key:          key,
		value:        value,
	}
}

func BuildMethodArgument(method string) Argument {
	return Argument{
		argumentType: ArgumentTypeMethod,
		value:        method,
	}
}

func BuildHeaderArgument(key string, value string) Argument {
	return Argument{
		argumentType: ArgumentTypeHeader,
		key:          key,
		value:        value,
	}
}

func BuildQueryArgument(key string, value string) Argument {
	return Argument{
		argumentType: ArgumentTypeQuery,
		key:          key,
		value:        value,
	}
}

func BuildCallerServiceArgument(namespace string, service string) Argument {
	return Argument{
		argumentType: ArgumentTypeCallerService,
		key:          namespace,
		value:        service,
	}
}

func BuildCallerIPArgument(callerIP string) Argument {
	return Argument{
		argumentType: ArgumentTypeCallerIP,
		value:        callerIP,
	}
}

func BuildPathArgument(path string) Argument {
	return Argument{
		argumentType: ArgumentTypePath,
		value:        path,
	}
}

func BuildCookieArgument(key, value string) Argument {
	return Argument{
		argumentType: ArgumentTypeCookie,
		key:          key,
		value:        value,
	}
}

func BuildArgumentFromLabel(labelKey string, labelValue string) Argument {
	if labelKey == LabelKeyMethod {
		return BuildMethodArgument(labelValue)
	}
	if labelKey == LabelKeyCallerIp {
		return BuildCallerIPArgument(labelValue)
	}
	if labelKey == LabelKeyPath {
		return BuildPathArgument(labelValue)
	}
	if strings.HasPrefix(labelKey, LabelKeyHeader) {
		return BuildHeaderArgument(labelKey[len(LabelKeyHeader):], labelValue)
	}
	if strings.HasPrefix(labelKey, LabelKeyQuery) {
		return BuildQueryArgument(labelKey[len(LabelKeyQuery):], labelValue)
	}
	if strings.HasPrefix(labelKey, LabelKeyCallerService) {
		return BuildCallerServiceArgument(labelKey[len(LabelKeyCallerService):], labelValue)
	}
	if strings.HasPrefix(labelKey, LabelKeyCookie) {
		return BuildCookieArgument(labelKey[len(LabelKeyCookie):], labelValue)
	}
	return BuildCustomArgument(labelKey, labelValue)
}

func (a Argument) ToLabels(labels map[string]string) {
	switch a.argumentType {
	case ArgumentTypeMethod:
		labels[LabelKeyMethod] = a.value
	case ArgumentTypeCallerIP:
		labels[LabelKeyCallerIp] = a.value
	case ArgumentTypeHeader:
		labels[LabelKeyHeader+a.key] = a.value
	case ArgumentTypeQuery:
		labels[LabelKeyQuery+a.key] = a.value
	case ArgumentTypeCallerService:
		labels[LabelKeyCallerService+a.key] = a.value
	case ArgumentTypeCustom:
		labels[a.key] = a.value
	case ArgumentTypePath:
		labels[LabelKeyPath] = a.value
	case ArgumentTypeCookie:
		labels[LabelKeyCookie+a.key] = a.value
	}
}
