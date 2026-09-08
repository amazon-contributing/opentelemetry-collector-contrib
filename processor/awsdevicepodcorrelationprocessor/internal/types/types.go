// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package types // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/awsdevicepodcorrelationprocessor/internal/types"

// ContainerInfo holds Kubernetes pod/container metadata for a device.
type ContainerInfo struct {
	PodName       string
	ContainerName string
	Namespace     string
}
