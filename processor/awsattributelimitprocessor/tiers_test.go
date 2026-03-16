// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsattributelimitprocessor

import (
	"testing"
)

func TestIsProtectedResource(t *testing.T) {
	tests := []struct {
		key      string
		expected bool
	}{
		// K8s identity
		{"k8s.cluster.name", true},
		{"k8s.node.name", true},
		{"k8s.pod.name", true},
		{"k8s.pod.uid", true},
		{"k8s.namespace.name", true},
		{"k8s.container.name", true},
		// K8s workload
		{"k8s.deployment.name", true},
		{"k8s.statefulset.name", true},
		{"k8s.daemonset.name", true},
		{"k8s.replicaset.name", true},
		{"k8s.job.name", true},
		{"k8s.cronjob.name", true},
		{"k8s.workload.name", true},
		{"k8s.workload.type", true},
		// Device-specific
		{"neurondevice", true},
		{"neuroncore", true},
		{"efa.device", true},
		{"aws.efa.eni.id", true},
		{"volume_id", true},
		{"instance_id", true},
		// App identity pod labels
		{"k8s.pod.label.app.kubernetes.io/name", true},
		{"k8s.pod.label.app.kubernetes.io/instance", true},
		{"k8s.pod.label.app.kubernetes.io/component", true},
		// Control plane
		{"k8s.component.name", true},
		// Prefix-protected
		{"cloud.region", true},
		{"cloud.account.id", true},
		{"cloud.provider", true},
		{"host.name", true},
		{"host.type", true},
		{"host.id", true},
		{"hw.type", true},
		{"hw.vendor", true},
		{"hw.id", true},
		// Not protected as resource attrs
		{"k8s.node.label.some-label", false},
		{"k8s.pod.label.some-label", false},
		{"job", false},
		{"instance", false},
		{"random_attr", false},
		// Datapoint keys are NOT protected as resource attrs
		{"cpu", false},
		{"mode", false},
		{"code", false},
		{"method", false},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			got := isProtectedResource(tt.key)
			if got != tt.expected {
				t.Errorf("isProtectedResource(%q) = %v, want %v", tt.key, got, tt.expected)
			}
		})
	}
}

func TestIsProtectedDatapoint(t *testing.T) {
	tests := []struct {
		key      string
		expected bool
	}{
		// node_exporter
		{"cpu", true},
		{"mode", true},
		{"device", true},
		{"mountpoint", true},
		{"fstype", true},
		// cadvisor
		{"interface", true},
		// neuron
		{"memory_location", true},
		{"percentile", true},
		// control plane
		{"verb", true},
		{"code", true},
		{"method", true},
		{"request_kind", true},
		// Not protected as datapoint attrs
		{"job", false},
		{"instance", false},
		{"random_attr", false},
		// Resource keys are NOT protected as datapoint attrs
		{"k8s.node.name", false},
		{"cloud.region", false},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			got := isProtectedDatapoint(tt.key)
			if got != tt.expected {
				t.Errorf("isProtectedDatapoint(%q) = %v, want %v", tt.key, got, tt.expected)
			}
		})
	}
}

func TestClassifyAttribute_Tier1_HelmTooling(t *testing.T) {
	keys := []string{
		"k8s.node.label.helm.sh/chart",
		"k8s.node.label.app.kubernetes.io/managed-by",
		"k8s.node.label.app.kubernetes.io/version",
		"k8s.node.label.app.kubernetes.io/part-of",
		"k8s.node.label.chart",
		"k8s.node.label.release",
		"k8s.node.label.heritage",
		"k8s.pod.label.helm.sh/chart",
		"k8s.pod.label.app.kubernetes.io/managed-by",
		"k8s.pod.label.app.kubernetes.io/version",
	}
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			tier := classifyAttribute(key, "resource")
			if tier != tier1HelmTooling {
				t.Errorf("classifyAttribute(%q, \"resource\") = %d, want %d (tier1HelmTooling)", key, tier, tier1HelmTooling)
			}
		})
	}
}

func TestClassifyAttribute_Tier2_K8sInternal(t *testing.T) {
	keys := []string{
		"k8s.node.label.pod-template-generation",
		"k8s.pod.label.statefulset.kubernetes.io/pod-name",
		"k8s.pod.label.batch.kubernetes.io/controller-uid",
	}
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			tier := classifyAttribute(key, "resource")
			if tier != tier2K8sInternal {
				t.Errorf("classifyAttribute(%q, \"resource\") = %d, want %d (tier2K8sInternal)", key, tier, tier2K8sInternal)
			}
		})
	}
}

func TestClassifyAttribute_Tier3_VendorNode(t *testing.T) {
	keys := []string{
		"k8s.node.label.karpenter.sh/nodepool",
		"k8s.node.label.karpenter.k8s.aws/instance-type",
		"k8s.node.label.nvidia.com/gpu.count",
		"k8s.node.label.aws.amazon.com/neuron.present",
	}
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			tier := classifyAttribute(key, "resource")
			if tier != tier3VendorNode {
				t.Errorf("classifyAttribute(%q, \"resource\") = %d, want %d (tier3VendorNode)", key, tier, tier3VendorNode)
			}
		})
	}
}

func TestClassifyAttribute_Tier4_EKSSystem(t *testing.T) {
	keys := []string{
		"k8s.node.label.eks.amazonaws.com/capacityType",
		"k8s.node.label.eks.amazonaws.com/nodegroup",
		"k8s.node.label.node.kubernetes.io/lifecycle",
	}
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			tier := classifyAttribute(key, "resource")
			if tier != tier4EKSSystem {
				t.Errorf("classifyAttribute(%q, \"resource\") = %d, want %d (tier4EKSSystem)", key, tier, tier4EKSSystem)
			}
		})
	}
}

