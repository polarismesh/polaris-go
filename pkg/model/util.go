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

package model

import (
	"fmt"
	"hash/fnv"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/mitchellh/go-homedir"
)

const (
	HomeVar = "$HOME"
)

//Hash集合数据结构
type HashSet map[interface{}]bool

//往集合添加值
func (h HashSet) Add(value interface{}) {
	if _, ok := h[value]; !ok {
		h[value] = true
	}
}

//往集合删除值
func (h HashSet) Delete(value interface{}) bool {
	if _, ok := h[value]; ok {
		delete(h, value)
		return true
	}
	return false
}

//值是否存在集合中
func (h HashSet) Contains(value interface{}) bool {
	_, ok := h[value]
	return ok
}

//复制hashSet
func (h HashSet) Copy() HashSet {
	newSet := make(map[interface{}]bool, len(h))
	for k, v := range h {
		newSet[k] = v
	}
	return newSet
}

//创建协程安全的HashSet
func NewSyncHashSet() *SyncHashSet {
	return &SyncHashSet{
		values: HashSet{},
	}
}

//协程安全的HashSet
type SyncHashSet struct {
	values HashSet
	mutex  sync.Mutex
}

//往set添加元素
func (s *SyncHashSet) Add(value interface{}) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.values.Add(value)
}

//删除元素
func (s *SyncHashSet) Delete(value interface{}) bool {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.values.Delete(value)
}

//检查元素存在性
func (s *SyncHashSet) Contains(value interface{}) bool {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.values.Contains(value)
}

//拷贝列表的元素
func (s *SyncHashSet) Copy() HashSet {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.values.Copy()
}

//IsDir file path is dir
func IsDir(path string) bool {
	s, err := os.Stat(path)
	if err != nil {
		return false
	}
	return s.IsDir()
}

//IsFile file path is dir
func IsFile(path string) bool {
	return !IsDir(path)
}

//对字符串进行hash操作
func HashStr(key string) (uint64, error) {
	a := fnv.New64()
	_, err := a.Write([]byte(key))
	if nil != err {
		return 0, err
	}
	return a.Sum64(), nil
}

//对PB消息进行hash
func HashMessage(message proto.Message) uint64 {
	hashCode, _ := HashStr(message.String())
	return hashCode
}

//转换时间指针
func ToDurationPtr(v time.Duration) *time.Duration {
	return &v
}

// ToMilliSeconds 时间转换成毫秒
func ToMilliSeconds(v time.Duration) int64 {
	return ParseMilliSeconds(v.Nanoseconds())
}

// ParseMilliSeconds 时间转换成毫秒
func ParseMilliSeconds(v int64) int64 {
	return v / 1e6
}

// GetIP get local ip from inteface name like eth1
func GetIP(name string) (string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}

	for _, v := range ifaces {
		if v.Name == name {
			addrs, err := v.Addrs()
			if err != nil {
				return "", err
			}

			for _, addr := range addrs {
				var ip net.IP
				switch val := addr.(type) {
				case *net.IPNet:
					ip = val.IP
				case *net.IPAddr:
					ip = val.IP
				default:
					continue
				}

				if len(ip) == net.IPv6len {
					return ip.To16().String(), nil
				} else if len(ip) == net.IPv4len {
					return ip.To4().String(), nil
				}
			}
		}
	}

	return "", fmt.Errorf("net interfaces is empty")
}

//IsNearbyMatch 判断是否满足就近条件
func IsNearbyMatch(dst, src string) bool {
	if len(dst) == 0 || len(src) == 0 {
		return true
	}
	return dst == src
}

//替换相对路径
func ReplaceHomeVar(path string) string {
	if !strings.HasPrefix(path, HomeVar) {
		return path
	}
	homeDir, err := homedir.Dir()
	if nil != err {
		return strings.Replace(path, HomeVar, ".", 1)
	}
	return strings.Replace(path, HomeVar, homeDir, 1)
}

//查看文件路径是否存在
func PathExist(path string) bool {
	_, err := os.Stat(path)
	if err != nil {
		if os.IsExist(err) {
			return true
		}
		return false
	}
	return true
}

//检测缓存目录，不存在则创建
func EnsureAndVerifyDir(dir string) error {
	if !PathExist(dir) {
		err := os.MkdirAll(dir, 0744)
		if nil != err {
			return NewSDKError(ErrCodeDiskError, err, "unable to create dir %s", dir)
		}
		return nil
	}
	pathInfo, _ := os.Stat(dir)
	if !pathInfo.IsDir() {
		return NewSDKError(ErrCodeDiskError, nil, "path %s is a file path", dir)
	}
	return nil
}

//从错误中获取错误码
func GetErrorCodeFromError(e error) ErrCode {
	if e == nil {
		return ErrCodeSuccess
	}
	sdkErr, ok := e.(SDKError)
	if !ok {
		return ErrCodeUnknown
	}
	return sdkErr.ErrorCode()
}

