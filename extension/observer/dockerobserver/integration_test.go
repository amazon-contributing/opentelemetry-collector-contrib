// Copyright The OpenTelemetry Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build integration
// +build integration

package dockerobserver

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.opentelemetry.io/collector/component"
	"go.uber.org/zap"

	"github.com/open-telemetry/opentelemetry-collector-contrib/extension/observer"
)

type testHost struct {
	component.Host
	t *testing.T
}

// ReportFatalError causes the test to be run to fail.
func (h *testHost) ReportFatalError(err error) {
	h.t.Fatalf("Receiver reported a fatal error: %v", err)
}

var _ component.Host = (*testHost)(nil)

func TestObserverEmitsEndpointsIntegration(t *testing.T) {
	t.Skip("Skipping integration test")
	image := "docker.io/library/nginx"
	tag := "1.17"

	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image:        fmt.Sprintf("%s:%s", image, tag),
		ExposedPorts: []string{"80/tcp"},
		WaitingFor:   wait.ForListeningPort("80/tcp"),
		SkipReaper:   true, // skipping the reaper to avoid creating two endpoints
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.Nil(t, err)
	defer func() {
		err := container.Terminate(ctx)
		require.Nil(t, err)
	}()
	require.NotNil(t, container)

	config := NewFactory().CreateDefaultConfig().(*Config)
	config.CacheSyncInterval = 1 * time.Second
	config.UseHostBindings = true
	config.UseHostnameIfPresent = true
	mn := &mockNotifier{endpointsMap: map[observer.EndpointID]observer.Endpoint{}}
	obvs := startObserverWithConfig(t, mn, config)
	defer stopObserver(t, obvs)
	require.Eventually(t, func() bool { return mn.AddCount() == 1 }, 3*time.Second, 10*time.Millisecond)
	endpoints := mn.EndpointsMap()
	require.Equal(t, len(endpoints), 1)
	for _, e := range endpoints {
		require.Equal(t, uint16(80), e.Details.Env()["alternate_port"])
		require.Equal(t, container.GetContainerID(), e.Details.Env()["container_id"])
		require.Equal(t, image, e.Details.Env()["image"])
		require.Equal(t, tag, e.Details.Env()["tag"])
	}
}

func TestObserverUpdatesEndpointsIntegration(t *testing.T) {
	t.Skip("Skipping integration test")
	image := "docker.io/library/nginx"
	tag := "1.17"

	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image:        fmt.Sprintf("%s:%s", image, tag),
		ExposedPorts: []string{"80/tcp"},
		WaitingFor:   wait.ForListeningPort("80/tcp"),
		SkipReaper:   true, // skipping the reaper to avoid creating two endpoints
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.Nil(t, err)
	defer func() {
		err = container.Terminate(ctx)
		require.Nil(t, err)
	}()
	require.NotNil(t, container)

	mn := &mockNotifier{endpointsMap: map[observer.EndpointID]observer.Endpoint{}}
	obvs := startObserver(t, mn)
	defer stopObserver(t, obvs)
	require.Eventually(t, func() bool { return mn.AddCount() == 1 }, 3*time.Second, 10*time.Millisecond)
	endpoints := mn.EndpointsMap()
	require.Equal(t, len(endpoints), 1)
	for _, e := range endpoints {
		require.Equal(t, uint16(80), e.Details.Env()["port"])
		require.Equal(t, container.GetContainerID(), e.Details.Env()["container_id"])
		require.Equal(t, image, e.Details.Env()["image"])
		require.Equal(t, tag, e.Details.Env()["tag"])
	}

	tcDockerClient, _, _, err := testcontainers.NewDockerClient()
	require.Nil(t, err)

	require.NoError(t, tcDockerClient.ContainerRename(context.Background(), container.GetContainerID(), "nginx-updated"))

	require.Eventually(t, func() bool { return mn.ChangeCount() == 1 }, 3*time.Second, 10*time.Millisecond)
	require.Equal(t, 1, mn.AddCount())

	endpoints = mn.EndpointsMap()
	for _, e := range endpoints {
		require.Equal(t, "nginx-updated", e.Details.Env()["name"])
		require.Equal(t, uint16(80), e.Details.Env()["port"])
		require.Equal(t, container.GetContainerID(), e.Details.Env()["container_id"])
		require.Equal(t, image, e.Details.Env()["image"])
		require.Equal(t, tag, e.Details.Env()["tag"])
	}
}