func TestClassifyAttribute_Tier5_K8sSystemNode(t *testing.T) {
	keys := []string{
		"k8s.node.label.kubernetes.io/arch",
		"k8s.node.label.kubernetes.io/os",
		"k8s.node.label.topology.kubernetes.io/something",
		"k8s.node.label.node.kubernetes.io/instance-type",
		"k8s.node.label.k8s.io/cloud-provider-aws",
	}
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			tier := classifyAttribute(key, "resource")
			if tier != tier5K8sSystemNode {
				t.Errorf("classifyAttribute(%q, \"resource\") = %d, want %d (tier5K8sSystemNode)", key, tier, tier5K8sSystemNode)
			}
		})
	}
}

func TestClassifyAttribute_Tier6_CustomerNode(t *testing.T) {
	keys := []string{
		"k8s.node.label.my-company/team",
		"k8s.node.label.environment",
		"k8s.node.label.custom-label",
	}
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			tier := classifyAttribute(key, "resource")
			if tier != tier6CustomerNode {
				t.Errorf("classifyAttribute(%q, \"resource\") = %d, want %d (tier6CustomerNode)", key, tier, tier6CustomerNode)
			}
		})
	}
}

func TestClassifyAttribute_Tier7_KnownPod(t *testing.T) {
	// Note: app.kubernetes.io/name, /instance, /component are protected.
	// But app.kubernetes.io/managed-by is Tier 1 (Helm tooling).
	// Remaining known-prefix pod labels go to Tier 7.
	keys := []string{
		"k8s.pod.label.batch.kubernetes.io/job-name",
		"k8s.pod.label.statefulset.kubernetes.io/ordinal",
	}
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			tier := classifyAttribute(key, "resource")
			if tier != tier7KnownPod {
				t.Errorf("classifyAttribute(%q, \"resource\") = %d, want %d (tier7KnownPod)", key, tier, tier7KnownPod)
			}
		})
	}
}

func TestClassifyAttribute_Tier8_CustomerPod(t *testing.T) {
	keys := []string{
		"k8s.pod.label.my-app/version",
		"k8s.pod.label.environment",
		"k8s.pod.label.team",
	}
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			tier := classifyAttribute(key, "resource")
			if tier != tier8CustomerPod {
				t.Errorf("classifyAttribute(%q, \"resource\") = %d, want %d (tier8CustomerPod)", key, tier, tier8CustomerPod)
			}
		})
	}
}

func TestClassifyAttribute_Tier9_Scope(t *testing.T) {
	keys := []string{
		"source",
		"collector.version",
	}
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			tier := classifyAttribute(key, "scope")
			if tier != tier9Scope {
				t.Errorf("classifyAttribute(%q, \"scope\") = %d, want %d (tier9Scope)", key, tier, tier9Scope)
			}
		})
	}
}

func TestClassifyAttribute_Tier10_Datapoint(t *testing.T) {
	keys := []string{
		"job",
		"instance",
		"some_metric_label",
		"request_path",
		"status",
	}
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			tier := classifyAttribute(key, "datapoint")
			if tier != tier10Datapoint {
				t.Errorf("classifyAttribute(%q, \"datapoint\") = %d, want %d (tier10Datapoint)", key, tier, tier10Datapoint)
			}
		})
	}
}

func TestClassifyAttribute_ProtectedReturnsZero(t *testing.T) {
	keys := []string{
		"k8s.pod.name",
		"k8s.cluster.name",
		"cloud.region",
		"host.name",
		"hw.type",
		"k8s.pod.label.app.kubernetes.io/name",
	}
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			tier := classifyAttribute(key, "resource")
			if tier != tierNotDroppable {
				t.Errorf("classifyAttribute(%q, false) = %d, want %d (not droppable)", key, tier, tierNotDroppable)
			}
		})
	}
}

func TestClassifyAttribute_NonLabelResourceAttr(t *testing.T) {
	// Non-label, non-protected resource attributes should not be droppable.
	keys := []string{
		"some.random.resource.attr",
		"service.name",
	}
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			tier := classifyAttribute(key, "resource")
			if tier != tierNotDroppable {
				t.Errorf("classifyAttribute(%q, false) = %d, want %d (not droppable)", key, tier, tierNotDroppable)
			}
		})
	}
}

func TestClassifyAttribute_Tier4_OnlyNodeScope(t *testing.T) {
	// EKS system labels on pod scope should NOT be Tier 4 — they should be Tier 8 (customer pod).
	key := "k8s.pod.label.eks.amazonaws.com/capacityType"
	tier := classifyAttribute(key, "resource")
	if tier == tier4EKSSystem {
		t.Errorf("pod label %q should not be classified as Tier 4 (EKS system node labels)", key)
	}
}

func TestClassifyAttribute_ProtectedDatapointAttrsNotDroppable(t *testing.T) {
	// These datapoint attributes are in protectedDatapointKeys and should return tierNotDroppable.
	keys := []string{
		// node_exporter
		"cpu",
		"mode",
		"device",
		"mountpoint",
		"fstype",
		// cadvisor
		"interface",
		// neuron
		"memory_location",
		"percentile",
		// control plane
		"verb",
		"code",
		"method",
		"request_kind",
	}
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			tier := classifyAttribute(key, "datapoint")
			if tier != tierNotDroppable {
				t.Errorf("classifyAttribute(%q, \"datapoint\") = %d, want %d (protected)", key, tier, tierNotDroppable)
			}
		})
	}
}
