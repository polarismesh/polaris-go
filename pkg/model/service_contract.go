// Tencent is pleased to support the open source community by making polaris-go available.
//
// Copyright (C) 2021 Tencent. All rights reserved.
//
// Licensed under the BSD 3-Clause License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// https://opensource.org/licenses/BSD-3-Clause
//
// Unless required by applicable law or agreed to in writing, software distributed
// under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
// CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package model

import (
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// MaxServiceContractFeatures is the maximum number of features in one contract.
	MaxServiceContractFeatures = 1000
	// MaxServiceContractInterfaces is the maximum number of interfaces in one contract.
	MaxServiceContractInterfaces = 2000
	// MaxServiceFeatureContentBytes limits one feature's content.
	MaxServiceFeatureContentBytes = 1024 * 1024
	// MaxServiceContractContentBytes limits top-level contract content.
	MaxServiceContractContentBytes = 16 * 1024 * 1024
	// MaxServiceContractEncodedBytes limits the sum of encoded contract text fields.
	MaxServiceContractEncodedBytes int64 = 64 * 1024 * 1024
)

// ServiceFeatureType is a capability category defined by the contract protocol.
type ServiceFeatureType int32

const (
	// ServiceFeatureTypeMSUnknown is the default microservice feature type.
	ServiceFeatureTypeMSUnknown ServiceFeatureType = 0
	// ServiceFeatureTypeMCPUnknown is the default MCP feature type.
	ServiceFeatureTypeMCPUnknown ServiceFeatureType = 100
	// ServiceFeatureTypeMCPTool identifies an MCP Tool.
	ServiceFeatureTypeMCPTool ServiceFeatureType = 101
	// ServiceFeatureTypeMCPResource identifies an MCP Resource.
	ServiceFeatureTypeMCPResource ServiceFeatureType = 102
	// ServiceFeatureTypeMCPPrompt identifies an MCP Prompt.
	ServiceFeatureTypeMCPPrompt ServiceFeatureType = 103
)

// Valid returns whether the feature type is defined by the specification.
func (t ServiceFeatureType) Valid() bool {
	switch t {
	case ServiceFeatureTypeMSUnknown, ServiceFeatureTypeMCPUnknown,
		ServiceFeatureTypeMCPTool, ServiceFeatureTypeMCPResource, ServiceFeatureTypeMCPPrompt:
		return true
	default:
		return false
	}
}

// ServiceFeatureStatus describes whether a feature is enabled.
type ServiceFeatureStatus int32

const (
	// ServiceFeatureEnabled means the feature is available.
	ServiceFeatureEnabled ServiceFeatureStatus = 0
	// ServiceFeatureDisabled means the feature is disabled.
	ServiceFeatureDisabled ServiceFeatureStatus = 1
)

// Valid returns whether the status is defined by the specification.
func (s ServiceFeatureStatus) Valid() bool {
	return s == ServiceFeatureEnabled || s == ServiceFeatureDisabled
}

// ServiceFeature is one capability published by a service contract.
type ServiceFeature struct {
	ID          string
	Type        ServiceFeatureType
	Name        string
	Description string
	Content     string
	Status      ServiceFeatureStatus
	CreateTime  string
	ModifyTime  string
}

// InterfaceDescriptor is one request or notification handler in a contract.
type InterfaceDescriptor struct {
	ID            string
	Name          string
	Method        string
	Path          string
	Content       string
	Source        int32
	Revision      string
	CreateTime    string
	UpdateTime    string
	Namespace     string
	Service       string
	Protocol      string
	Version       string
	Type          string
	ContentDigest string
}

// GetServiceContractRequest identifies one service contract and its call budget.
type GetServiceContractRequest struct {
	FlowID     uint64
	Namespace  string
	Service    string
	Name       string
	Type       string
	Protocol   string
	Version    string
	Timeout    *time.Duration
	RetryCount *int
}