//服务实例是否可用
func IsInstanceAvailable(instance Instance) bool {
	if !instance.IsHealthy() {
		return false
	}
	cbStatus := instance.GetCircuitBreakerStatus()
	if nil != cbStatus && !cbStatus.IsAvailable() {
		return false
	}
	return true
}

//对map进行排序, keys的长度必须等于map的长度
//返回已经排序的key，以及map中总字符串长度
func SortMap(values map[string]string, keys []string) ([]string, int) {
	if len(values) == 0 {
		return keys, 0
	}
	if len(keys) < len(values) {
		keys = make([]string, len(values))
	}
	var idx int
	var count int
	for k, v := range values {
		count += len(k) + len(v)
		keys[idx] = k
		idx++
	}
	if len(keys) > 1 {
		sort.Strings(keys)
	}
	return keys, count
}

//将uint32类型转化为ipv4地址
func ToNetIP(val uint32) net.IP {
	return net.IPv4(byte(val>>24), byte(val>>16&0xFF), byte(val>>8)&0xFF, byte(val&0xFF))
}

//NewConcurrentMap 创建一个新的 ConcurrentMap
func NewConcurrentMap() *ConcurrentMap {
	return &ConcurrentMap{
		actualMap: make(map[interface{}]interface{}),
		rwLock:    sync.RWMutex{},
	}
}

//ConcurrentMap 并发map，读读不阻塞
type ConcurrentMap struct {
	actualMap map[interface{}]interface{}
	rwLock    sync.RWMutex
	size      int
}

//Put 存入一个键值对
func (cm *ConcurrentMap) Put(k, v interface{}) {
	defer cm.rwLock.Unlock()
	cm.rwLock.Lock()
	cm.actualMap[k] = v
	cm.size++
}

//Remove 根据 key 删除一个 key-value
func (cm *ConcurrentMap) Remove(k interface{}) {
	defer cm.rwLock.Unlock()
	cm.rwLock.Lock()
	delete(cm.actualMap, k)
	cm.size--
}

//Get 根据 key 获取一个数据
func (cm *ConcurrentMap) Get(k interface{}) interface{} {
	defer cm.rwLock.RUnlock()
	cm.rwLock.RLock()
	return cm.actualMap[k]
}

//Contains 判断是否包含某个 key
func (cm *ConcurrentMap) Contains(k interface{}) bool {
	defer cm.rwLock.RUnlock()
	cm.rwLock.RLock()
	_, exist := cm.actualMap[k]
	return exist
}

//ForEach 遍历所有的 key-value
func (cm *ConcurrentMap) ForEach(consumer func(k, v interface{})) {
	defer cm.rwLock.RUnlock()
	cm.rwLock.RLock()
	for k, v := range cm.actualMap {
		consumer(k, v)
	}
}

//Keys 获取所有的 key 数组
func (cm *ConcurrentMap) Keys() []interface{} {
	defer cm.rwLock.RUnlock()
	cm.rwLock.RLock()
	keys := make([]interface{}, len(cm.actualMap))
	i := 0
	for k := range cm.actualMap {
		keys[i] = k
		i++
	}
	return keys
}

//Values 获取所有的 value 数组
func (cm *ConcurrentMap) Values() []interface{} {
	defer cm.rwLock.RUnlock()
	cm.rwLock.RLock()
	values := make([]interface{}, len(cm.actualMap))
	i := 0
	for _, v := range cm.actualMap {
		values[i] = v
		i++
	}
	return values
}

//ComputeIfAbsent 懒Put操作，通过 key 计算是否存在该 key，如果存在，直接返回，否则执行 function 方法计算对应的 value
func (cm *ConcurrentMap) ComputeIfAbsent(key interface{}, function func(key interface{}) interface{}) (bool, interface{}) {
	defer cm.rwLock.Unlock()
	cm.rwLock.Lock()

	exist := true

	if _, exist := cm.actualMap[key]; !exist {
		cm.actualMap[key] = function(key)
		cm.size++
		exist = false
	}
	return exist, cm.actualMap[key]
}

//PutIfAbsent
func (cm *ConcurrentMap) PutIfAbsent(key, val interface{}) interface{} {
	defer cm.rwLock.Unlock()
	cm.rwLock.Lock()

	v := cm.actualMap[key]
	if v == nil {
		cm.actualMap[key] = val
		return nil
	}

	return v
}

//Clear 清空 map
func (cm *ConcurrentMap) Clear() {
	defer cm.rwLock.Unlock()
	cm.rwLock.Lock()
	cm.actualMap = make(map[interface{}]interface{})
}

//Size 返回map的元素个数
func (cm *ConcurrentMap) Size() int {
	return cm.size
}
