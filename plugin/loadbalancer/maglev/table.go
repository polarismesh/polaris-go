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

package maglev

import (
	"fmt"
	"github.com/polarismesh/polaris-go/pkg/algorithm/hash"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/loadbalancer"
	"github.com/polarismesh/polaris-go/plugin/loadbalancer/common"
	"math"
)

//maglev向量表选择器
type TableSelector struct {
	model.SelectorBase
	nodes             []*model.WeightedIndex
	tableSize         uint64
	hashFunc          hash.HashFuncWithSeed
	minEntriesPerHost float64
	maxEntriesPerHost float64
}

//构建表过程中的一些元数据中间信息
type tableBuildEntry struct {
	node             *model.WeightedIndex
	offset           uint64
	skip             uint64
	normalizedWeight float64
	targetWeight     float64
	next             uint64
	count            uint64
}

//构建表数据
func (t *TableSelector) buildTableEntries(
	instanceSet *model.InstanceSet, hashFunc hash.HashFuncWithSeed) (float64, []tableBuildEntry, error) {
	var realInstance model.Instance
	var svcInstances = instanceSet.GetServiceClusters().GetServiceInstances()
	var instances = svcInstances.GetInstances()
	var instanceSlice = instanceSet.GetInstances()
	var totalWeight = instanceSet.TotalWeight()
	var maxNormalizedWeight float64
	entries := make([]tableBuildEntry, instanceSet.Count())
	for i := 0; i < instanceSet.Count(); i++ {
		entry := &entries[i]
		instanceIdx := &instanceSlice[i]
		realInstance = instances[instanceIdx.Index]
		normalizedWeight := float64(realInstance.GetWeight()) / float64(totalWeight)
		if maxNormalizedWeight < normalizedWeight {
			maxNormalizedWeight = normalizedWeight
		}
		idBuf := []byte(realInstance.GetId())
		seed0HashValue, err := hashFunc(idBuf, 0)
		if nil != err {
			return 0, nil, fmt.Errorf("fail to get seed0 hash value for %s", realInstance.GetId())
		}
		seed1HashValue, err := hashFunc(idBuf, 1)
		if nil != err {
			return 0, nil, fmt.Errorf("fail to get seed1 hash value for %s", realInstance.GetId())
		}
		entry.node = instanceIdx
		entry.normalizedWeight = normalizedWeight
		entry.offset = seed0HashValue % t.tableSize
		entry.skip = seed1HashValue%(t.tableSize-1) + 1
	}
	return maxNormalizedWeight, entries, nil
}

//创建maglev向量选择器
func NewTable(
	instanceSet *model.InstanceSet, tableSize uint64, hashFunc hash.HashFuncWithSeed, id int32) (*TableSelector, error) {
	var selector = &TableSelector{
		hashFunc:  hashFunc,
		tableSize: tableSize,
	}
	selector.Id = id
	if instanceSet.Count() == 0 {
		return selector, nil
	}
	selector.nodes = make([]*model.WeightedIndex, tableSize)
	maxNormalizedWeight, entries, err := selector.buildTableEntries(instanceSet, hashFunc)
	if nil != err {
		return nil, err
	}
	var tableIndex uint64
	var entrySize = len(entries)
	for iteration := 1; tableIndex < tableSize; iteration++ {
		for i := 0; i < entrySize && tableIndex < tableSize; i++ {
			entry := &entries[i]
			// To understand how target_weight_ and weight_ are used below, consider a host with weight
			// equal to max_normalized_weight. This would be picked on every single iteration. If it had
			// weight equal to max_normalized_weight / 3, then it would only be picked every 3 iterations,
			// etc.
			if float64(iteration)*entry.normalizedWeight < entry.targetWeight {
				continue
			}
			entry.targetWeight += maxNormalizedWeight
			pIndex := selector.permutation(entry)
			for nil != selector.nodes[pIndex] {
				entry.next++
				pIndex = selector.permutation(entry)
			}
			selector.nodes[pIndex] = entry.node
			entry.next++
			entry.count++
			tableIndex++
		}
	}
	selector.minEntriesPerHost = float64(tableSize)
	selector.maxEntriesPerHost = 0
	for _, entry := range entries {
		selector.minEntriesPerHost = math.Min(float64(entry.count), selector.minEntriesPerHost)
		selector.maxEntriesPerHost = math.Max(float64(entry.count), selector.maxEntriesPerHost)
	}
	svcInstances := instanceSet.GetServiceClusters().GetServiceInstances()
	log.GetBaseLogger().Debugf("maglev: build for %s:%s, maxEntriesPerHost %.1f, minEntriesPerHost %.1f",
		svcInstances.GetNamespace(), svcInstances.GetService(), selector.maxEntriesPerHost, selector.minEntriesPerHost)
	return selector, nil
}

//进行列表排序
func (t *TableSelector) permutation(entry *tableBuildEntry) uint64 {
	return (entry.offset + entry.skip*entry.next) % t.tableSize
}

//选择实例下标
func (t *TableSelector) Select(value interface{}) (int, *model.ReplicateNodes, error) {
	ringLen := len(t.nodes)
	if ringLen == 0 {
		return -1, nil, nil
	}
	criteria := value.(*loadbalancer.Criteria)
	hashValue, err := common.CalcHashValue(criteria, t.hashFunc)
	if nil != err {
		return -1, nil, err
	}
	nodeIndex := int(hashValue % t.tableSize)
	return t.nodes[nodeIndex].Index, nil, nil
}
