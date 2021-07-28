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

package grpc

import (
	"context"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/network"
	"google.golang.org/grpc"
	"google.golang.org/grpc/stats"
	"strings"
	"time"
)

//创建连接
func (g *Connector) CreateConnection(
	address string, timeout time.Duration, clientInfo *network.ClientInfo) (network.ClosableConn, error) {
	var opts []grpc.DialOption
	opts = append(opts, grpc.WithInsecure())
	opts = append(opts, grpc.WithBlock())
	localIPValue := clientInfo.GetIPString()
	if len(localIPValue) == 0 {
		opts = append(opts, grpc.WithStatsHandler(&statHandler{clientInfo: clientInfo}))
	}
	log.GetBaseLogger().Debugf("create connection with maxCallRecvSize %d", g.cfg.MaxCallRecvMsgSize)
	opts = append(opts, grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(g.cfg.MaxCallRecvMsgSize)))
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	conn, err := grpc.DialContext(ctx, address, opts...)
	if nil != err {
		return nil, err
	}
	return conn, nil
}

// Handler defines the interface for the related stats handling (e.g., RPCs, connections).
type statHandler struct {
	//全局上下文
	clientInfo *network.ClientInfo
}

// TagRPC can attach some information to the given context.
// The context used for the rest lifetime of the RPC will be derived from
// the returned context.
func (s *statHandler) TagRPC(ctx context.Context, info *stats.RPCTagInfo) context.Context {
	return ctx
}

// HandleRPC processes the RPC stats.
func (s *statHandler) HandleRPC(context.Context, stats.RPCStats) {

}

// TagConn can attach some information to the given context.
// The returned context will be used for stats handling.
// For conn stats handling, the context used in HandleConn for this
// connection will be derived from the context returned.
// For RPC stats handling,
//  - On server side, the context used in HandleRPC for all RPCs on this
// connection will be derived from the context returned.
//  - On client side, the context is not derived from the context returned.
func (s *statHandler) TagConn(ctx context.Context, info *stats.ConnTagInfo) context.Context {
	localAddr := info.LocalAddr.String()
	localIP := strings.Split(localAddr, ":")[0]
	hashValue, _ := model.HashStr(localIP)
	log.GetBaseLogger().Infof(
		"localAddress from connection is %s, IP is %s, hashValue is %d", localAddr, localIP, hashValue)
	s.clientInfo.IP.Store(localIP)
	s.clientInfo.HashKey.Store([]byte(localAddr))
	return ctx
}

// HandleConn processes the Conn stats.
func (s *statHandler) HandleConn(context.Context, stats.ConnStats) {

}
