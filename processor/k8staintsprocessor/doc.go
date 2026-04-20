// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

//go:generate mdatagen metadata.yaml

// Package k8staintsprocessor implements an OTel processor that enriches
// metrics and logs with Kubernetes node taints. It watches Node objects via
// a SharedInformer and adds each taint as a resource attribute with the format
// k8s.node.taint.<key> = <value>.
package k8staintsprocessor // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/k8staintsprocessor"
