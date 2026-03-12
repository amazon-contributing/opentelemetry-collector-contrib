// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package kubelet

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestNewClient_Defaults(t *testing.T) {
	c := NewClient(zap.NewNop())
	assert.Equal(t, defaultSocketPath, c.socketPath)
	assert.NotNil(t, c.resourceNames)
	assert.NotNil(t, c.deviceToPod)
}

func TestNewClient_WithSocketPath(t *testing.T) {
	c := NewClient(zap.NewNop(), WithSocketPath("/custom/path"))
	assert.Equal(t, "/custom/path", c.socketPath)
}

func TestNewClient_WithEmptySocketPath(t *testing.T) {
	c := NewClient(zap.NewNop(), WithSocketPath(""))
	assert.Equal(t, defaultSocketPath, c.socketPath)
}

func TestAddResourceName(t *testing.T) {
	c := NewClient(zap.NewNop())
	c.AddResourceName("nvidia.com/gpu")
	c.AddResourceName("aws.amazon.com/neuron")
	assert.Len(t, c.resourceNames, 2)
	_, ok := c.resourceNames["nvidia.com/gpu"]
	assert.True(t, ok)
}

func TestAddResourceName_Dedup(t *testing.T) {
	c := NewClient(zap.NewNop())
	c.AddResourceName("nvidia.com/gpu")
	c.AddResourceName("nvidia.com/gpu")
	assert.Len(t, c.resourceNames, 1)
}

func TestGetContainerInfo_NoData(t *testing.T) {
	c := NewClient(zap.NewNop())
	info := c.GetContainerInfo("0", "nvidia.com/gpu")
	assert.Nil(t, info)
}

func TestGetContainerInfo_WithData(t *testing.T) {
	c := NewClient(zap.NewNop())
	c.mu.Lock()
	c.deviceToPod[deviceKey{DeviceID: "0", ResourceName: "nvidia.com/gpu"}] = ContainerInfo{
		PodName: "ml-pod", Namespace: "default", ContainerName: "trainer",
	}
	c.mu.Unlock()

	info := c.GetContainerInfo("0", "nvidia.com/gpu")
	assert.NotNil(t, info)
	assert.Equal(t, "ml-pod", info.PodName)
	assert.Equal(t, "default", info.Namespace)
	assert.Equal(t, "trainer", info.ContainerName)
}

func TestGetContainerInfo_WrongResource(t *testing.T) {
	c := NewClient(zap.NewNop())
	c.mu.Lock()
	c.deviceToPod[deviceKey{DeviceID: "0", ResourceName: "nvidia.com/gpu"}] = ContainerInfo{
		PodName: "ml-pod", Namespace: "default", ContainerName: "trainer",
	}
	c.mu.Unlock()

	info := c.GetContainerInfo("0", "other.resource")
	assert.Nil(t, info)
}

func TestStop_NilFields(t *testing.T) {
	c := NewClient(zap.NewNop())
	// Should not panic with nil cancel and conn.
	c.Stop()
}

func TestRefresh_NoResourceNames(t *testing.T) {
	c := NewClient(zap.NewNop())
	// Should be a no-op when no resource names registered.
	c.refresh()
	assert.Empty(t, c.deviceToPod)
}
