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

package grpc

import (
	"context"
	"testing"
	"time"

	apiservice "github.com/polarismesh/specification/source/go/api/v1/service_manage"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type blockingClientEventStream struct {
	apiservice.PolarisGRPC_WatchClientEventsClient
	sendStarted chan struct{}
	releaseSend chan struct{}
	closeSend   chan struct{}
}

type cancelableClientEventStream struct {
	apiservice.PolarisGRPC_WatchClientEventsClient
	ctx     context.Context
	started chan struct{}
}

func (s *cancelableClientEventStream) Send(*apiservice.ClientEvent) error {
	close(s.started)
	<-s.ctx.Done()
	return s.ctx.Err()
}

func (s *cancelableClientEventStream) CloseSend() error {
	return nil
}

func (s *blockingClientEventStream) Send(*apiservice.ClientEvent) error {
	close(s.sendStarted)
	<-s.releaseSend
	return nil
}

func (s *blockingClientEventStream) CloseSend() error {
	close(s.closeSend)
	return nil
}

func TestClientEventStreamCloseWaitsForSend(t *testing.T) {
	fake := &blockingClientEventStream{
		sendStarted: make(chan struct{}),
		releaseSend: make(chan struct{}),
		closeSend:   make(chan struct{}),
	}
	stream := &clientEventStream{
		PolarisGRPC_WatchClientEventsClient: fake,
		cancel:                              func() {},
	}

	sendDone := make(chan error, 1)
	go func() {
		sendDone <- stream.Send(&apiservice.ClientEvent{
			Type:     apiservice.ClientEvent_WATCH,
			ClientId: "client",
		})
	}()
	<-fake.sendStarted

	closeDone := make(chan error, 1)
	go func() { closeDone <- stream.Close() }()
	select {
	case <-fake.closeSend:
		t.Fatal("CloseSend ran concurrently with Send")
	case <-time.After(20 * time.Millisecond):
	}

	close(fake.releaseSend)
	require.NoError(t, <-sendDone)
	require.NoError(t, <-closeDone)
	<-fake.closeSend

	err := stream.Send(&apiservice.ClientEvent{})
	require.Error(t, err)
	require.Equal(t, codes.Canceled, status.Code(err))
}

func TestClientEventStreamCloseCancelsBlockedSend(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fake := &cancelableClientEventStream{ctx: ctx, started: make(chan struct{})}
	stream := &clientEventStream{
		PolarisGRPC_WatchClientEventsClient: fake,
		cancel:                              cancel,
	}

	sendDone := make(chan error, 1)
	go func() {
		sendDone <- stream.Send(&apiservice.ClientEvent{ClientId: "client"})
	}()
	<-fake.started

	closeDone := make(chan error, 1)
	go func() { closeDone <- stream.Close() }()
	select {
	case <-closeDone:
		require.Error(t, <-sendDone)
	case <-time.After(250 * time.Millisecond):
		t.Fatal("Close did not cancel a blocked Send within 250ms")
	}
}