// SetTimeout sets the request timeout.
func (r *GetServiceContractRequest) SetTimeout(timeout time.Duration) {
	r.Timeout = ToDurationPtr(timeout)
}

// SetRetryCount sets the request retry count.
func (r *GetServiceContractRequest) SetRetryCount(retryCount int) {
	r.RetryCount = &retryCount
}

// GetTimeoutPtr returns the request timeout pointer.
func (r *GetServiceContractRequest) GetTimeoutPtr() *time.Duration {
	return r.Timeout
}

// GetRetryCountPtr returns the request retry count pointer.
func (r *GetServiceContractRequest) GetRetryCountPtr() *int {
	return r.RetryCount
}

// Validate checks the contract identity and call budget.
func (r *GetServiceContractRequest) Validate() error {
	if r == nil {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil, "GetServiceContractRequest can not be nil")
	}
	if strings.TrimSpace(r.Namespace) == "" || strings.TrimSpace(r.Service) == "" {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil,
			"GetServiceContractRequest namespace and service must not be empty")
	}
	if r.Timeout != nil && *r.Timeout < 0 {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil,
			"GetServiceContractRequest timeout must not be negative")
	}
	if r.RetryCount != nil && *r.RetryCount < 0 {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil,
			"GetServiceContractRequest retryCount must not be negative")
	}
	return nil
}

// ServiceContract is a framework-independent service contract.
type ServiceContract struct {
	GetServiceContractRequest

	ID              string
	Revision        string
	Content         string
	ContentDigest   string
	Interfaces      []*InterfaceDescriptor
	ServiceFeatures []*ServiceFeature
	Metadata        map[string]string
	Status          string
	CreateTime      string
	UpdateTime      string
}

// ReportServiceContractRequest is the report form of a ServiceContract.
type ReportServiceContractRequest = ServiceContract

// ReportServiceContractResponse confirms that the server accepted a contract.
type ReportServiceContractResponse struct{}

