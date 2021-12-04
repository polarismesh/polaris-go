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
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/polarismesh/polaris-go/pkg/model/pb"
)

// Test_GetAddressByInstance 根据Instance获取IP：port
func Test_GetAddressByInstance(t *testing.T) {
	Convey("空instance，应该返回错误", t, func() {
		address := GetAddressByInstance(nil)
		So(address, ShouldBeEmpty)
	})

	Convey("空host，应该返回错误", t, func() {
		ins := pb.NewInstanceInProto(nil, nil, nil)
		p := gomonkey.ApplyMethod(reflect.TypeOf(ins), "GetHost", func(insp *pb.InstanceInProto) string {
			return ""
		})
		defer p.Reset()
		address := GetAddressByInstance(ins)
		So(address, ShouldBeEmpty)
	})

	Convey("空port，应该返回错误", t, func() {
		ins := pb.NewInstanceInProto(nil, nil, nil)
		p := gomonkey.ApplyMethod(reflect.TypeOf(ins), "GetHost", func(insp *pb.InstanceInProto) string {
			return "127.0.0.1"
		})
		defer p.Reset()
		p1 := gomonkey.ApplyMethod(reflect.TypeOf(ins), "GetPort", func(insp *pb.InstanceInProto) uint32 {
			return 0
		})
		defer p1.Reset()
		address := GetAddressByInstance(ins)
		So(address, ShouldBeEmpty)
	})

	Convey("正确的ip和port，返回正确address", t, func() {
		ins := pb.NewInstanceInProto(nil, nil, nil)
		p := gomonkey.ApplyMethod(reflect.TypeOf(ins), "GetHost", func(insp *pb.InstanceInProto) string {
			return "127.0.0.1"
		})
		defer p.Reset()
		p1 := gomonkey.ApplyMethod(reflect.TypeOf(ins), "GetPort", func(insp *pb.InstanceInProto) uint32 {
			return 8080
		})
		defer p1.Reset()
		address := GetAddressByInstance(ins)
		So(address, ShouldNotBeEmpty)
		So(address, ShouldEqual, "127.0.0.1:8080")
	})
}
