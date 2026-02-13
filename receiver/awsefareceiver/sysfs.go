// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsefareceiver // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awsefareceiver"

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"go.uber.org/zap"
)

const (
	defaultEfaPath = "/sys/class/infiniband"
)

// efaDevice represents a single EFA device with its port and counter values.
type efaDevice struct {
	name     string
	port     string
	counters map[string]uint64
}

// sysFsReader is an interface for reading EFA data from sysfs, enabling testability.
type sysFsReader interface {
	EfaDataExists() (bool, error)
	ListDevices() ([]string, error)
	ListPorts(deviceName string) ([]string, error)
	ReadCounter(deviceName string, port string, counter string) (uint64, error)
}

type sysfsReaderImpl struct {
	basePath string
	logger   *zap.Logger
}

func newSysFsReader(hostPath string, logger *zap.Logger) sysFsReader {
	basePath := defaultEfaPath
	if hostPath != "" {
		basePath = filepath.Join(hostPath, defaultEfaPath)
	}
	return &sysfsReaderImpl{basePath: basePath, logger: logger}
}

func (r *sysfsReaderImpl) EfaDataExists() (bool, error) {
	info, err := os.Stat(r.basePath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if !info.IsDir() {
		return false, nil
	}

	if err := checkPermissions(info); err != nil {
		r.logger.Warn("Not reading from EFA directory, permission check failed",
			zap.String("path", r.basePath), zap.Error(err))
		return false, nil
	}

	return true, nil
}

func (r *sysfsReaderImpl) ListDevices() ([]string, error) {
	dirs, err := os.ReadDir(r.basePath)
	if err != nil {
		return nil, fmt.Errorf("failed to list EFA devices at %q: %w", r.basePath, err)
	}

	result := make([]string, 0, len(dirs))
	for _, entry := range dirs {
		// In sysfs, entries are often symlinks to device directories.
		// Follow symlinks with os.Stat to check the real target.
		info, err := os.Stat(filepath.Join(r.basePath, entry.Name()))
		if err != nil {
			continue
		}
		if !info.IsDir() {
			continue
		}
		result = append(result, entry.Name())
	}
	return result, nil
}

func (r *sysfsReaderImpl) ListPorts(deviceName string) ([]string, error) {
	portsPath := filepath.Join(r.basePath, deviceName, "ports")
	portDirs, err := os.ReadDir(portsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to list EFA ports at %q: %w", portsPath, err)
	}

	result := make([]string, 0, len(portDirs))
	for _, dir := range portDirs {
		if !dir.IsDir() {
			continue
		}
		result = append(result, dir.Name())
	}
	return result, nil
}

func (r *sysfsReaderImpl) ReadCounter(deviceName string, port string, counter string) (uint64, error) {
	path := filepath.Join(r.basePath, deviceName, "ports", port, "hw_counters", counter)
	return readUint64FromFile(path)
}

func readUint64FromFile(path string) (uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) || os.IsPermission(err) {
			return 0, nil
		}
		// Some kernel drivers return these for counters that exist but
		// aren't available on the current hardware revision.
		errMsg := err.Error()
		if strings.Contains(errMsg, "operation not supported") || strings.Contains(errMsg, "invalid argument") {
			return 0, nil
		}
		return 0, fmt.Errorf("failed to read file %q: %w", path, err)
	}

	value := strings.TrimSpace(string(data))

	// Workaround for https://github.com/prometheus/node_exporter/issues/966
	if strings.Contains(value, "N/A (no PMA)") {
		return 0, nil
	}

	v, err := strconv.ParseUint(value, 0, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse %q from %q: %w", value, path, err)
	}
	return v, nil
}
