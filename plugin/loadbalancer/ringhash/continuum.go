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
	"bytes"
	"encoding/binary"
	"fmt"
	"github.com/polarismesh/polaris-go/pkg/algorithm/hash"
	"github.com/polarismesh/polaris-go/pkg/algorithm/search"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/loadbalancer"
	"github.com/polarismesh/polaris-go/plugin/loadbalancer/common"
	"github.com/modern-go/reflect2"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
)

//一致性hash环的节点
type continuumPoint struct {
	//hash的主键
	hashKey string
	//hash值
	hashValue uint64
	//实例的数组下标
	index int
	//备份节点
	replicates *atomic.Value
}

//hash环数组
type points []continuumPoint

//比较环中节点hash值
func (c points) Less(i, j int) bool { return c[i].hashValue < c[j].hashValue }

//获取环长度
func (c points) Len() int { return len(c) }

//交换位置
func (c points) Swap(i, j int) { c[i], c[j] = c[j], c[i] }

//获取数组下标的值
func (c points) GetValue(idx int) uint64 {
	return c[idx].hashValue
}

//数组长度
func (c points) Count() int {
	return c.Len()
}

//一致性hash环
type ContinuumSelector struct {
	model.SelectorBase
	svcClusters model.ServiceClusters
	ring        points
	hashFunc    hash.HashFuncWithSeed
}

const (
	//最多做多少次rehash
	maxRehashIteration = 5
)

//创建hash环
func NewContinuum(
	instanceSet *model.InstanceSet, vnodeCount int, hashFunc hash.HashFuncWithSeed, id int32) (*ContinuumSelector, error) {
	var continuum = &ContinuumSelector{
		svcClusters: instanceSet.GetServiceClusters(),
		hashFunc:    hashFunc,
	}
	continuum.Id = id
	if instanceSet.Count() == 0 {
		return continuum, nil
	}
	var ringLen = vnodeCount * instanceSet.Count()
	continuum.ring = make(points, 0, ringLen)
	svcInstances := instanceSet.GetServiceClusters().GetServiceInstances()
	instances := svcInstances.GetInstances()
	instanceSlice := instanceSet.GetInstances()
	var realInstance model.Instance
	var maxWeight = instanceSet.MaxWeight()
	var hashValues = make(map[uint64]string, ringLen)
	var err error
	for _, instanceIdx := range instanceSlice {
		realInstance = instances[instanceIdx.Index]
		weight := realInstance.GetWeight()
		pct := float64(weight) / float64(maxWeight)
		limit := int(math.Floor(pct * float64(vnodeCount)))
		for i := 0; i < limit; i++ {
			hashKeyBuilder := strings.Builder{}
			hashKeyBuilder.Grow(len(realInstance.GetId()))
			hashKeyBuilder.WriteString(realInstance.GetId())
			hashKeyBuilder.WriteString(strconv.Itoa(i))
			hashKey := hashKeyBuilder.String()
			var hashValue uint64
			if hashValue, err = hashFunc([]byte(hashKey), 0); nil != err {
				return nil, err
			}
			if addr, ok := hashValues[hashValue]; ok {
				//hash冲突
				log.GetBaseLogger().Debugf("hash conflict between %s and %s", addr, hashKey)
				hashValue, hashKey, err = continuum.doRehash(hashValue, hashValues, 1)
				if nil != err {
					log.GetBaseLogger().Errorf("fail to generate hash at %s:%d(id=%s, limit=%d), error %v",
						realInstance.GetHost(), realInstance.GetPort(), realInstance.GetId(), i, err)
					continue
				}
			}
			hashValues[hashValue] = hashKey
			continuum.ring = append(continuum.ring, continuumPoint{
				hashKey:    hashKey,
				hashValue:  hashValue,
				index:      instanceIdx.Index,
				replicates: &atomic.Value{},
			})
		}
	}
	if len(continuum.ring) > 1 {
		sort.Sort(continuum.ring)
	}
	if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
		svcKey := &model.ServiceKey{
			Namespace: svcInstances.GetNamespace(),
			Service:   svcInstances.GetService(),
		}
		log.GetBaseLogger().Debugf("hash ring for service %s is \n%s", *svcKey, continuum.String())
	}
	return continuum, nil
}

