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

package dns

import "net"

// NewARR 创建一个A RR
func NewARR(name string, ip net.IP, ttl uint32) RR {
	rr := A{
		Hdr: RR_Header{
			Name:     name,
			Rrtype:   TypeA,
			Class:    ClassINET,
			Ttl:      ttl,
			Rdlength: 4,
		},
		A: ip,
	}
	return &rr
}

// NewAAAARR 创建一个AAAA RR
func NewAAAARR(name string, ip net.IP, ttl uint32) RR {
	rr := AAAA{
		Hdr: RR_Header{
			Name:     name,
			Rrtype:   TypeAAAA,
			Class:    ClassINET,
			Ttl:      ttl,
			Rdlength: 16,
		},
		AAAA: ip,
	}
	return &rr
}
