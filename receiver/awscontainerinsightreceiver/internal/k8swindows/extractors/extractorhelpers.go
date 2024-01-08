package extractors

import (
	"fmt"

	stats "k8s.io/kubelet/pkg/apis/stats/v1alpha1"
)

// convertCPUStats Convert kubelet CPU stats to Raw CPU stats
func convertCPUStats(kubeletCPUStat *stats.CPUStats) *CPUStat {
	var cpuStat CPUStat
	if kubeletCPUStat.UsageCoreNanoSeconds != nil {
		cpuStat.UsageCoreNanoSeconds = *kubeletCPUStat.UsageCoreNanoSeconds
	}
	if kubeletCPUStat.UsageNanoCores != nil {
		cpuStat.UsageNanoCores = *kubeletCPUStat.UsageNanoCores
	}
	return &cpuStat
}

// convertMemoryStats Convert kubelet memory stats to Raw memory stats
func convertMemoryStats(kubeletMemoryStat *stats.MemoryStats) *MemoryStat {
	var memoryStat MemoryStat
	if kubeletMemoryStat.UsageBytes != nil {
		memoryStat.UsageBytes = *kubeletMemoryStat.UsageBytes
	}
	if kubeletMemoryStat.AvailableBytes != nil {
		memoryStat.AvailableBytes = *kubeletMemoryStat.AvailableBytes
	}
	if kubeletMemoryStat.WorkingSetBytes != nil {
		memoryStat.WorkingSetBytes = *kubeletMemoryStat.WorkingSetBytes
	}
	if kubeletMemoryStat.RSSBytes != nil {
		memoryStat.RSSBytes = *kubeletMemoryStat.RSSBytes
	}
	if kubeletMemoryStat.PageFaults != nil {
		memoryStat.PageFaults = *kubeletMemoryStat.PageFaults
	}
	if kubeletMemoryStat.MajorPageFaults != nil {
		memoryStat.MajorPageFaults = *kubeletMemoryStat.MajorPageFaults
	}
	return &memoryStat
}

// ConvertPodToRaw Converts Kubelet Pod stats to RawMetric.
func ConvertPodToRaw(podStat *stats.PodStats) *RawMetric {
	var rawMetic *RawMetric
	rawMetic = &RawMetric{}
	rawMetic.Id = podStat.PodRef.UID
	rawMetic.Name = podStat.PodRef.Name
	rawMetic.Namespace = podStat.PodRef.Namespace

	if podStat.CPU != nil {
		rawMetic.Time = podStat.CPU.Time.Time
		rawMetic.CPUStats = convertCPUStats(podStat.CPU)
	}

	if podStat.Memory != nil {
		rawMetic.MemoryStats = convertMemoryStats(podStat.Memory)
	}
	return rawMetic
}

// ConvertContainerToRaw Converts Kubelet Container stats per Pod to RawMetric.
func ConvertContainerToRaw(containerStat *stats.ContainerStats, podStat *stats.PodStats) *RawMetric {
	var rawMetic *RawMetric
	rawMetic = &RawMetric{}
	rawMetic.Id = fmt.Sprintf("%s-%s", podStat.PodRef.UID, containerStat.Name)
	rawMetic.Name = containerStat.Name
	rawMetic.Namespace = podStat.PodRef.Namespace

	if podStat.CPU != nil {
		rawMetic.Time = podStat.CPU.Time.Time
	}

	if containerStat.CPU != nil {
		rawMetic.CPUStats = convertCPUStats(containerStat.CPU)
	}

	if containerStat.Memory != nil {
		rawMetic.MemoryStats = convertMemoryStats(containerStat.Memory)
	}

	return rawMetic
}

// ConvertNodeToRaw Converts Kubelet Node stats to RawMetric.
func ConvertNodeToRaw(nodeStat *stats.NodeStats) *RawMetric {
	var rawMetic *RawMetric
	rawMetic = &RawMetric{}
	rawMetic.Id = nodeStat.NodeName
	rawMetic.Name = nodeStat.NodeName

	if nodeStat.CPU != nil {
		rawMetic.Time = nodeStat.CPU.Time.Time
		rawMetic.CPUStats = convertCPUStats(nodeStat.CPU)
	}

	if nodeStat.Memory != nil {
		rawMetic.MemoryStats = convertMemoryStats(nodeStat.Memory)
	}

	return rawMetic
}
