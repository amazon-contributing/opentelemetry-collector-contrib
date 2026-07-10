// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package cadvisor

import (
	"errors"
	"net/http"
	"testing"

	"github.com/google/cadvisor/cache/memory"
	"github.com/google/cadvisor/container"
	"github.com/google/cadvisor/fs"
	info "github.com/google/cadvisor/info/v1"
	"github.com/google/cadvisor/manager"
	"github.com/google/cadvisor/utils/sysfs"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/cadvisor/testutils"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/stores"
)

type mockCadvisorManager struct {
	t *testing.T
}

// Start the manager. Calling other manager methods before this returns
// may produce undefined behavior.
func (*mockCadvisorManager) Start() error {
	return nil
}

// Get information about all subcontainers of the specified container (includes self).
func (m *mockCadvisorManager) SubcontainersInfo(_ string, _ *info.ContainerInfoRequest) ([]*info.ContainerInfo, error) {
	containerInfos := testutils.LoadContainerInfo(m.t, "./extractors/testdata/CurInfoContainer.json")
	return containerInfos, nil
}

type mockCadvisorManager2 struct{}

func (*mockCadvisorManager2) Start() error {
	return errors.New("new error")
}

func (*mockCadvisorManager2) SubcontainersInfo(_ string, _ *info.ContainerInfoRequest) ([]*info.ContainerInfo, error) {
	return nil, nil
}

func newMockCreateManager(t *testing.T) createCadvisorManager {
	return func(_ *memory.InMemoryCache, _ sysfs.SysFs, _ manager.HousekeepingConfig,
		_ container.MetricSet, _ *http.Client, _ []string,
		_ string,
	) (cadvisorManager, error) {
		return &mockCadvisorManager{t: t}, nil
	}
}

var mockCreateManager2 = func(_ *memory.InMemoryCache, _ sysfs.SysFs, _ manager.HousekeepingConfig,
	_ container.MetricSet, _ *http.Client, _ []string,
	_ string,
) (cadvisorManager, error) {
	return &mockCadvisorManager2{}, nil
}

var mockCreateManagerWithError = func(_ *memory.InMemoryCache, _ sysfs.SysFs, _ manager.HousekeepingConfig,
	_ container.MetricSet, _ *http.Client, _ []string,
	_ string,
) (cadvisorManager, error) {
	return nil, errors.New("error")
}

type MockDecorator struct{}

func (*MockDecorator) Decorate(metric stores.CIMetric) stores.CIMetric {
	return metric
}

func (*MockDecorator) Shutdown() error {
	return nil
}

func TestGetMetrics(t *testing.T) {
	hostInfo := testutils.MockHostInfo{ClusterName: "cluster"}
	decoratorOption := WithDecorator(&MockDecorator{})

	c, err := New("eks", hostInfo, zap.NewNop(), cadvisorManagerCreator(newMockCreateManager(t)), decoratorOption)
	assert.NotNil(t, c)
	assert.NoError(t, err)
	assert.NotNil(t, c.GetMetrics())
	assert.NoError(t, c.Shutdown())
}

func TestGetMetricsNoClusterName(t *testing.T) {
	hostInfo := testutils.MockHostInfo{}
	decoratorOption := WithDecorator(&MockDecorator{})

	c, err := New("eks", hostInfo, zap.NewNop(), cadvisorManagerCreator(newMockCreateManager(t)), decoratorOption)
	assert.NotNil(t, c)
	assert.NoError(t, err)
	assert.Nil(t, c.GetMetrics())
	assert.NoError(t, c.Shutdown())
}

func TestGetMetricsErrorWhenCreatingManager(t *testing.T) {
	hostInfo := testutils.MockHostInfo{ClusterName: "cluster"}
	decoratorOption := WithDecorator(&MockDecorator{})

	c, err := New("eks", hostInfo, zap.NewNop(), cadvisorManagerCreator(mockCreateManagerWithError), decoratorOption)
	assert.Nil(t, c)
	assert.Error(t, err)
}

func TestGetMetricsErrorWhenCallingManagerStart(t *testing.T) {
	hostInfo := testutils.MockHostInfo{ClusterName: "cluster"}
	decoratorOption := WithDecorator(&MockDecorator{})

	c, err := New("eks", hostInfo, zap.NewNop(), cadvisorManagerCreator(mockCreateManager2), decoratorOption)
	assert.Nil(t, c)
	assert.Error(t, err)
}

// TestFilesystemPluginsRegistered guards the cAdvisor filesystem-plugin blank imports in
// cadvisor_linux.go. Without them the plugin registry is empty, all mounts are dropped, and
// node_filesystem_* / container_filesystem_* metrics are not emitted. Each fsType maps to the
// /install import that must register a handler for it; removing an import makes its lookup nil
// and fails this test.
func TestFilesystemPluginsRegistered(t *testing.T) {
	// fsType -> the /install blank import that must register a handler for it.
	cases := map[string]string{
		"xfs":     "github.com/google/cadvisor/fs/vfs/install",     // AL2023 root device
		"ext4":    "github.com/google/cadvisor/fs/vfs/install",     // common block fs
		"overlay": "github.com/google/cadvisor/fs/overlay/install", // container overlay fs
		"tmpfs":   "github.com/google/cadvisor/fs/tmpfs/install",   // tmpfs mounts
	}
	for fsType, requiredImport := range cases {
		assert.NotNilf(t, fs.GetPluginForFsType(fsType),
			"no cAdvisor fs plugin registered for FSType %q; the blank import %q is likely "+
				"missing from cadvisor_linux.go",
			fsType, requiredImport)
	}
}
