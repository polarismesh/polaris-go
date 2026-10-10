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

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	apimodel "github.com/polarismesh/specification/source/go/api/v1/model"
)

const fixtureFileName = "listener.yaml"

// fixture 管理本轮独占测试数据；只删除已成功创建的随机分组和文件。
type fixture struct {
	baseURL, namespace, group, token string
	client                           *http.Client
	groupCreated, fileCreated        bool
}

// newFixture 生成随机测试分组，不读取或修改已有业务配置。
func newFixture(o options, token string) (*fixture, error) {
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return nil, fmt.Errorf("generate test group: %w", err)
	}
	return &fixture{
		baseURL: strings.TrimRight(o.openAPI, "/"), namespace: o.namespace,
		group: "e2e-listener-cancel-" + hex.EncodeToString(suffix[:]), token: token,
		client: &http.Client{Timeout: o.timeout},
	}, nil
}

// request 调用 Polaris 管理 API，同时校验 HTTP 状态和业务 code；不输出 token 或响应正文。
func (f *fixture) request(ctx context.Context, method, path string, body interface{}) error {
	var payload []byte
	var err error
	if body != nil {
		payload, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode %s %s: %w", method, path, err)
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, f.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if f.token != "" {
		req.Header.Set("X-Polaris-Token", f.token)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	var result struct {
		Code json.Number `json:"code"`
	}
	err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: HTTP %d, code=%s", method, path, resp.StatusCode, result.Code)
	}
	if err != nil {
		return fmt.Errorf("decode %s %s: %w", method, path, err)
	}
	code, err := strconv.ParseUint(string(result.Code), 10, 32)
	if err != nil || uint32(code) != uint32(apimodel.Code_ExecuteSuccess) {
		return fmt.Errorf("%s %s: unexpected business code %q", method, path, result.Code)
	}
	return nil
}

// prepare 在现有命名空间创建独占分组与未发布文件；发布由真实 SDK 完成。
func (f *fixture) prepare(ctx context.Context) error {
	log.Printf("[SETUP] namespace=%s group=%s file=%s", f.namespace, f.group, fixtureFileName)
	if err := f.request(ctx, http.MethodPost, "/config/v1/configfilegroups", map[string]string{
		"namespace": f.namespace, "name": f.group, "comment": "CancelableConfigGroupListener E2E temporary group",
	}); err != nil {
		return err
	}
	f.groupCreated = true
	if err := f.request(ctx, http.MethodPost, "/config/v1/configfiles", map[string]string{
		"namespace": f.namespace, "group": f.group, "name": fixtureFileName,
		"content": "generation: initial\n", "format": "yaml",
	}); err != nil {
		return err
	}
	f.fileCreated = true
	return nil
}

// cleanup 使用独立超时清理本轮数据，主测试失败或收到终止信号时仍执行；失败返回错误。
func (f *fixture) cleanup() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*f.client.Timeout)
	defer cancel()
	query := url.Values{"namespace": {f.namespace}, "group": {f.group}, "name": {fixtureFileName}}
	var result error
	if f.fileCreated {
		if err := f.request(ctx, http.MethodDelete, "/config/v1/configfiles?"+query.Encode(), nil); err != nil {
			result = errors.Join(result, err)
		}
	}
	query.Del("name")
	if f.groupCreated {
		if err := f.request(ctx, http.MethodDelete, "/config/v1/configfilegroups?"+query.Encode(), nil); err != nil {
			result = errors.Join(result, err)
		}
	}
	if result == nil && f.groupCreated {
		log.Printf("[CLEANUP] 已删除本轮分组 %s/%s", f.namespace, f.group)
	}
	return result
}

// waitUntil 周期检查条件直到满足或 context 超时；condition 应快速返回且可安全重复调用。
func waitUntil(ctx context.Context, description string, condition func() bool) error {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("%s: %w", description, err)
		}
		if condition() {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s: %w", description, ctx.Err())
		case <-ticker.C:
		}
	}
}

// waitSignal 等待阶段完成信号，超时返回包含阶段名的错误。
func waitSignal(ctx context.Context, signal <-chan struct{}, description string) error {
	select {
	case <-signal:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("%s: %w", description, ctx.Err())
	}
}
