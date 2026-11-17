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

package polaris

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	apimodel "github.com/polarismesh/specification/source/go/api/v1/model"
	skillmanage "github.com/polarismesh/specification/source/go/api/v1/skill_manage"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/network"
	"github.com/polarismesh/polaris-go/pkg/plugin/skillconnector"
)

const (
	skillRequestIDPrefixGet      = 51
	skillRequestIDPrefixList     = 52
	skillRequestIDPrefixDownload = 53
	opKeySkillGet                = "SkillGet"
	opKeySkillList               = "SkillList"
	opKeySkillDownload           = "SkillDownload"
	headerAuthToken              = "X-Polaris-Token"
	headerRequestID              = "request-id"
)

// GetSkill 获取 published Skill。
func (c *Connector) GetSkill(req *skillconnector.GetSkillRequest) (*skillconnector.GetSkillResponse, error) {
	conn, err := c.connManager.GetConnection(opKeySkillGet, configSkillCluster())
	if err != nil {
		return nil, model.NewSDKError(model.ErrCodeNetworkError, err, "fail to get skill connection")
	}
	defer conn.Release(opKeySkillGet)
	start := time.Now()
	ctx, cancel := c.requestContext(skillRequestIDPrefixGet, c.messageTimeout())
	defer cancel()
	resp, err := skillmanage.NewPolarisSkillClient(network.ToGRPCConn(conn.Conn)).GetSkill(ctx,
		&skillmanage.GetSkillRequest{
			Name:      req.Name,
			Namespace: req.Namespace,
			Version:   req.Version,
		})
	if err != nil {
		return nil, c.rpcError(conn, err, start, "fail to call PolarisSkill.GetSkill")
	}
	if resp.GetCode() != uint32(apimodel.Code_ExecuteSuccess) {
		return nil, skillServerError(resp.GetCode(), resp.GetInfo(), "PolarisSkill.GetSkill returned an error")
	}
	return &skillconnector.GetSkillResponse{
		Resource: convertResource(resp.GetResource()),
		Version:  convertResourceVersion(resp.GetVersion()),
		Content:  resp.GetContent(),
	}, nil
}

// GetSkillList 获取 published Skill 列表。
func (c *Connector) GetSkillList(req *skillconnector.ListSkillsRequest) (*skillconnector.ListSkillsResponse, error) {
	conn, err := c.connManager.GetConnection(opKeySkillList, configSkillCluster())
	if err != nil {
		return nil, model.NewSDKError(model.ErrCodeNetworkError, err, "fail to get skill connection")
	}
	defer conn.Release(opKeySkillList)
	start := time.Now()
	ctx, cancel := c.requestContext(skillRequestIDPrefixList, c.messageTimeout())
	defer cancel()
	resp, err := skillmanage.NewPolarisSkillClient(network.ToGRPCConn(conn.Conn)).GetSkillList(ctx,
		&skillmanage.ListSkillsRequest{
			Namespace: req.Namespace,
			Name:      req.Name,
			Tag:       req.Tag,
			Owner:     req.Owner,
			Scope:     req.Scope,
			Keyword:   req.Keyword,
			OrderBy:   req.OrderBy,
			OrderType: req.OrderType,
			Offset:    req.Offset,
			Limit:     req.Limit,
		})
	if err != nil {
		return nil, c.rpcError(conn, err, start, "fail to call PolarisSkill.GetSkillList")
	}
	if resp.GetCode() != uint32(apimodel.Code_ExecuteSuccess) {
		return nil, skillServerError(resp.GetCode(), resp.GetInfo(), "PolarisSkill.GetSkillList returned an error")
	}
	result := &skillconnector.ListSkillsResponse{
		Total:     resp.GetTotal(),
		Resources: make([]*skillconnector.Resource, 0, len(resp.GetResources())),
	}
	for _, resource := range resp.GetResources() {
		result.Resources = append(result.Resources, convertResource(resource))
	}
	return result, nil
}

