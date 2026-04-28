// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Package costallocation implements the Coeus cost allocation engine for the
// awscontainerinsightreceiver. It computes proportional cost attribution metrics
// at every level of the Kubernetes hierarchy (node, pod, container, namespace,
// cluster) by combining EC2 instance pricing, hardware specifications, EKS
// cluster fees, and existing Container Insights utilization metrics.
package costallocation // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/costallocation"
