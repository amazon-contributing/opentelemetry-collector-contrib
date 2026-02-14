// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsefareceiver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func testLogger() *zap.Logger {
	return zap.NewNop()
}

func setupTestSysfs(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	hwCountersPath := filepath.Join(root, "sys/class/infiniband/rdmap0s31/ports/1/hw_counters")
	require.NoError(t, os.MkdirAll(hwCountersPath, 0o755))

	counters := map[string]string{
		"rdma_read_bytes":             "1234",
		"rdma_write_bytes":            "5678",
		"rdma_write_recv_bytes":       "9012",
		"rx_bytes":                    "3456",
		"rx_drops":                    "7",
		"tx_bytes":                    "7890",
		"retrans_bytes":               "100",
		"retrans_pkts":                "10",
		"retrans_timeout_events":      "1",
		"unresponsive_remote_events":  "2",
		"impaired_remote_conn_events": "3",
	}

	for name, value := range counters {
		require.NoError(t, os.WriteFile(filepath.Join(hwCountersPath, name), []byte(value+"\n"), 0o600))
	}

	return root
}

func newTestReader(basePath string) *sysfsReaderImpl {
	return &sysfsReaderImpl{basePath: basePath, logger: testLogger()}
}

func TestSysFsReaderEfaDataExists(t *testing.T) {
	root := setupTestSysfs(t)
	basePath := filepath.Join(root, "sys/class/infiniband")

	// checkPermissions requires root ownership, which temp dirs won't have.
	// Verify the path exists and is a directory; the permission check is
	// tested separately.
	info, err := os.Stat(basePath)
	require.NoError(t, err)
	assert.True(t, info.IsDir())

	// When running as non-root, EfaDataExists returns false due to permission check.
	// This is expected and correct behavior.
	reader := newTestReader(basePath)
	exists, err := reader.EfaDataExists()
	require.NoError(t, err)
	if os.Getuid() == 0 {
		assert.True(t, exists)
	} else {
		assert.False(t, exists, "expected false when not running as root (permission check)")
	}
}

func TestSysFsReaderEfaDataNotExists(t *testing.T) {
	reader := newTestReader("/nonexistent/path")

	exists, err := reader.EfaDataExists()
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestSysFsReaderListDevices(t *testing.T) {
	root := setupTestSysfs(t)
	reader := newTestReader(filepath.Join(root, "sys/class/infiniband"))

	devices, err := reader.ListDevices()
	require.NoError(t, err)
	assert.Equal(t, []string{"rdmap0s31"}, devices)
}

func TestSysFsReaderListPorts(t *testing.T) {
	root := setupTestSysfs(t)
	reader := newTestReader(filepath.Join(root, "sys/class/infiniband"))

	ports, err := reader.ListPorts("rdmap0s31")
	require.NoError(t, err)
	assert.Equal(t, []string{"1"}, ports)
}

func TestSysFsReaderReadCounter(t *testing.T) {
	root := setupTestSysfs(t)
	reader := newTestReader(filepath.Join(root, "sys/class/infiniband"))

	val, err := reader.ReadCounter("rdmap0s31", "1", "rdma_read_bytes")
	require.NoError(t, err)
	assert.Equal(t, uint64(1234), val)
}

func TestSysFsReaderReadCounterMissing(t *testing.T) {
	root := setupTestSysfs(t)
	reader := newTestReader(filepath.Join(root, "sys/class/infiniband"))

	val, err := reader.ReadCounter("rdmap0s31", "1", "nonexistent_counter")
	require.NoError(t, err)
	assert.Equal(t, uint64(0), val)
}

func TestReadUint64FromFileNAPMA(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "counter")
	require.NoError(t, os.WriteFile(path, []byte("N/A (no PMA)\n"), 0o600))

	val, err := readUint64FromFile(path)
	require.NoError(t, err)
	assert.Equal(t, uint64(0), val)
}

func TestNewSysFsReaderWithHostPath(t *testing.T) {
	reader := newSysFsReader("/host", testLogger()).(*sysfsReaderImpl)
	assert.Equal(t, "/host/sys/class/infiniband", reader.basePath)
}

func TestNewSysFsReaderWithoutHostPath(t *testing.T) {
	reader := newSysFsReader("", testLogger()).(*sysfsReaderImpl)
	assert.Equal(t, "/sys/class/infiniband", reader.basePath)
}

func TestReadUint64FromFileInvalidContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "counter")
	require.NoError(t, os.WriteFile(path, []byte("not_a_number\n"), 0o600))

	val, err := readUint64FromFile(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse")
	assert.Equal(t, uint64(0), val)
}

func TestReadUint64FromFilePermissionDenied(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "counter")
	require.NoError(t, os.WriteFile(path, []byte("123\n"), 0o600))
	require.NoError(t, os.Chmod(path, 0o000))
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })

	val, err := readUint64FromFile(path)
	require.NoError(t, err)
	assert.Equal(t, uint64(0), val)
}

func TestListDevicesWithSymlink(t *testing.T) {
	root := t.TempDir()
	basePath := filepath.Join(root, "sys/class/infiniband")
	require.NoError(t, os.MkdirAll(basePath, 0o755))

	realDevice := filepath.Join(root, "devices", "efa0")
	require.NoError(t, os.MkdirAll(realDevice, 0o755))
	require.NoError(t, os.Symlink(realDevice, filepath.Join(basePath, "efa0")))

	reader := newTestReader(basePath)
	devices, err := reader.ListDevices()
	require.NoError(t, err)
	assert.Equal(t, []string{"efa0"}, devices)
}

func TestListDevicesSymlinkToFile(t *testing.T) {
	root := t.TempDir()
	basePath := filepath.Join(root, "sys/class/infiniband")
	require.NoError(t, os.MkdirAll(basePath, 0o755))

	filePath := filepath.Join(root, "somefile")
	require.NoError(t, os.WriteFile(filePath, []byte("data"), 0o600))
	require.NoError(t, os.Symlink(filePath, filepath.Join(basePath, "not_a_device")))

	reader := newTestReader(basePath)
	devices, err := reader.ListDevices()
	require.NoError(t, err)
	assert.Empty(t, devices)
}
