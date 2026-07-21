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

package configuration

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/configconnector"

	// 匿名引入 zaplog：其 init() 会注册默认全局 Logger，保证 SetBaseLogger 前后有可恢复的原始 logger。
	_ "github.com/polarismesh/polaris-go/plugin/logger/zaplog"
)

// captureLogger 是一个捕获日志格式化结果的测试用 Logger，用于断言日志内容。
// 所有级别方法都记录格式化后的字符串；IsLevelEnabled 返回 false，
// 使得受 Debug 级别开关保护的日志不被触发，仅捕获无条件打印的 Info/Warn/Error 等。
type captureLogger struct {
	mu    sync.Mutex
	lines []string
}

func (l *captureLogger) record(format string, args ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *captureLogger) Tracef(format string, args ...interface{}) { l.record(format, args...) }
func (l *captureLogger) Debugf(format string, args ...interface{}) { l.record(format, args...) }
func (l *captureLogger) Infof(format string, args ...interface{})  { l.record(format, args...) }
func (l *captureLogger) Warnf(format string, args ...interface{})  { l.record(format, args...) }
func (l *captureLogger) Errorf(format string, args ...interface{}) { l.record(format, args...) }
func (l *captureLogger) Fatalf(format string, args ...interface{}) { l.record(format, args...) }
func (l *captureLogger) IsLevelEnabled(int) bool                   { return false }
func (l *captureLogger) SetLogLevel(int) error                     { return nil }

// dump 返回捕获到的全部日志行拼接结果。
func (l *captureLogger) dump() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

// newTestConfigFile 构造一个用于测试的 defaultConfigFile，其 fileRepo 的 loadRemoteFile()
// 返回指定的远程文件（携带 Encrypted/Md5/Version）。logCtx 置零，GetBaseLogger 会回退到全局 logger。
func newTestConfigFile(oldContent string, remote *configconnector.ConfigFile) *defaultConfigFile {
	repo := &ConfigFileRepo{remoteConfigFileRef: &atomic.Value{}}
	repo.remoteConfigFileRef.Store(remote)
	cf := &defaultConfigFile{
		fileRepo: repo,
		content:  oldContent,
	}
	cf.Namespace = remote.GetNamespace()
	cf.FileGroup = remote.GetFileGroup()
	cf.FileName = remote.GetFileName()
	return cf
}

// TestRepoChangeListener_EncryptedNotLogPlaintext 验证加密场景下变更日志不泄露明文内容，
// 且打印了不可逆的 Md5；同时对用户可见的 content 仍被正确更新。
func TestRepoChangeListener_EncryptedNotLogPlaintext(t *testing.T) {
	orig := log.GetBaseLogger()
	cl := &captureLogger{}
	log.SetBaseLogger(cl)
	defer log.SetBaseLogger(orig)

	const oldPlain = "old-secret-plaintext"
	const newPlain = "new-secret-plaintext"

	remote := &configconnector.ConfigFile{
		Namespace: "default",
		FileGroup: "polaris-config-example",
		FileName:  "aes.yaml",
		Encrypted: true,
		Md5:       "md5-encrypted-xyz",
		Version:   7,
	}
	cf := newTestConfigFile(oldPlain, remote)

	metadata := &model.DefaultConfigFileMetadata{
		Namespace: "default",
		FileGroup: "polaris-config-example",
		FileName:  "aes.yaml",
	}
	if err := cf.repoChangeListener(metadata, newPlain, model.Persistent{}); err != nil {
		t.Fatalf("repoChangeListener 返回错误: %v", err)
	}

	logs := cl.dump()
	if strings.Contains(logs, oldPlain) || strings.Contains(logs, newPlain) {
		t.Errorf("加密场景日志泄露了明文内容:\n%s", logs)
	}
	if !strings.Contains(logs, "md5-encrypted-xyz") {
		t.Errorf("加密场景日志应包含 Md5 以判断变更:\n%s", logs)
	}
	if cf.content != newPlain {
		t.Errorf("content 未被正确更新: got %q, want %q", cf.content, newPlain)
	}
}

