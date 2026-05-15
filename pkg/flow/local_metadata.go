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

package flow

import (
	"fmt"
	"sync"
)

// localInstanceMeta 本端某次成功 Register 留下的实例信息快照。
// metadata 为浅拷贝副本，避免业务侧修改 InstanceRegisterRequest 后影响内部状态。
type localInstanceMeta struct {
	pid        int32
	instanceID string
	host       string
	port       int
	metadata   map[string]string
}

// localMetadataStore SDKContext 维度的本端实例 metadata 表。
// 外层按 (namespace, service) 索引，内层按 (pid, instanceID) 区分同服务下的多个实例。
// PID 进入 key 是为了在多 SDKContext / 进程级隔离场景下保持记录间互不污染，
// 同时也能保留 host:port 反查时按当前进程 PID 精确匹配的能力。
type localMetadataStore struct {
	mu    sync.RWMutex
	store map[string]map[string]*localInstanceMeta
}

// newLocalMetadataStore 创建一个空的本端实例 metadata 表
func newLocalMetadataStore() *localMetadataStore {
	return &localMetadataStore{
		store: make(map[string]map[string]*localInstanceMeta),
	}
}

// Register 登记一个本端实例的 metadata 副本。
// 同一 (ns, svc, pid, instanceID) 重复调用按覆盖语义处理，保留最新一份。
func (s *localMetadataStore) Register(
	namespace, service string, pid int32, instanceID, host string, port int, metadata map[string]string) {
	if instanceID == "" {
		// 无 instanceID 无法精确定位记录，直接丢弃
		return
	}
	svcKey := buildServiceKey(namespace, service)
	innerKey := buildInstanceKey(pid, instanceID)

	s.mu.Lock()
	defer s.mu.Unlock()
	bucket, ok := s.store[svcKey]
	if !ok {
		bucket = make(map[string]*localInstanceMeta)
		s.store[svcKey] = bucket
	}
	bucket[innerKey] = &localInstanceMeta{
		pid:        pid,
		instanceID: instanceID,
		host:       host,
		port:       port,
		metadata:   copyMetadata(metadata),
	}
}

// Deregister 移除一个本端实例的记录。
// 当 instanceID 非空时按 (pid, instanceID) 精确删除；
// 当 instanceID 为空时按 (pid, host, port) 精确匹配该 (ns, svc) 下记录后删除，
// 仅删除当前进程 PID 对应的条目，避免多 SDKContext 场景下误删其它记录。
func (s *localMetadataStore) Deregister(
	namespace, service string, pid int32, instanceID, host string, port int) {
	svcKey := buildServiceKey(namespace, service)

	s.mu.Lock()
	defer s.mu.Unlock()
	bucket, ok := s.store[svcKey]
	if !ok {
		return
	}
	if instanceID != "" {
		delete(bucket, buildInstanceKey(pid, instanceID))
	} else {
		for k, v := range bucket {
			if v.pid == pid && v.host == host && v.port == port {
				delete(bucket, k)
			}
		}
	}
	if len(bucket) == 0 {
		delete(s.store, svcKey)
	}
}

// Get 返回 (ns, svc) 下所有已注册实例的 metadata 浅拷贝列表。
// 调用方对返回的 map 任意读写均不会影响内部状态。
func (s *localMetadataStore) Get(namespace, service string) []map[string]string {
	svcKey := buildServiceKey(namespace, service)

	s.mu.RLock()
	defer s.mu.RUnlock()
	bucket, ok := s.store[svcKey]
	if !ok || len(bucket) == 0 {
		return nil
	}
	result := make([]map[string]string, 0, len(bucket))
	for _, v := range bucket {
		result = append(result, copyMetadata(v.metadata))
	}
	return result
}

// buildServiceKey 拼接 (namespace, service) 索引键
func buildServiceKey(namespace, service string) string {
	return namespace + "/" + service
}

// buildInstanceKey 拼接 (pid, instanceID) 索引键
func buildInstanceKey(pid int32, instanceID string) string {
	return fmt.Sprintf("%d/%s", pid, instanceID)
}

// copyMetadata 返回 metadata map 的浅拷贝；nil 时返回非空空 map，便于调用方安全读取
func copyMetadata(src map[string]string) map[string]string {
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
