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

package ringhash

import (
	"fmt"
	"github.com/polarismesh/polaris-go/pkg/algorithm/search"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/loadbalancer"
	"github.com/polarismesh/polaris-go/plugin/loadbalancer/common"
	murmur32 "github.com/spaolacci/murmur3"
	"sort"
	"strconv"
	"strings"
)

//一致性hash环
type L5ContinuumSelector struct {
	model.SelectorBase
	svcClusters model.ServiceClusters
	ring        points
}

func IPToUInt32(ip string) uint32 {
	bits := strings.Split(ip, ".")

	b0, _ := strconv.Atoi(bits[0])
	b1, _ := strconv.Atoi(bits[1])
	b2, _ := strconv.Atoi(bits[2])
	b3, _ := strconv.Atoi(bits[3])

	var sum uint32

	sum += uint32(b0) << 24
	sum += uint32(b1) << 16
	sum += uint32(b2) << 8
	sum += uint32(b3)

	return sum
}

func compare(new, old model.Instance) bool {
	if new.GetWeight() > old.GetWeight() {
		return true
	}
	if new.GetWeight() == old.GetWeight() {
		newIP := IPToUInt32(new.GetHost())
		oldIP := IPToUInt32(old.GetHost())
		if newIP < oldIP {
			return true
		}
		if newIP == oldIP {
			return new.GetPort() < old.GetPort()
		}
	}
	return false
}

//创建hash环
func NewL5Continuum(
	instanceSet *model.InstanceSet, id int32) (*L5ContinuumSelector, error) {
	var continuum = &L5ContinuumSelector{
		svcClusters: instanceSet.GetServiceClusters(),
	}
	continuum.Id = id
	if instanceSet.Count() == 0 {
		return continuum, nil
	}
	ringLen := 0
	svcInstances := instanceSet.GetServiceClusters().GetServiceInstances()
	instances := svcInstances.GetInstances()
	instanceSlice := instanceSet.GetInstances()
	for _, instanceIdx := range instanceSlice {
		ringLen += instances[instanceIdx.Index].GetWeight()
	}
	continuum.ring = make(points, 0, ringLen)
	var hashValues = make(map[uint64]continuumPoint, ringLen)
	for _, instanceIdx := range instanceSlice {
		realInstance := instances[instanceIdx.Index]
		weight := realInstance.GetWeight()
		for i := 0; i < weight; i++ {
			hashKey := fmt.Sprintf("%s:%d:%d", realInstance.GetHost(), i, realInstance.GetPort())
			hashValue := uint64(murmur32.Sum32WithSeed([]byte(hashKey), 16))
			if addr, ok := hashValues[hashValue]; !ok {
				hashValues[hashValue] = continuumPoint{
					hashKey:   hashKey,
					hashValue: hashValue,
					index:     instanceIdx.Index,
				}
			} else {
				oldInstance := instances[addr.index]
				if compare(realInstance, oldInstance) {
					hashValues[hashValue] = continuumPoint{
						hashKey:   hashKey,
						hashValue: hashValue,
						index:     instanceIdx.Index,
					}
				}
			}
		}
	}
	for _, value := range hashValues {
		continuum.ring = append(continuum.ring, value)
	}
	if len(continuum.ring) > 1 {
		sort.Sort(continuum.ring)
	}
	return continuum, nil
}

//选择实例下标
func (c *L5ContinuumSelector) Select(value interface{}) (int, *model.ReplicateNodes, error) {
	ringLen := len(c.ring)
	switch ringLen {
	case 0:
		return -1, nil, nil
	case 1:
		return c.ring[0].index, nil, nil
	default:
		criteria := value.(*loadbalancer.Criteria)
		hashValue, _ := common.CalcHashValueWithSeed(criteria, func([]byte, uint32) (uint64, error) {
			var hashValue = uint64(murmur32.Sum32WithSeed(criteria.HashKey, 16))
			return hashValue, nil
		}, 16)
		targetIndex, nodes := c.selectByHashValue(hashValue)
		return targetIndex, nodes, nil
	}
}

//通过hash值选择具体的节点
func (c *L5ContinuumSelector) selectByHashValue(hashValue uint64) (int, *model.ReplicateNodes) {
	ringIndex := search.BinarySearch(c.ring, hashValue)
	targetPoint := &c.ring[ringIndex]
	targetIndex := targetPoint.index
	return targetIndex, nil
}