//打印hash环
func (c ContinuumSelector) String() string {
	builder := &strings.Builder{}
	builder.WriteString("[")
	if len(c.ring) > 0 {
		svcInstances := c.svcClusters.GetServiceInstances().GetInstances()
		for i, point := range c.ring {
			if i > 0 {
				builder.WriteString(", ")
			}
			builder.WriteString(fmt.Sprintf("{\"idx\": %d, \"hash\": %d, \"instIdx\": %d, \"instId\": "+
				"\"%s\", \"address\": \"%s:%d\"}",
				i, point.hashValue, point.index, svcInstances[point.index].GetId(),
				svcInstances[point.index].GetHost(), svcInstances[point.index].GetPort()))
		}
	}
	builder.WriteString("]")
	return builder.String()
}

//做rehash
func (c *ContinuumSelector) doRehash(
	lastHash uint64, hashValues map[uint64]string, iteration int) (uint64, string, error) {
	if iteration > maxRehashIteration {
		return 0, "", fmt.Errorf("rehash exceed max iteration %d", maxRehashIteration)
	}
	var err error
	var hashValue uint64
	buf := bytes.NewBuffer(make([]byte, 0, 8))
	if err = binary.Write(buf, binary.LittleEndian, lastHash); nil != err {
		return 0, "", err
	}
	if hashValue, err = c.hashFunc(buf.Bytes(), 0); nil != err {
		return 0, "", err
	}
	if key, ok := hashValues[hashValue]; ok {
		log.GetBaseLogger().Debugf("hash conflict between %s and %d", key, lastHash)
		return c.doRehash(hashValue, hashValues, iteration+1)
	}
	lastHashStr := strconv.FormatInt(int64(lastHash), 10)
	hashValues[hashValue] = lastHashStr
	return hashValue, lastHashStr, nil
}

//选择实例下标
func (c *ContinuumSelector) Select(value interface{}) (int, *model.ReplicateNodes, error) {
	ringLen := len(c.ring)
	switch ringLen {
	case 0:
		return -1, nil, nil
	case 1:
		return c.ring[0].index, nil, nil
	default:
		criteria := value.(*loadbalancer.Criteria)
		hashValue, err := common.CalcHashValue(criteria, c.hashFunc)
		if nil != err {
			return -1, nil, err
		}
		targetIndex, nodes := c.selectByHashValue(hashValue, criteria.ReplicateInfo.Count)
		return targetIndex, nodes, nil
	}
}

//通过hash值选择具体的节点
func (c *ContinuumSelector) selectByHashValue(hashValue uint64, replicateCount int) (int, *model.ReplicateNodes) {
	ringIndex := search.BinarySearch(c.ring, hashValue)
	targetPoint := &c.ring[ringIndex]
	targetIndex := targetPoint.index
	if replicateCount == 0 {
		return targetIndex, nil
	}
	replicateNodesValue := targetPoint.replicates.Load()
	if !reflect2.IsNil(replicateNodesValue) {
		replicateNodes := replicateNodesValue.(*model.ReplicateNodes)
		if replicateNodes.Count == replicateCount {
			//个数匹配，则直接获取缓存信息
			return targetIndex, replicateNodes
		}
	}
	replicateIndexes := make([]int, 0, replicateCount)
	ringSize := c.ring.Len()
	for i := 1; i < ringSize; i++ {
		if len(replicateIndexes) == replicateCount {
			break
		}
		replicRingIndex := (ringIndex + i) % ringSize
		replicRing := &c.ring[replicRingIndex]
		replicateIndex := replicRing.index
		if targetIndex == replicateIndex {
			continue
		}
		if containsIndex(replicateIndexes, replicateIndex) {
			continue
		}
		replicateIndexes = append(replicateIndexes, replicateIndex)
	}
	//加入缓存
	replicateNodes := &model.ReplicateNodes{
		SvcClusters: c.svcClusters,
		Count:       replicateCount,
		Indexes:     replicateIndexes,
	}
	targetPoint.replicates.Store(replicateNodes)
	return targetIndex, replicateNodes
}

//查看数组是否包含索引
func containsIndex(replicateIndexes []int, replicateIndex int) bool {
	for _, idx := range replicateIndexes {
		if idx == replicateIndex {
			return true
		}
	}
	return false
}
