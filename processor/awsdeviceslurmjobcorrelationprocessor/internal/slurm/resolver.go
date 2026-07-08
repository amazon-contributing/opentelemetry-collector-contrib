// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package slurm // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/awsdeviceslurmjobcorrelationprocessor/internal/slurm"

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Resolver maintains a cached mapping from device IDs to Slurm job info.
// It combines cgroup inspection (authoritative device→job binding) with
// scontrol (metadata hydration: job name, user, account, partition).
type Resolver struct {
	cgroup        *CgroupResolver
	scontrol      *ScontrolClient
	nodeExclusive bool
	pollInterval  time.Duration
	logger        *zap.Logger

	mu       sync.RWMutex
	// jobCache maps jobID → *JobInfo (metadata from slurmrestd)
	jobCache map[string]*JobInfo
	// deviceToJob maps "deviceType:deviceID" → jobID
	deviceToJob map[string]string
	// nodeJobID is set when nodeExclusive=true and exactly one job runs on this node
	nodeJobID string

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewResolver creates a Resolver that periodically refreshes device→job mappings.
func NewResolver(cgroup *CgroupResolver, scontrol *ScontrolClient, nodeExclusive bool, pollInterval time.Duration, logger *zap.Logger) *Resolver {
	return &Resolver{
		cgroup:        cgroup,
		scontrol:      scontrol,
		nodeExclusive: nodeExclusive,
		pollInterval:  pollInterval,
		logger:        logger,
		jobCache:      make(map[string]*JobInfo),
		deviceToJob:   make(map[string]string),
	}
}

// Start begins background polling.
func (r *Resolver) Start(ctx context.Context) error {
	ctx, r.cancel = context.WithCancel(ctx)
	r.refresh()
	r.wg.Add(1)
	go r.pollLoop(ctx)
	return nil
}

// Stop halts background polling.
func (r *Resolver) Stop() {
	if r.cancel != nil {
		r.cancel()
	}
	r.wg.Wait()
}

func (r *Resolver) pollLoop(ctx context.Context) {
	defer r.wg.Done()
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.refresh()
		}
	}
}

func (r *Resolver) refresh() {
	// Step 1: Discover job IDs on this node via cgroups
	jobIDs, err := r.cgroup.GetJobIDsOnNode()
	if err != nil {
		r.logger.Debug("Failed to get job IDs from cgroups", zap.Error(err))
	}

	// If cgroups are empty (e.g. non-batch node in a multi-node job),
	// discover jobs via scontrol querying by hostname.
	if len(jobIDs) == 0 {
		hostname, _ := os.Hostname()
		jobs, err := r.scontrol.GetJobsOnNode(hostname)
		if err != nil {
			r.logger.Debug("Failed to get jobs from scontrol", zap.String("node", hostname), zap.Error(err))
			return
		}
		for _, j := range jobs {
			jobIDs = append(jobIDs, j.JobID)
		}
	}

	if len(jobIDs) == 0 {
		r.logger.Debug("No jobs found on this node")
		r.mu.Lock()
		r.jobCache = make(map[string]*JobInfo)
		r.deviceToJob = make(map[string]string)
		r.nodeJobID = ""
		r.mu.Unlock()
		return
	}

	newDeviceToJob := make(map[string]string)
	newJobCache := make(map[string]*JobInfo)

	for _, jobID := range jobIDs {
		// Get GPU devices for this job from cgroup
		gpuDevices, err := r.cgroup.GetGPUDevicesForJob(jobID)
		if err != nil {
			r.logger.Debug("Failed to get GPU devices for job from cgroup", zap.String("job_id", jobID), zap.Error(err))
		}
		for _, gpuIdx := range gpuDevices {
			key := fmt.Sprintf("gpu:%d", gpuIdx)
			newDeviceToJob[key] = jobID
		}

		// If node exclusive with single job, all EFA devices belong to it
		if r.nodeExclusive && len(jobIDs) == 1 {
			efaDevices := discoverEFADevices()
			for _, efaDev := range efaDevices {
				key := fmt.Sprintf("efa:%s", efaDev)
				newDeviceToJob[key] = jobID
			}
		}

		// Step 2: Hydrate metadata via scontrol
		job, err := r.scontrol.GetJob(jobID)
		if err != nil {
			r.logger.Debug("Failed to get job metadata from scontrol", zap.String("job_id", jobID), zap.Error(err))
			// Keep existing cache entry if available
			r.mu.RLock()
			if existing, ok := r.jobCache[jobID]; ok {
				newJobCache[jobID] = existing
			} else {
				newJobCache[jobID] = &JobInfo{JobID: jobID}
			}
			r.mu.RUnlock()
		} else {
			newJobCache[jobID] = job
			// If cgroup didn't give us GPU devices, try parsing from GRES
			if len(gpuDevices) == 0 {
				gresIndices := parseGRESGPUIndices(job.GRESDetail)
				for _, idx := range gresIndices {
					key := fmt.Sprintf("gpu:%d", idx)
					newDeviceToJob[key] = jobID
				}
			}
		}
	}

	r.mu.Lock()
	r.deviceToJob = newDeviceToJob
	r.jobCache = newJobCache
	if r.nodeExclusive && len(jobIDs) == 1 {
		r.nodeJobID = jobIDs[0]
	} else {
		r.nodeJobID = ""
	}
	r.mu.Unlock()

	r.logger.Debug("Refreshed device-job mappings",
		zap.Int("jobs", len(jobIDs)),
		zap.Int("device_mappings", len(newDeviceToJob)),
	)
}

// GetJobInfo returns job metadata for the given device.
// deviceType is "gpu" or "efa", deviceID is the raw value from the metric attribute.
func (r *Resolver) GetJobInfo(deviceID string, deviceType string) *JobInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Node exclusive fast path: if there's exactly one job, all devices belong to it
	if r.nodeExclusive && r.nodeJobID != "" {
		return r.jobCache[r.nodeJobID]
	}

	key := fmt.Sprintf("%s:%s", deviceType, deviceID)
	jobID, ok := r.deviceToJob[key]
	if !ok {
		return nil
	}
	return r.jobCache[jobID]
}

// parseGRESGPUIndices extracts GPU device indices from a GRES detail string.
// Example: "gpu:tesla_t4:1(IDX:0)" → [0]
// Example: "gpu:a100:4(IDX:0-3)" → [0,1,2,3]
func parseGRESGPUIndices(gresDetail string) []int {
	if gresDetail == "" {
		return nil
	}

	var indices []int
	// Look for IDX: patterns
	parts := strings.Split(gresDetail, "(IDX:")
	if len(parts) < 2 {
		return nil
	}
	idxStr := strings.TrimRight(parts[1], ")")
	for _, rangeStr := range strings.Split(idxStr, ",") {
		rangeStr = strings.TrimSpace(rangeStr)
		if strings.Contains(rangeStr, "-") {
			bounds := strings.SplitN(rangeStr, "-", 2)
			if len(bounds) == 2 {
				start := parseInt(bounds[0])
				end := parseInt(bounds[1])
				for i := start; i <= end; i++ {
					indices = append(indices, i)
				}
			}
		} else {
			indices = append(indices, parseInt(rangeStr))
		}
	}
	return indices
}

func parseInt(s string) int {
	n := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		}
	}
	return n
}

// discoverEFADevices lists EFA devices from sysfs.
func discoverEFADevices() []string {
	entries, err := os.ReadDir("/sys/class/infiniband")
	if err != nil {
		return nil
	}
	var devices []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "rdmap") || strings.HasPrefix(e.Name(), "efa") {
			devices = append(devices, e.Name())
		}
	}
	return devices
}
