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

package flow

import (
	"strings"
	"time"

	apiservice "github.com/polarismesh/specification/source/go/api/v1/service_manage"
	"google.golang.org/protobuf/proto"

	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
)

// SyncReportServiceContract reports a service contract and returns a success confirmation.
func (e *Engine) SyncReportServiceContract(
	req *model.ReportServiceContractRequest,
) (*model.ReportServiceContractResponse, error) {
	if err := e.checkServiceContractAvailable(); err != nil {
		return nil, err
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	request := serviceContractToProto(req)
	_, err := e.callServiceContractWithRetry(
		func(timeout time.Duration) (*apiservice.ServiceContract, error) {
			return e.contractConnector.ReportServiceContract(request, timeout)
		}, &req.GetServiceContractRequest)
	if err != nil {
		return nil, err
	}
	return &model.ReportServiceContractResponse{}, nil
}

// SyncGetServiceContract queries a service contract.
func (e *Engine) SyncGetServiceContract(
	req *model.GetServiceContractRequest,
) (*model.ServiceRuleResponse, error) {
	if err := e.checkServiceContractAvailable(); err != nil {
		return nil, err
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	name := req.Name
	if name == "" {
		name = req.Service
	}
	contractType := req.Type
	if contractType == "" {
		contractType = name
	}
	response, err := e.callServiceContractWithRetry(
		func(timeout time.Duration) (*apiservice.ServiceContract, error) {
			return e.contractConnector.GetServiceContract(&apiservice.ServiceContract{
				Name:      name,
				Type:      contractType,
				Namespace: req.Namespace,
				Service:   req.Service,
				Protocol:  req.Protocol,
				Version:   req.Version,
			}, timeout)
		}, req)
	if err != nil {
		return nil, err
	}
	if response == nil {
		return nil, model.NewSDKError(model.ErrCodeInvalidResponse, nil,
			"GetServiceContract returned an empty contract")
	}
	if err := validateServiceContractIdentity(response, req); err != nil {
		return nil, err
	}
	contract := serviceContractFromProto(response)
	return &model.ServiceRuleResponse{
		Type:     model.EventServiceContract,
		Service:  model.ServiceKey{Namespace: req.Namespace, Service: req.Service},
		Value:    contract,
		Revision: contract.Revision,
	}, nil
}

func (e *Engine) checkServiceContractAvailable() error {
	if e.contractConnector == nil || !e.configuration.GetProvider().IsReportServiceContractEnable() {
		return model.NewSDKError(model.ErrCodeServiceContractDisabled, nil,
			"provider.reportServiceContractEnable is false or the server connector does not support Service Contract")
	}
	return nil
}

func serviceContractTimeout(req *model.GetServiceContractRequest, e *Engine) time.Duration {
	if req != nil && req.Timeout != nil && *req.Timeout > 0 {
		return *req.Timeout
	}
	return e.configuration.GetGlobal().GetAPI().GetTimeout()
}

func (e *Engine) callServiceContractWithRetry(
	call func(time.Duration) (*apiservice.ServiceContract, error),
	req *model.GetServiceContractRequest,
) (*apiservice.ServiceContract, error) {
	timeout := serviceContractTimeout(req, e)
	if timeout <= 0 {
		timeout = config.DefaultAPIInvokeTimeout
	}
	maxRetry := e.configuration.GetGlobal().GetAPI().GetMaxRetryTimes()
	if req != nil && req.RetryCount != nil {
		maxRetry = *req.RetryCount
	}
	start := time.Now()
	var lastErr error
	for attempt := 0; ; attempt++ {
		remaining := timeout - time.Since(start)
		if remaining <= 0 {
			return nil, model.NewSDKError(model.ErrCodeAPITimeoutError, lastErr,
				"Service Contract request exceeded timeout %v", timeout)
		}
		response, err := call(remaining)
		if err == nil {
			return response, nil
		}
		lastErr = err
		if attempt >= maxRetry || !isServiceContractRetriable(err) {
			return nil, err
		}
		remaining = timeout - time.Since(start)
		if remaining <= 0 {
			return nil, model.NewSDKError(model.ErrCodeAPITimeoutError, err,
				"Service Contract request exceeded timeout %v", timeout)
		}
		interval := e.configuration.GetGlobal().GetAPI().GetRetryInterval()
		if interval > remaining {
			interval = remaining
		}
		if e.logCtx != nil {
			e.logCtx.GetBaseLogger().Warnf(
				"retry Service Contract request after error, attempt=%d maxRetry=%d timeout=%v: %v",
				attempt+1, maxRetry, timeout, err)
		}
		time.Sleep(interval)
	}
}

func isServiceContractRetriable(err error) bool {
	switch model.GetErrorCodeFromError(err) {
	case model.ErrCodeNetworkError, model.ErrCodeConnectError, model.ErrorCodeRpcError,
		model.ErrorCodeRpcTimeout, model.ErrCodeAPITimeoutError:
		return true
	default:
		return false
	}
}

func validateServiceContractIdentity(
	response *apiservice.ServiceContract,
	req *model.GetServiceContractRequest,
) error {
	expectedName := req.Name
	if expectedName == "" {
		expectedName = req.Service
	}
	expectedType := req.Type
	if expectedType == "" {
		expectedType = expectedName
	}
	if response.GetNamespace() != req.Namespace || response.GetService() != req.Service {
		return model.NewSDKError(model.ErrCodeInvalidResponse, nil,
			"GetServiceContract returned contract %s/%s for request %s/%s",
			response.GetNamespace(), response.GetService(), req.Namespace, req.Service)
	}
	if response.GetName() != expectedName || response.GetType() != expectedType {
		return model.NewSDKError(model.ErrCodeInvalidResponse, nil,
			"GetServiceContract returned contract name %q type %q for request name %q type %q",
			response.GetName(), response.GetType(), expectedName, expectedType)
	}
	if req.Protocol != "" && !strings.EqualFold(response.GetProtocol(), req.Protocol) {
		return model.NewSDKError(model.ErrCodeInvalidResponse, nil,
			"GetServiceContract returned protocol %q for request %q",
			response.GetProtocol(), req.Protocol)
	}
	if req.Version != "" && response.GetVersion() != req.Version {
		return model.NewSDKError(model.ErrCodeInvalidResponse, nil,
			"GetServiceContract returned version %q for request %q",
			response.GetVersion(), req.Version)
	}
	return nil
}

func serviceContractToProto(contract *model.ServiceContract) *apiservice.ServiceContract {
	name := contract.Name
	if name == "" {
		name = contract.Service
	}
	contractType := contract.Type
	if contractType == "" {
		contractType = name
	}
	result := &apiservice.ServiceContract{
		Id:            contract.ID,
		Name:          name,
		Namespace:     contract.Namespace,
		Service:       contract.Service,
		Protocol:      contract.Protocol,
		Version:       contract.Version,
		Revision:      contract.Revision,
		Content:       contract.Content,
		ContentDigest: contract.ContentDigest,
		Metadata:      cloneServiceContractStringMap(contract.Metadata),
		Status:        contract.Status,
		Type:          contractType,
		Ctime:         contract.CreateTime,
		Mtime:         contract.UpdateTime,
	}
	result.ServiceFeatures = make([]*apiservice.ServiceFeature, 0, len(contract.ServiceFeatures))
	for _, feature := range contract.ServiceFeatures {
		if feature == nil {
			continue
		}
		result.ServiceFeatures = append(result.ServiceFeatures, &apiservice.ServiceFeature{
			Id:          feature.ID,
			Type:        apiservice.ServiceFeatureType(feature.Type),
			Name:        feature.Name,
			Description: feature.Description,
			Content:     feature.Content,
			Status:      apiservice.ServiceFeatureStatus(feature.Status),
			Ctime:       feature.CreateTime,
			Mtime:       feature.ModifyTime,
		})
	}
	result.Interfaces = make([]*apiservice.InterfaceDescriptor, 0, len(contract.Interfaces))
	for _, descriptor := range contract.Interfaces {
		if descriptor == nil {
			continue
		}
		result.Interfaces = append(result.Interfaces, &apiservice.InterfaceDescriptor{
			Id:            descriptor.ID,
			Method:        descriptor.Method,
			Path:          descriptor.Path,
			Content:       descriptor.Content,
			Source:        apiservice.InterfaceDescriptor_Source(descriptor.Source),
			Revision:      descriptor.Revision,
			Ctime:         descriptor.CreateTime,
			Mtime:         descriptor.UpdateTime,
			Name:          descriptor.Name,
			Namespace:     descriptor.Namespace,
			Service:       descriptor.Service,
			Protocol:      descriptor.Protocol,
			Version:       descriptor.Version,
			Type:          descriptor.Type,
			ContentDigest: descriptor.ContentDigest,
		})
	}
	return result
}

func serviceContractFromProto(contract *apiservice.ServiceContract) *model.ServiceContract {
	if contract == nil {
		return nil
	}
	copyContract := proto.Clone(contract).(*apiservice.ServiceContract)
	result := &model.ServiceContract{
		ID: copyContract.GetId(),
		GetServiceContractRequest: model.GetServiceContractRequest{
			Namespace: copyContract.GetNamespace(),
			Service:   copyContract.GetService(),
			Name:      copyContract.GetName(),
			Type:      copyContract.GetType(),
			Protocol:  copyContract.GetProtocol(),
			Version:   copyContract.GetVersion(),
		},
		Revision:      copyContract.GetRevision(),
		Content:       copyContract.GetContent(),
		ContentDigest: copyContract.GetContentDigest(),
		Metadata:      cloneServiceContractStringMap(copyContract.GetMetadata()),
		Status:        copyContract.GetStatus(),
		CreateTime:    copyContract.GetCtime(),
		UpdateTime:    copyContract.GetMtime(),
	}
	result.ServiceFeatures = make([]*model.ServiceFeature, 0, len(copyContract.GetServiceFeatures()))
	for _, feature := range copyContract.GetServiceFeatures() {
		if feature == nil {
			continue
		}
		result.ServiceFeatures = append(result.ServiceFeatures, &model.ServiceFeature{
			ID:          feature.GetId(),
			Type:        model.ServiceFeatureType(feature.GetType()),
			Name:        feature.GetName(),
			Description: feature.GetDescription(),
			Content:     feature.GetContent(),
			Status:      model.ServiceFeatureStatus(feature.GetStatus()),
			CreateTime:  feature.GetCtime(),
			ModifyTime:  feature.GetMtime(),
		})
	}
	result.Interfaces = make([]*model.InterfaceDescriptor, 0, len(copyContract.GetInterfaces()))
	for _, descriptor := range copyContract.GetInterfaces() {
		if descriptor == nil {
			continue
		}
		result.Interfaces = append(result.Interfaces, &model.InterfaceDescriptor{
			ID:            descriptor.GetId(),
			Name:          descriptor.GetName(),
			Method:        descriptor.GetMethod(),
			Path:          descriptor.GetPath(),
			Content:       descriptor.GetContent(),
			Source:        int32(descriptor.GetSource()),
			Revision:      descriptor.GetRevision(),
			CreateTime:    descriptor.GetCtime(),
			UpdateTime:    descriptor.GetMtime(),
			Namespace:     descriptor.GetNamespace(),
			Service:       descriptor.GetService(),
			Protocol:      descriptor.GetProtocol(),
			Version:       descriptor.GetVersion(),
			Type:          descriptor.GetType(),
			ContentDigest: descriptor.GetContentDigest(),
		})
	}
	return result
}

func cloneServiceContractStringMap(value map[string]string) map[string]string {
	if value == nil {
		return nil
	}
	result := make(map[string]string, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}
