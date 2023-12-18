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
