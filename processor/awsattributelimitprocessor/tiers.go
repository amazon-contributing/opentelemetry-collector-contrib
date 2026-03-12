// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsattributelimitprocessor

import "strings"

// Tier constants define the drop priority.
// Lower tiers are dropped first. Tier 0 means the attribute is not droppable.
// Datapoint and scope attrs are dropped before resource attrs to avoid
// over-pruning shared resource attributes.
const (
	tierNotDroppable  = 0 // Protected attribute
	tier1Datapoint    = 1 // Non-protected datapoint attributes (per-datapoint, no shared impact)
	tier2Scope        = 2 // Non-protected scope attributes (except instrumentation.cloudwatch.*)
	tier3HelmTooling  = 3 // Helm/tooling labels (node + pod)
	tier4K8sInternal  = 4 // K8s internal controller labels (node + pod)
	tier5EKSSystem    = 5 // EKS system labels (node only)
	tier6KnownNode    = 6 // Known-prefix node labels
	tier7CustomerNode = 7 // Customer node labels (unknown prefix)
	tier8KnownPod     = 8 // Known-prefix pod labels
	tier9CustomerPod  = 9 // Customer pod labels (unknown prefix)
)

// protectedExactKeys contains resource attribute keys that are never dropped.
var protectedExactKeys = map[string]struct{}{
	// K8s identity
	"k8s.cluster.name":   {},
	"k8s.node.name":      {},
	"k8s.pod.name":       {},
	"k8s.pod.uid":        {},
	"k8s.namespace.name": {},
	"k8s.container.name": {},
	// K8s workload
	"k8s.deployment.name":  {},
	"k8s.statefulset.name": {},
	"k8s.daemonset.name":   {},
	"k8s.replicaset.name":  {},
	"k8s.job.name":         {},
	"k8s.cronjob.name":     {},
	"k8s.workload.name":    {},
	"k8s.workload.type":    {},
	// Device-specific
	"neurondevice":   {},
	"neuroncore":     {},
	"efa.device":     {},
	"aws.efa.eni.id": {},
	"volume_id":      {},
	"instance_id":    {},
	// App identity pod labels
	"k8s.pod.label.app.kubernetes.io/name":      {},
	"k8s.pod.label.app.kubernetes.io/instance":  {},
	"k8s.pod.label.app.kubernetes.io/component": {},
	// Control plane
	"k8s.component.name": {},
}

// protectedPrefixes contains resource attribute prefixes that are never dropped.
var protectedPrefixes = []string{
	"cloud.",
	"host.",
	"hw.",
}

// protectedScopePrefix is the scope attribute prefix that is never dropped.
const protectedScopePrefix = "instrumentation.cloudwatch."

// isProtected returns true if the key is a protected attribute that should never be dropped.
func isProtected(key string) bool {
	if _, ok := protectedExactKeys[key]; ok {
		return true
	}
	for _, prefix := range protectedPrefixes {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

// helmToolingSuffixes are Helm/tooling label suffixes (node + pod scope).
var helmToolingSuffixes = map[string]struct{}{
	"helm.sh/chart":                {},
	"app.kubernetes.io/managed-by": {},
	"app.kubernetes.io/version":    {},
	"app.kubernetes.io/part-of":    {},
	"chart":                        {},
	"release":                      {},
	"heritage":                     {},
}

// k8sInternalSuffixes are K8s internal controller label suffixes (node + pod scope).
var k8sInternalSuffixes = map[string]struct{}{
	"pod-template-generation":            {},
	"statefulset.kubernetes.io/pod-name": {},
	"batch.kubernetes.io/controller-uid": {},
}

// eksSystemSuffixes are EKS system label suffixes (node scope only).
var eksSystemSuffixes = map[string]struct{}{
	"eks.amazonaws.com/capacityType": {},
	"eks.amazonaws.com/nodegroup":    {},
	"node.kubernetes.io/lifecycle":   {},
}

// knownNodeLabelPrefixes are prefixes for known node label classification.
var knownNodeLabelPrefixes = []string{
	"kubernetes.io/",
	"node.kubernetes.io/",
	"topology.kubernetes.io/",
	"eks.amazonaws.com/",
	"karpenter.sh/",
	"karpenter.k8s.aws/",
	"nvidia.com/",
	"aws.amazon.com/",
	"k8s.io/",
}

// knownPodLabelPrefixes are prefixes for known pod label classification.
var knownPodLabelPrefixes = []string{
	"app.kubernetes.io/",
	"batch.kubernetes.io/",
	"statefulset.kubernetes.io/",
}

const (
	nodeLabelPrefix = "k8s.node.label."
	podLabelPrefix  = "k8s.pod.label."
)

// classifyAttribute returns the tier for a droppable attribute,
// or 0 (tierNotDroppable) if the attribute is protected or not classifiable.
// attrSource indicates where the attribute lives: "datapoint", "scope", or "resource".
func classifyAttribute(key string, attrSource string) int {
	if attrSource == "datapoint" {
		if isProtected(key) {
			return tierNotDroppable
		}
		return tier1Datapoint
	}

	if attrSource == "scope" {
		if strings.HasPrefix(key, protectedScopePrefix) {
			return tierNotDroppable
		}
		return tier2Scope
	}

	// Resource attribute classification.
	if isProtected(key) {
		return tierNotDroppable
	}

	isNodeLabel := strings.HasPrefix(key, nodeLabelPrefix)
	isPodLabel := strings.HasPrefix(key, podLabelPrefix)

	if !isNodeLabel && !isPodLabel {
		return tierNotDroppable
	}

	var suffix string
	if isNodeLabel {
		suffix = key[len(nodeLabelPrefix):]
	} else {
		suffix = key[len(podLabelPrefix):]
	}

	if _, ok := helmToolingSuffixes[suffix]; ok {
		return tier3HelmTooling
	}

	if _, ok := k8sInternalSuffixes[suffix]; ok {
		return tier4K8sInternal
	}

	if isNodeLabel {
		if _, ok := eksSystemSuffixes[suffix]; ok {
			return tier5EKSSystem
		}
		for _, prefix := range knownNodeLabelPrefixes {
			if strings.HasPrefix(suffix, prefix) {
				return tier6KnownNode
			}
		}
		return tier7CustomerNode
	}

	for _, prefix := range knownPodLabelPrefixes {
		if strings.HasPrefix(suffix, prefix) {
			return tier8KnownPod
		}
	}

	return tier9CustomerPod
}
