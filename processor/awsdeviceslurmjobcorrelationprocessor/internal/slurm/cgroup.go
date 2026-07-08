// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package slurm // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/awsdeviceslurmjobcorrelationprocessor/internal/slurm"

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"go.uber.org/zap"
)

// CgroupResolver discovers which Slurm job owns which device by inspecting
// the cgroup filesystem. It supports both cgroup v1 and v2 layouts.
type CgroupResolver struct {
	cgroupRoot    string
	cgroupVersion string
	hostPath      string
	logger        *zap.Logger
}

// NewCgroupResolver creates a new cgroup-based device-to-job resolver.
func NewCgroupResolver(cgroupRoot, cgroupVersion, hostPath string, logger *zap.Logger) *CgroupResolver {
	return &CgroupResolver{
		cgroupRoot:    cgroupRoot,
		cgroupVersion: cgroupVersion,
		hostPath:      hostPath,
		logger:        logger,
	}
}

var jobIDRegex = regexp.MustCompile(`job_(\d+)`)

// GetJobIDsOnNode returns all Slurm job IDs with cgroups on this node.
func (r *CgroupResolver) GetJobIDsOnNode() ([]string, error) {
	root := filepath.Join(r.hostPath, r.cgroupRoot)
	var jobIDs []string

	if r.cgroupVersion == "v2" {
		// cgroup v2: /sys/fs/cgroup/system.slice/slurmstepd.scope/job_{id}/
		slurmPath := filepath.Join(root, "system.slice", "slurmstepd.scope")
		entries, err := os.ReadDir(slurmPath)
		if err != nil {
			return nil, fmt.Errorf("reading cgroup v2 slurm path %s: %w", slurmPath, err)
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			matches := jobIDRegex.FindStringSubmatch(entry.Name())
			if len(matches) >= 2 {
				jobIDs = append(jobIDs, matches[1])
			}
		}
	} else {
		// cgroup v1: /sys/fs/cgroup/devices/slurm/uid_*/job_{id}/
		// or /sys/fs/cgroup/cpuset/slurm/uid_*/job_{id}/
		for _, controller := range []string{"freezer", "cpuset", "devices", "memory"} {
			slurmPath := filepath.Join(root, controller, "slurm")
			uidDirs, err := os.ReadDir(slurmPath)
			if err != nil {
				continue
			}
			for _, uidDir := range uidDirs {
				if !uidDir.IsDir() || !strings.HasPrefix(uidDir.Name(), "uid_") {
					continue
				}
				jobDirs, err := os.ReadDir(filepath.Join(slurmPath, uidDir.Name()))
				if err != nil {
					continue
				}
				for _, jobDir := range jobDirs {
					if !jobDir.IsDir() {
						continue
					}
					matches := jobIDRegex.FindStringSubmatch(jobDir.Name())
					if len(matches) >= 2 {
						jobIDs = append(jobIDs, matches[1])
					}
				}
			}
			if len(jobIDs) > 0 {
				break
			}
		}
	}

	return dedupStrings(jobIDs), nil
}

// GetGPUDevicesForJob returns the GPU device indices assigned to a job via cgroup.
// For cgroup v1, reads devices.list for nvidia device major number (195).
// For cgroup v2, reads the devices file.
func (r *CgroupResolver) GetGPUDevicesForJob(jobID string) ([]int, error) {
	root := filepath.Join(r.hostPath, r.cgroupRoot)

	if r.cgroupVersion == "v2" {
		// In cgroup v2, GPU isolation is typically via the step's scope
		devicePath := filepath.Join(root, "system.slice", "slurmstepd.scope", "job_"+jobID)
		return r.parseGPUDevicesFromDir(devicePath)
	}

	// cgroup v1: walk uid dirs to find the job
	slurmPath := filepath.Join(root, "devices", "slurm")
	uidDirs, err := os.ReadDir(slurmPath)
	if err != nil {
		return nil, fmt.Errorf("reading cgroup v1 devices/slurm: %w", err)
	}
	for _, uidDir := range uidDirs {
		if !uidDir.IsDir() || !strings.HasPrefix(uidDir.Name(), "uid_") {
			continue
		}
		jobPath := filepath.Join(slurmPath, uidDir.Name(), "job_"+jobID)
		if info, err := os.Stat(jobPath); err == nil && info.IsDir() {
			return r.parseGPUDevicesFromDir(jobPath)
		}
	}

	return nil, fmt.Errorf("job %s not found in cgroup tree", jobID)
}

func (r *CgroupResolver) parseGPUDevicesFromDir(dirPath string) ([]int, error) {
	devicesFile := filepath.Join(dirPath, "devices.list")
	data, err := os.ReadFile(devicesFile)
	if err != nil {
		// Fallback: try to enumerate nvidia devices via allowed devices
		return nil, fmt.Errorf("reading %s: %w", devicesFile, err)
	}

	// NVIDIA GPU devices have major number 195
	// Format: "c 195:0 rwm" -> GPU 0, "c 195:1 rwm" -> GPU 1
	var gpuIndices []int
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "c 195:") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		devParts := strings.Split(parts[1], ":")
		if len(devParts) != 2 {
			continue
		}
		minor, err := strconv.Atoi(devParts[1])
		if err != nil {
			continue
		}
		// Minor 255 is the control device (nvidiactl), skip it
		if minor == 255 {
			continue
		}
		gpuIndices = append(gpuIndices, minor)
	}
	return gpuIndices, nil
}

func dedupStrings(input []string) []string {
	seen := make(map[string]struct{}, len(input))
	result := make([]string, 0, len(input))
	for _, s := range input {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			result = append(result, s)
		}
	}
	return result
}
