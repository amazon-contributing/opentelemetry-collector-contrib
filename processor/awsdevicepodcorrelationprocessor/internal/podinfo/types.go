// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0
package podinfo // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/awsdevicepodcorrelationprocessor/internal/podinfo"

// ContainerInfo holds Kubernetes pod/container metadata for a device.
type ContainerInfo struct {
	PodName       string
	ContainerName string
	Namespace     string
}

// DRABinding is a running container's claim on a DRA ResourceClaim, as reported
// by the Kubelet Pod Resources API on this node. It carries only the pod↔claim
// association; the allocated devices (driver/pool/device) are read separately
// from the ResourceClaim itself, because not every DRA driver populates the
// per-device detail in the Pod Resources API (network drivers such as DRANET
// report the claim but with empty ClaimResources, whereas the ResourceClaim's
// allocation is always populated by the scheduler).
//
// Because the Pod Resources API only lists pods with running containers, these
// bindings are inherently node-local and active-only: terminated pods (e.g.
// completed Jobs) are never reported, so a device freed by one pod and reused by
// another is attributed to the current owner without any reservedFor bookkeeping.
type DRABinding struct {
	ClaimName      string
	ClaimNamespace string
	Pod            ContainerInfo
}