func TestObserverRemovesEndpointsIntegration(t *testing.T) {
	t.Skip("Skipping integration test")
	image := "docker.io/library/nginx"
	tag := "1.17"

	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image:        fmt.Sprintf("%s:%s", image, tag),
		ExposedPorts: []string{"80/tcp"},
		WaitingFor:   wait.ForListeningPort("80/tcp"),
		SkipReaper:   true, // skipping the reaper to avoid creating two endpoints
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.Nil(t, err)
	require.NotNil(t, container)

	mn := &mockNotifier{endpointsMap: map[observer.EndpointID]observer.Endpoint{}}
	obvs := startObserver(t, mn)
	defer stopObserver(t, obvs)
	require.Eventually(t, func() bool { return mn.AddCount() == 1 }, 3*time.Second, 10*time.Millisecond)
	endpoints := mn.EndpointsMap()
	require.Equal(t, len(endpoints), 1)
	for _, e := range endpoints {
		require.Equal(t, uint16(80), e.Details.Env()["port"])
		require.Equal(t, container.GetContainerID(), e.Details.Env()["container_id"])
		require.Equal(t, image, e.Details.Env()["image"])
		require.Equal(t, tag, e.Details.Env()["tag"])
	}

	err = container.Terminate(ctx)
	require.Nil(t, err)

	require.Eventually(t, func() bool { return mn.RemoveCount() == 1 }, 3*time.Second, 10*time.Millisecond)
	require.Empty(t, mn.EndpointsMap())
}

func TestObserverExcludesImagesIntegration(t *testing.T) {
	t.Skip("Skipping integration test")
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image:        "docker.io/library/nginx:1.17",
		ExposedPorts: []string{"80/tcp"},
		WaitingFor:   wait.ForListeningPort("80/tcp"),
		SkipReaper:   true,
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.Nil(t, err)
	defer func() {
		err := container.Terminate(ctx)
		require.Nil(t, err)
	}()
	require.NotNil(t, container)

	config := NewFactory().CreateDefaultConfig().(*Config)
	config.ExcludedImages = []string{"*nginx*"}

	mn := &mockNotifier{endpointsMap: map[observer.EndpointID]observer.Endpoint{}}
	obvs := startObserverWithConfig(t, mn, config)
	defer stopObserver(t, obvs)
	time.Sleep(2 * time.Second) // wait for endpoints to sync
	require.Equal(t, 0, mn.AddCount())
	require.Equal(t, 0, mn.ChangeCount())
	require.Empty(t, mn.EndpointsMap())
}

func startObserver(t *testing.T, listener observer.Notify) *dockerObserver {
	config := NewFactory().CreateDefaultConfig().(*Config)
	require.NoError(t, config.Validate())
	return startObserverWithConfig(t, listener, config)
}

func startObserverWithConfig(t *testing.T, listener observer.Notify, c *Config) *dockerObserver {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ext, err := newObserver(zap.NewNop(), c)
	require.NoError(t, err)
	require.NotNil(t, ext)

	obvs, ok := ext.(*dockerObserver)
	require.True(t, ok)
	require.NoError(t, err, "failed creating extension")
	require.NoError(t, obvs.Start(ctx, &testHost{
		t: t,
	}))

	go obvs.ListAndWatch(listener)
	return obvs
}

func stopObserver(t *testing.T, obvs *dockerObserver) {
	assert.NoError(t, obvs.Shutdown(context.Background()))
}

var _ observer.Notify = (*mockNotifier)(nil)

type mockNotifier struct {
	sync.Mutex
	endpointsMap map[observer.EndpointID]observer.Endpoint
	addCount     int
	removeCount  int
	changeCount  int
}

func (m *mockNotifier) ID() observer.NotifyID {
	return "mockNotifier"
}

func (m *mockNotifier) AddCount() int {
	m.Lock()
	defer m.Unlock()
	return m.addCount
}

func (m *mockNotifier) ChangeCount() int {
	m.Lock()
	defer m.Unlock()
	return m.changeCount
}

func (m *mockNotifier) RemoveCount() int {
	m.Lock()
	defer m.Unlock()
	return m.removeCount
}

func (m *mockNotifier) EndpointsMap() map[observer.EndpointID]observer.Endpoint {
	m.Lock()
	defer m.Unlock()
	return m.endpointsMap
}

func (m *mockNotifier) OnAdd(added []observer.Endpoint) {
	m.Lock()
	defer m.Unlock()
	m.addCount++
	for _, e := range added {
		m.endpointsMap[e.ID] = e
	}
}

func (m *mockNotifier) OnRemove(removed []observer.Endpoint) {
	m.Lock()
	defer m.Unlock()
	m.removeCount++
	for _, e := range removed {
		delete(m.endpointsMap, e.ID)
	}
}

func (m *mockNotifier) OnChange(changed []observer.Endpoint) {
	m.Lock()
	defer m.Unlock()
	m.changeCount++
	for _, e := range changed {
		m.endpointsMap[e.ID] = e
	}
}
