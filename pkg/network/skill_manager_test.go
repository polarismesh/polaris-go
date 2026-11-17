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
// Unless required by applicable law or agreed to in writing, software distributed under
// the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND,
// either express or implied. See the License for the specific language governing permissions and
// limitations under the License.

package network

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/sdk"
)

type closableTestConn struct {
	mutex  sync.Mutex
	closed bool
}

func (c *closableTestConn) Close() error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.closed = true
	return nil
}

func (c *closableTestConn) isClosed() bool {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.closed
}

type skillTestCreator struct {
	started chan struct{}
	release chan struct{}
	conn    ClosableConn
}

func (c *skillTestCreator) Name() string { return "skill-test" }

func (c *skillTestCreator) CreateConnection(
	string, time.Duration, *ClientInfo,
) (ClosableConn, error) {
	close(c.started)
	if c.release != nil {
		<-c.release
	}
	return c.conn, nil
}

func newSkillTestManager(t *testing.T, creator ConnCreator) ConnectionManager {
	t.Helper()
	log.SetNetworkLogger(noopLogger{})
	cfg := config.NewDefaultConfiguration([]string{"127.0.0.1:8091"})
	cfg.GetSkill().SetEnable(true)
	cfg.GetSkill().SetAddresses([]string{"127.0.0.1:18094"})
	cfg.GetSkill().SetConnectTimeout(time.Second)
	manager, err := NewSkillConnectionManager(cfg, sdk.NewValueContext())
	require.NoError(t, err)
	manager.SetConnCreator(creator)
	return manager
}

func TestSkillManagerDestroyClosesLateDial(t *testing.T) {
	conn := &closableTestConn{}
	creator := &skillTestCreator{
		started: make(chan struct{}),
		release: make(chan struct{}),
		conn:    conn,
	}
	manager := newSkillTestManager(t, creator)
	result := make(chan error, 1)
	go func() {
		_, err := manager.GetConnection("skill-test", config.SkillCluster)
		result <- err
	}()

	<-creator.started
	manager.Destroy()
	close(creator.release)

	select {
	case err := <-result:
		require.Error(t, err)
		assert.Contains(t, err.Error(), "destroyed")
	case <-time.After(time.Second):
		t.Fatal("GetConnection did not return after Destroy")
	}
	assert.True(t, conn.isClosed())
}

func TestSkillManagerDestroyClosesCurrentAndRejectsNewConnection(t *testing.T) {
	conn := &closableTestConn{}
	manager := newSkillTestManager(t, &skillTestCreator{
		started: make(chan struct{}),
		conn:    conn,
	})

	connection, err := manager.GetConnection("skill-test", config.SkillCluster)
	require.NoError(t, err)
	manager.Destroy()

	assert.False(t, IsAvailableConnection(connection))
	assert.True(t, conn.isClosed())
	_, err = manager.GetConnection("skill-test", config.SkillCluster)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "destroyed")
}

func TestSkillManagerDestroyClosesRetiringConnection(t *testing.T) {
	conn := &closableTestConn{}
	manager := newSkillTestManager(t, &skillTestCreator{started: make(chan struct{}), conn: conn})
	connection, err := manager.GetConnection("active-download", config.SkillCluster)
	require.NoError(t, err)
	defer connection.Release("active-download")
	manager.ReportConnectionDown(connection.ConnID)
	manager.Destroy()
	assert.False(t, IsAvailableConnection(connection))
	assert.True(t, conn.isClosed())
}

type sequentialSkillCreator struct {
	connections []ClosableConn
	next        int
}

func (c *sequentialSkillCreator) Name() string { return "skill-sequence" }

func (c *sequentialSkillCreator) CreateConnection(string, time.Duration, *ClientInfo) (ClosableConn, error) {
	conn := c.connections[c.next]
	c.next++
	return conn, nil
}

func TestSkillManagerDestroyClosesReplacedConnection(t *testing.T) {
	first, second := &closableTestConn{}, &closableTestConn{}
	manager := newSkillTestManager(t, &sequentialSkillCreator{connections: []ClosableConn{first, second}})
	connection, err := manager.GetConnection("old-download", config.SkillCluster)
	require.NoError(t, err)
	defer connection.Release("old-download")
	manager.ReportConnectionDown(connection.ConnID)
	next, err := manager.GetConnection("new-download", config.SkillCluster)
	require.NoError(t, err)
	defer next.Release("new-download")
	assert.False(t, first.isClosed())
	manager.Destroy()
	assert.True(t, first.isClosed())
	assert.True(t, second.isClosed())
	assert.Empty(t, manager.(*connectionManager).connections)
	manager.Destroy()
}

func TestSkillManagerForgetsClosedConnection(t *testing.T) {
	conn := &closableTestConn{}
	manager := newSkillTestManager(t, &skillTestCreator{started: make(chan struct{}), conn: conn})
	defer manager.Destroy()
	connection, err := manager.GetConnection("download", config.SkillCluster)
	require.NoError(t, err)
	manager.ReportConnectionDown(connection.ConnID)
	connection.Release("download")
	assert.True(t, conn.isClosed())
	assert.Empty(t, manager.(*connectionManager).connections)
}
