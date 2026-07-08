// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package slurm

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestCgroupResolver_GetJobIDsOnNode_V1(t *testing.T) {
	// Create fake cgroup v1 structure
	tmpDir := t.TempDir()
	slurmDir := filepath.Join(tmpDir, "devices", "slurm", "uid_1000")
	require.NoError(t, os.MkdirAll(filepath.Join(slurmDir, "job_12345"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(slurmDir, "job_12346"), 0755))

	resolver := NewCgroupResolver(tmpDir, "v1", "", zaptest.NewLogger(t))
	jobIDs, err := resolver.GetJobIDsOnNode()
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"12345", "12346"}, jobIDs)
}

func TestCgroupResolver_GetJobIDsOnNode_V2(t *testing.T) {
	tmpDir := t.TempDir()
	slurmDir := filepath.Join(tmpDir, "system.slice", "slurmstepd.scope")
	require.NoError(t, os.MkdirAll(filepath.Join(slurmDir, "job_500"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(slurmDir, "job_501"), 0755))
	// Non-job dir should be ignored
	require.NoError(t, os.MkdirAll(filepath.Join(slurmDir, "user.slice"), 0755))

	resolver := NewCgroupResolver(tmpDir, "v2", "", zaptest.NewLogger(t))
	jobIDs, err := resolver.GetJobIDsOnNode()
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"500", "501"}, jobIDs)
}

func TestCgroupResolver_GetJobIDsOnNode_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	resolver := NewCgroupResolver(tmpDir, "v1", "", zaptest.NewLogger(t))
	jobIDs, err := resolver.GetJobIDsOnNode()
	require.NoError(t, err)
	assert.Empty(t, jobIDs)
}

func TestCgroupResolver_GetGPUDevicesForJob_V1(t *testing.T) {
	tmpDir := t.TempDir()
	jobDir := filepath.Join(tmpDir, "devices", "slurm", "uid_1000", "job_100")
	require.NoError(t, os.MkdirAll(jobDir, 0755))

	// Write a devices.list with NVIDIA GPU entries
	devicesContent := `c 1:3 rwm
c 1:5 rwm
c 195:0 rwm
c 195:1 rwm
c 195:255 rwm
c 5:0 rwm
`
	require.NoError(t, os.WriteFile(filepath.Join(jobDir, "devices.list"), []byte(devicesContent), 0644))

	resolver := NewCgroupResolver(tmpDir, "v1", "", zaptest.NewLogger(t))
	gpus, err := resolver.GetGPUDevicesForJob("100")
	require.NoError(t, err)
	// Should find GPU 0 and 1, skip 255 (control device)
	assert.Equal(t, []int{0, 1}, gpus)
}

func TestCgroupResolver_GetGPUDevicesForJob_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	resolver := NewCgroupResolver(tmpDir, "v1", "", zaptest.NewLogger(t))
	_, err := resolver.GetGPUDevicesForJob("999")
	assert.Error(t, err)
}

func TestCgroupResolver_HostPath(t *testing.T) {
	tmpDir := t.TempDir()
	hostMount := filepath.Join(tmpDir, "host")
	slurmDir := filepath.Join(hostMount, "sys", "fs", "cgroup", "system.slice", "slurmstepd.scope")
	require.NoError(t, os.MkdirAll(filepath.Join(slurmDir, "job_42"), 0755))

	resolver := NewCgroupResolver("/sys/fs/cgroup", "v2", hostMount, zaptest.NewLogger(t))
	jobIDs, err := resolver.GetJobIDsOnNode()
	require.NoError(t, err)
	assert.Equal(t, []string{"42"}, jobIDs)
}
