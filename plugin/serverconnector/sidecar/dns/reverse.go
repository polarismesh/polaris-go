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
package dns

// StringToType is the reverse of TypeToString, needed for string parsing.
var StringToType = reverseInt16(TypeToString)

// StringToClass is the reverse of ClassToString, needed for string parsing.
var StringToClass = reverseInt16(ClassToString)

// StringToOpcode is a map of opcodes to strings.
var StringToOpcode = reverseInt(OpcodeToString)

// StringToRcode is a map of rcodes to strings.
var StringToRcode = reverseInt(RcodeToString)

func init() {
	// Preserve previous NOTIMP typo, see github.com/miekg/dns/issues/733.
	StringToRcode["NOTIMPL"] = RcodeNotImplemented
}

// Reverse a map
func reverseInt16(m map[uint16]string) map[string]uint16 {
	n := make(map[string]uint16, len(m))
	for u, s := range m {
		n[s] = u
	}
	return n
}

// Reverse a map
func reverseInt(m map[int]string) map[string]int {
	n := make(map[string]int, len(m))
	for u, s := range m {
		n[s] = u
	}
	return n
}
