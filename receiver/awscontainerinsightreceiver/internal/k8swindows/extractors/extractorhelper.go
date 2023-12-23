package extractors

import (
	stats "k8s.io/kubelet/pkg/apis/stats/v1alpha1"
)

// ConvertPodToRaw Converts Kubelet Pod stats to RawMetric.
func ConvertPodToRaw(podStat *stats.PodStats) *RawMetric {
	var rawMetic *RawMetric
	rawMetic = &RawMetric{}
	rawMetic.Id = podStat.PodRef.UID
	rawMetic.Name = podStat.PodRef.Name
	rawMetic.Namespace = podStat.PodRef.Namespace
	rawMetic.Time = podStat.CPU.Time.Time
	rawMetic.CPUStats = podStat.CPU
	rawMetic.MemoryStats = podStat.Memory
	return rawMetic
}

// ConvertNodeToRaw Converts Kubelet Node stats to RawMetric.
func ConvertNodeToRaw(nodeStat *stats.NodeStats) *RawMetric {
	var rawMetic *RawMetric
	rawMetic = &RawMetric{}
	rawMetic.Id = nodeStat.NodeName
	rawMetic.Name = nodeStat.NodeName
	rawMetic.Time = nodeStat.CPU.Time.Time
	rawMetic.CPUStats = nodeStat.CPU
	rawMetic.MemoryStats = nodeStat.Memory
	return rawMetic
}
