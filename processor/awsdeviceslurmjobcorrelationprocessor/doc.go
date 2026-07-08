// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Package awsdeviceslurmjobcorrelationprocessor enriches GPU and EFA device
// metrics with Slurm job metadata by correlating device IDs to running jobs
// via cgroup inspection and slurmrestd.
package awsdeviceslurmjobcorrelationprocessor // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/awsdeviceslurmjobcorrelationprocessor"
