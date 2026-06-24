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

package http

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/polarismesh/specification/source/go/api/v1/fault_tolerance"
	"github.com/polarismesh/specification/source/go/api/v1/service_manage"
	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/pb"
)

func TestDetector_DetectInstance(t *testing.T) {
	mockIns := &pb.InstanceInProto{
		Instance: &service_manage.Instance{
			Host: wrapperspb.String("127.0.0.1"),
			Port: wrapperspb.UInt32(60000),
		},
	}

	t.Run("MethodGet", func(t *testing.T) {
		t.Run("normal", func(t *testing.T) {
			mockRule := &fault_tolerance.FaultDetectRule{
				Port: 0,
				HttpConfig: &fault_tolerance.HttpProtocolConfig{
					Method: http.MethodGet,
					Url:    "/test",
					Headers: []*fault_tolerance.HttpProtocolConfig_MessageHeader{
						{
							Key:   "X-Header",
							Value: "test",
						},
					},
				},
			}
			mockSvr := &MockHttpServer{
				callback: func(rsp http.ResponseWriter, req *http.Request) {
					assert.Equal(t, req.URL.Path, mockRule.HttpConfig.Url)
					assert.Equal(t, req.Method, http.MethodGet)
					assert.Equal(t, req.Header.Get("X-Header"), "test")
					rsp.WriteHeader(http.StatusOK)
				},
			}
			var detector = &Detector{
				client:  mockSvr.MockHttpSender(),
				timeout: time.Second,
				cfg:     &Config{},
			}
			t.Run("normal-get", func(t *testing.T) {
				mockRule.Port = 11111
				ret, err := detector.DetectInstance(mockIns, mockRule)
				assert.NoError(t, err)
				assert.Equal(t, ret.GetCode(), strconv.Itoa(http.StatusOK))
			})

			t.Run("normal-get", func(t *testing.T) {
				mockRule.Port = 0
				ret, err := detector.DetectInstance(mockIns, mockRule)
				assert.NoError(t, err)
				assert.Equal(t, ret.GetCode(), strconv.Itoa(http.StatusOK))
			})
		})

		t.Run("abnormal", func(t *testing.T) {
			expectUrl := "/testExpect"
			mockRule := &fault_tolerance.FaultDetectRule{
				Port: 0,
				HttpConfig: &fault_tolerance.HttpProtocolConfig{
					Method: http.MethodGet,
					Url:    "/test123",
					Headers: []*fault_tolerance.HttpProtocolConfig_MessageHeader{
						{
							Key:   "X-Header",
							Value: "test",
						},
					},
				},
			}
			t.Run("abnormal-get", func(t *testing.T) {
				mockSvr := &MockHttpServer{
					callback: func(rsp http.ResponseWriter, req *http.Request) {
						assert.NotEqual(t, req.URL.Path, expectUrl)
						assert.Equal(t, req.Header.Get("X-Header"), "test")
						rsp.WriteHeader(http.StatusNotFound)
					},
				}
				var detector = &Detector{
					client:  mockSvr.MockHttpSender(),
					timeout: time.Second,
					cfg:     &Config{},
				}

				mockRule.Port = 11111
				ret, err := detector.DetectInstance(mockIns, mockRule)
				assert.NoError(t, err)
				assert.Equal(t, ret.GetCode(), strconv.Itoa(http.StatusNotFound))
				assert.Equal(t, ret.GetRetStatus(), model.RetSuccess)
			})

			t.Run("abnormal-get", func(t *testing.T) {
				mockSvr := &MockHttpServer{
					callback: func(rsp http.ResponseWriter, req *http.Request) {
						assert.NotEqual(t, req.URL.Path, expectUrl)
						assert.Equal(t, req.Header.Get("X-Header"), "test")
						rsp.WriteHeader(http.StatusInternalServerError)
					},
				}
				var detector = &Detector{
					client:  mockSvr.MockHttpSender(),
					timeout: time.Second,
					cfg:     &Config{},
				}

				mockRule.Port = 0
				ret, err := detector.DetectInstance(mockIns, mockRule)
				assert.NoError(t, err)
				assert.Equal(t, ret.GetCode(), strconv.Itoa(http.StatusInternalServerError))
				assert.Equal(t, ret.GetRetStatus(), model.RetFail)
			})
		})
	})
}

type MockHttpSender struct {
	svr *MockHttpServer
}

func (m *MockHttpSender) Do(req *http.Request) (*http.Response, error) {
	record := httptest.NewRecorder()
	m.svr.testHandle(record, req)
	return record.Result(), nil
}

type MockHttpServer struct {
	callback func(rsp http.ResponseWriter, req *http.Request)
}

func (m *MockHttpServer) MockHttpSender() HttpSender {
	return &MockHttpSender{
		svr: m,
	}
}

func (m *MockHttpServer) testHandle(rsp http.ResponseWriter, req *http.Request) {
	if m.callback != nil {
		m.callback(rsp, req)
	}
}

// TestDetector_LastDoErrConcurrent 验证多个 goroutine 并发操作 lastDoErr map 时
// lastDoErrMu 能正确保护并发读写，不会触发 race condition。
func TestDetector_LastDoErrConcurrent(t *testing.T) {
	detector := &Detector{
		lastDoErr: make(map[string]string, 8),
	}

	var wg sync.WaitGroup
	const goroutines = 10
	const iterations = 100

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				urlStr := "http://127.0.0.1:8080/echo"
				if i%2 == 0 {
					urlStr = "http://127.0.0.2:8080/echo"
				}
				// 模拟 doHttpDetect 中对 lastDoErr 的并发读写模式
				detector.lastDoErrMu.Lock()
				if detector.lastDoErr == nil {
					detector.lastDoErr = make(map[string]string, 8)
				}
				errMsg := "connection refused"
				if lastErr, ok := detector.lastDoErr[urlStr]; !ok || lastErr != errMsg {
					detector.lastDoErr[urlStr] = errMsg
				}
				detector.lastDoErrMu.Unlock()

				// 模拟 delete 路径
				if i%3 == 0 {
					detector.lastDoErrMu.Lock()
					if detector.lastDoErr != nil {
						delete(detector.lastDoErr, urlStr)
					}
					detector.lastDoErrMu.Unlock()
				}
			}
		}(g)
	}
	wg.Wait()
	t.Logf("concurrent lastDoErr test completed, goroutines=%d, iterations=%d", goroutines, iterations)
}
