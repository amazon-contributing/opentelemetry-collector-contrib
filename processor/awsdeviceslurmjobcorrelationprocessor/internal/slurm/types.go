// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package slurm // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/awsdeviceslurmjobcorrelationprocessor/internal/slurm"

// JobInfo holds Slurm job metadata used to enrich device metrics.
type JobInfo struct {
	JobID     string
	JobName   string
	User      string
	Account   string
	Partition string
	// GRESDetail contains the GRES allocation string (e.g., "gpu:tesla_t4:1(IDX:0)").
	GRESDetail string
	// NodeList is the Slurm node expression for nodes allocated to this job.
	NodeList string
}

// DeviceJobMapping maps a device identifier to its owning job.
type DeviceJobMapping struct {
	DeviceID   string
	DeviceType string
	Job        *JobInfo
}
