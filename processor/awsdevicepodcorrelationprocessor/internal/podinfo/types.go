// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package podinfo // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/awsdevicepodcorrelationprocessor/internal/podinfo"

// ContainerInfo holds Kubernetes pod/container metadata for a device.
type ContainerInfo struct {
	PodName       string
	ContainerName string
	Namespace     string
}