// TestRepoChangeListener_PlainNotLogContent 验证非加密场景同样不打印配置内容明文，
// 仅打印 Md5/version（配置内容可能敏感，无论是否加密都不进日志）；content 仍被正确更新。
func TestRepoChangeListener_PlainNotLogContent(t *testing.T) {
	orig := log.GetBaseLogger()
	cl := &captureLogger{}
	log.SetBaseLogger(cl)
	defer log.SetBaseLogger(orig)

	const oldPlain = "old-plain-value"
	const newPlain = "new-plain-secret-value"

	remote := &configconnector.ConfigFile{
		Namespace: "default",
		FileGroup: "polaris-config-example",
		FileName:  "example.yaml",
		Encrypted: false,
		Md5:       "md5-plain-xyz",
		Version:   3,
	}
	cf := newTestConfigFile(oldPlain, remote)

	metadata := &model.DefaultConfigFileMetadata{
		Namespace: "default",
		FileGroup: "polaris-config-example",
		FileName:  "example.yaml",
	}
	if err := cf.repoChangeListener(metadata, newPlain, model.Persistent{}); err != nil {
		t.Fatalf("repoChangeListener 返回错误: %v", err)
	}

	logs := cl.dump()
	if strings.Contains(logs, oldPlain) || strings.Contains(logs, newPlain) {
		t.Errorf("非加密场景日志也不应打印配置内容明文:\n%s", logs)
	}
	if !strings.Contains(logs, "md5-plain-xyz") {
		t.Errorf("非加密场景日志应包含 Md5 以判断变更:\n%s", logs)
	}
	if cf.content != newPlain {
		t.Errorf("content 未被正确更新: got %q, want %q", cf.content, newPlain)
	}
}

// TestRepoChangeListener_DeletedRemoteNil 验证删除(NotExist)场景：loadRemoteFile() 返回 nil 时
// 不 panic，encrypted/md5/version 取零值，日志不泄露原有内容，且 changeType 为 Deleted、content 被清空。
func TestRepoChangeListener_DeletedRemoteNil(t *testing.T) {
	orig := log.GetBaseLogger()
	cl := &captureLogger{}
	log.SetBaseLogger(cl)
	defer log.SetBaseLogger(orig)

	const oldPlain = "old-content-to-delete"

	// remoteConfigFileRef 为空（不 Store 任何文件），模拟删除后 fireChangeEvent 重置的场景，
	// 使 loadRemoteFile() 返回 nil。
	repo := &ConfigFileRepo{remoteConfigFileRef: &atomic.Value{}}
	cf := &defaultConfigFile{
		fileRepo: repo,
		content:  oldPlain,
	}
	cf.Namespace = "default"
	cf.FileGroup = "polaris-config-example"
	cf.FileName = "aes.yaml"

	metadata := &model.DefaultConfigFileMetadata{
		Namespace: "default",
		FileGroup: "polaris-config-example",
		FileName:  "aes.yaml",
	}
	// 传入 NotExistedFileContent 触发删除语义（oldContent 非删除标记、newContent 为删除标记）。
	if err := cf.repoChangeListener(metadata, NotExistedFileContent, model.Persistent{}); err != nil {
		t.Fatalf("repoChangeListener 返回错误: %v", err)
	}

	logs := cl.dump()
	if strings.Contains(logs, oldPlain) {
		t.Errorf("删除场景日志不应泄露原有内容:\n%s", logs)
	}
	if !strings.Contains(logs, "Deleted") {
		t.Errorf("删除场景应记录 changeType=Deleted:\n%s", logs)
	}
	if cf.content != "" {
		t.Errorf("删除后 content 应被清空: got %q", cf.content)
	}
}