// Validate checks a contract before it is sent to the server.
func (c *ServiceContract) Validate() error {
	if c == nil {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil, "ServiceContract can not be nil")
	}
	if strings.TrimSpace(c.Namespace) == "" || strings.TrimSpace(c.Service) == "" ||
		strings.TrimSpace(c.Protocol) == "" {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil,
			"ServiceContract namespace, service and protocol must not be empty")
	}
	if c.Timeout != nil && *c.Timeout < 0 {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil, "ServiceContract timeout must not be negative")
	}
	if c.RetryCount != nil && *c.RetryCount < 0 {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil, "ServiceContract retryCount must not be negative")
	}
	if len(c.Content) > MaxServiceContractContentBytes {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil,
			"ServiceContract content exceeds maximum %d bytes", MaxServiceContractContentBytes)
	}
	if len(c.ServiceFeatures) > MaxServiceContractFeatures {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil,
			"ServiceContract has %d features, maximum is %d",
			len(c.ServiceFeatures), MaxServiceContractFeatures)
	}
	if len(c.Interfaces) > MaxServiceContractInterfaces {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil,
			"ServiceContract has %d interfaces, maximum is %d",
			len(c.Interfaces), MaxServiceContractInterfaces)
	}

	totalContentBytes := int64(len(c.Content))
	if err := validateStringMap(c.Metadata, MaxInstanceMetadataEntries, "ServiceContract"); err != nil {
		return err
	}
	for index, feature := range c.ServiceFeatures {
		if feature == nil {
			return NewSDKError(ErrCodeAPIInvalidArgument, nil,
				"ServiceContract feature %d is nil", index)
		}
		if !feature.Type.Valid() {
			return NewSDKError(ErrCodeAPIInvalidArgument, nil,
				"ServiceContract feature %d type %d is invalid", index, feature.Type)
		}
		if strings.TrimSpace(feature.Name) == "" {
			return NewSDKError(ErrCodeAPIInvalidArgument, nil,
				"ServiceContract feature %d name is empty", index)
		}
		if !feature.Status.Valid() {
			return NewSDKError(ErrCodeAPIInvalidArgument, nil,
				"ServiceContract feature %d status %d is invalid", index, feature.Status)
		}
		if len(feature.Content) > MaxServiceFeatureContentBytes {
			return NewSDKError(ErrCodeAPIInvalidArgument, nil,
				"ServiceContract feature %d content exceeds maximum %d bytes",
				index, MaxServiceFeatureContentBytes)
		}
		totalContentBytes += int64(len(feature.Name) + len(feature.Description) + len(feature.Content))
		if totalContentBytes > MaxServiceContractEncodedBytes {
			return NewSDKError(ErrCodeAPIInvalidArgument, nil,
				"ServiceContract encoded content exceeds maximum %d bytes", MaxServiceContractEncodedBytes)
		}
		if utf8.RuneCountInString(feature.Name) > MaxMetadataKeyRunes ||
			utf8.RuneCountInString(feature.Description) > MaxMetadataValueRunes {
			return NewSDKError(ErrCodeAPIInvalidArgument, nil,
				"ServiceContract feature %d name or description is too long", index)
		}
		if feature.Content != "" && !json.Valid([]byte(feature.Content)) {
			return NewSDKError(ErrCodeAPIInvalidArgument, nil,
				"ServiceContract feature %d content must be valid JSON", index)
		}
	}
	for index, descriptor := range c.Interfaces {
		if descriptor == nil {
			return NewSDKError(ErrCodeAPIInvalidArgument, nil,
				"ServiceContract interface %d is nil", index)
		}
		if strings.TrimSpace(descriptor.Method) == "" {
			return NewSDKError(ErrCodeAPIInvalidArgument, nil,
				"ServiceContract interface %d method is empty", index)
		}
		if descriptor.Source < 0 || descriptor.Source > 2 {
			return NewSDKError(ErrCodeAPIInvalidArgument, nil,
				"ServiceContract interface %d source %d is invalid", index, descriptor.Source)
		}
		totalContentBytes += int64(len(descriptor.Name) + len(descriptor.Method) +
			len(descriptor.Path) + len(descriptor.Content))
		if totalContentBytes > MaxServiceContractEncodedBytes {
			return NewSDKError(ErrCodeAPIInvalidArgument, nil,
				"ServiceContract encoded content exceeds maximum %d bytes", MaxServiceContractEncodedBytes)
		}
		if utf8.RuneCountInString(descriptor.Name) > MaxMetadataKeyRunes ||
			utf8.RuneCountInString(descriptor.Method) > MaxMetadataValueRunes ||
			utf8.RuneCountInString(descriptor.Path) > MaxMetadataValueRunes {
			return NewSDKError(ErrCodeAPIInvalidArgument, nil,
				"ServiceContract interface %d field is too long", index)
		}
	}
	return nil
}

// CloneServiceContract returns a deep copy of contract-owned slices and maps.
func CloneServiceContract(contract *ServiceContract) *ServiceContract {
	if contract == nil {
		return nil
	}
	result := *contract
	result.Metadata = cloneStringMapForMCP(contract.Metadata)
	result.ServiceFeatures = make([]*ServiceFeature, 0, len(contract.ServiceFeatures))
	for _, feature := range contract.ServiceFeatures {
		if feature == nil {
			result.ServiceFeatures = append(result.ServiceFeatures, nil)
			continue
		}
		copyFeature := *feature
		result.ServiceFeatures = append(result.ServiceFeatures, &copyFeature)
	}
	result.Interfaces = make([]*InterfaceDescriptor, 0, len(contract.Interfaces))
	for _, descriptor := range contract.Interfaces {
		if descriptor == nil {
			result.Interfaces = append(result.Interfaces, nil)
			continue
		}
		copyDescriptor := *descriptor
		result.Interfaces = append(result.Interfaces, &copyDescriptor)
	}
	return &result
}