// DownloadSkill 下载 Skill 并逐帧交给 handler。
func (c *Connector) DownloadSkill(
	req *skillconnector.DownloadRequest, handler skillconnector.DownloadHandler,
) error {
	conn, err := c.connManager.GetConnection(opKeySkillDownload, configSkillCluster())
	if err != nil {
		return model.NewSDKError(model.ErrCodeNetworkError, err, "fail to get skill connection")
	}
	defer conn.Release(opKeySkillDownload)
	start := time.Now()
	ctx, cancel := c.requestContext(skillRequestIDPrefixDownload, c.downloadRequestTimeout(req.Format))
	defer cancel()
	stream, err := skillmanage.NewPolarisSkillClient(network.ToGRPCConn(conn.Conn)).DownloadSkill(ctx,
		&skillmanage.DownloadSkillRequest{
			Name:      req.Name,
			Namespace: req.Namespace,
			Version:   req.Version,
			Tag:       req.Tag,
			Format:    req.Format,
		})
	if err != nil {
		return c.rpcError(conn, err, start, "fail to call PolarisSkill.DownloadSkill")
	}
	for {
		resp, recvErr := stream.Recv()
		if recvErr == io.EOF {
			c.connManager.ReportSuccess(conn.ConnID, int32(model.ErrCodeSuccess), time.Since(start))
			return nil
		}
		if recvErr != nil {
			return c.rpcError(conn, recvErr, start,
				"fail to receive PolarisSkill.DownloadSkill stream")
		}
		if resp.GetCode() != uint32(apimodel.Code_ExecuteSuccess) {
			return skillServerError(resp.GetCode(), resp.GetInfo(), "PolarisSkill.DownloadSkill returned an error")
		}
		if err = handler(&skillconnector.DownloadChunk{
			Version:     resp.GetVersion(),
			ContentType: resp.GetContentType(),
			Content:     resp.GetContent(),
			ZipChunk:    append([]byte(nil), resp.GetZipChunk()...),
			TotalSize:   resp.GetTotalSize(),
			Filename:    resp.GetFilename(),
		}); err != nil {
			cancel()
			return err
		}
	}
}

func (c *Connector) requestContext(
	prefix int, timeout time.Duration,
) (ctx context.Context, cancel context.CancelFunc) {
	headers := metadata.Pairs(
		headerRequestID, fmt.Sprintf("%d%d", prefix, uuid.New().ID()),
		headerAuthToken, c.token,
	)
	ctx, cancel = context.WithTimeout(context.Background(), timeout)
	return metadata.NewOutgoingContext(ctx, headers), cancel
}

func (c *Connector) messageTimeout() time.Duration {
	return c.requestTimeout
}

func (c *Connector) downloadRequestTimeout(format string) time.Duration {
	if strings.EqualFold(format, model.SkillFormatZIP) && c.downloadTimeout > 0 {
		return c.downloadTimeout
	}
	return c.messageTimeout()
}

func (c *Connector) reportFailure(conn *network.Connection, err error, start time.Time) {
	c.connManager.ReportFail(conn.ConnID, int32(model.ErrCodeNetworkError), time.Since(start))
	c.connManager.ReportConnectionDown(conn.ConnID)
	c.logCtx.GetNetworkLogger().Warnf("skill request failed on connection %s: %v", conn.ConnID, err)
}

func (c *Connector) rpcError(
	conn *network.Connection, err error, start time.Time, message string,
) model.SDKError {
	if isSkillConnectionError(err) {
		c.reportFailure(conn, err, start)
		return model.NewSDKError(model.ErrCodeNetworkError, err, message)
	}
	c.logCtx.GetNetworkLogger().Warnf("skill request rejected on connection %s: %v", conn.ConnID, err)
	switch status.Code(err) {
	case codes.PermissionDenied, codes.Unauthenticated:
		return model.NewSDKError(model.ErrCodeUnauthorized, err, message)
	case codes.InvalidArgument, codes.OutOfRange, codes.FailedPrecondition:
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, err, message)
	case codes.NotFound:
		return model.NewSDKError(model.ErrCodeSkillNotFound, err, message)
	default:
		return model.NewSDKError(model.ErrCodeServerException, err, message)
	}
}

func isSkillConnectionError(err error) bool {
	if _, ok := status.FromError(err); !ok {
		return true
	}
	switch status.Code(err) {
	case codes.Canceled, codes.DeadlineExceeded, codes.Unavailable:
		return true
	default:
		return false
	}
}

func configSkillCluster() config.ClusterType {
	return config.SkillCluster
}

func skillServerError(code uint32, info, message string) model.SDKError {
	if code == uint32(apimodel.Code_NotFoundResource) {
		return model.NewSDKErrorWithServerInfo(model.ErrCodeSkillNotFound, nil, code, info, message)
	}
	return model.NewServerSDKError(code, info, nil, message)
}
