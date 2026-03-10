// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsattributelimitprocessor

import "strings"

// Tier constants define the drop priority for Phase 2.
// Lower tiers are dropped first. Tier 0 means the attribute is not droppable.
const (
	tierNotDroppable  = 0 // Protected or non-label resource attribute
	tier1HelmTooling  = 1 // Helm/tooling labels (node + pod)
	tier2K8sInternal  = 2 // K8s internal controller labels (node + pod)
	tier3EKSSystem    = 3 // EKS system labels (node only)
	tier4KnownNode    = 4 // Known-prefix node labels
	tier5CustomerNode = 5 // Customer node labels (unknown prefix)
	tier6KnownPod     = 6 // Known-prefix pod labels
	tier7CustomerPod  = 7 // Customer pod labels (unknown prefix)
	tier8Datapoint    = 8 // Non-protected datapoint attributes
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

// tier1Suffixes are Helm/tooling label suffixes (node + pod scope).
var tier1Suffixes = map[string]struct{}{
	"helm.sh/chart":                {},
	"app.kubernetes.io/managed-by": {},
	"app.kubernetes.io/version":    {},
	"app.kubernetes.io/part-of":    {},
	"chart":                        {},
	"release":                      {},
	"heritage":                     {},
}

// tier2Suffixes are K8s internal controller label suffixes (node + pod scope).
var tier2Suffixes = map[string]struct{}{
	"pod-template-generation":            {},
	"statefulset.kubernetes.io/pod-name": {},
	"batch.kubernetes.io/controller-uid": {},
}

// tier3Suffixes are EKS system label suffixes (node scope only).
var tier3Suffixes = map[string]struct{}{
	"eks.amazonaws.com/capacityType": {},
	"eks.amazonaws.com/nodegroup":    {},
	"node.kubernetes.io/lifecycle":   {},
}

// knownNodeLabelPrefixes are prefixes for Tier 4 node label classification.
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

// knownPodLabelPrefixes are prefixes for Tier 6 pod label classification.
var knownPodLabelPrefixes = []string{
	"app.kubernetes.io/",
	"batch.kubernetes.io/",
	"statefulset.kubernetes.io/",
}

const (
	nodeLabelPrefix = "k8s.node.label."
	podLabelPrefix  = "k8s.pod.label."
)

// classifyAttribute returns the tier (1-8) for a droppable attribute,
// or 0 (tierNotDroppable) if the attribute is protected or not classifiable.
func classifyAttribute(key string, isDatapoint bool) int {
	// Protected attributes are never dropped.
	if isProtected(key) {
		return tierNotDroppable
	}

	// Non-protected datapoint attributes are always Tier 8.
	if isDatapoint {
		return tier8Datapoint
	}

	// Extract suffix for node/pod label classification.
	isNodeLabel := strings.HasPrefix(key, nodeLabelPrefix)
	isPodLabel := strings.HasPrefix(key, podLabelPrefix)

	if !isNodeLabel && !isPodLabel {
		// Non-label resource attribute that isn't protected — treat as not droppable.
		// This covers resource attrs like scope-related keys we may have missed.
		return tierNotDroppable
	}

	var suffix string
	if isNodeLabel {
		suffix = key[len(nodeLabelPrefix):]
	} else {
		suffix = key[len(podLabelPrefix):]
	}

	// Tier 1: Helm/tooling labels (both node and pod).
	if _, ok := tier1Suffixes[suffix]; ok {
		return tier1HelmTooling
	}

	// Tier 2: K8s internal controller labels (both node and pod).
	if _, ok := tier2Suffixes[suffix]; ok {
		return tier2K8sInternal
	}

	// Node-specific tiers (3-5).
	if isNodeLabel {
		// Tier 3: EKS system labels.
		if _, ok := tier3Suffixes[suffix]; ok {
			return tier3EKSSystem
		}

		// Tier 4: Known-prefix node labels.
		for _, prefix := range knownNodeLabelPrefixes {
			if strings.HasPrefix(suffix, prefix) {
				return tier4KnownNode
			}
		}

		// Tier 5: Customer node labels (unknown prefix).
		return tier5CustomerNode
	}

	// Pod-specific tiers (6-7).
	// Tier 6: Known-prefix pod labels.
	for _, prefix := range knownPodLabelPrefixes {
		if strings.HasPrefix(suffix, prefix) {
			return tier6KnownPod
		}
	}

	// Tier 7: Customer pod labels (unknown prefix).
	return tier7CustomerPod
}
