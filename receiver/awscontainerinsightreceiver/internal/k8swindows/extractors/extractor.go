package extractors

import (
	"time"

	cExtractor "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/cadvisor/extractors"
)

// CPUStat for Pod, Container and Node.
type CPUStat struct {
	Time                 time.Time
	UsageNanoCores       uint64
	UsageCoreNanoSeconds uint64
}

// MemoryStat for Pod, Container and Node
type MemoryStat struct {
	Time            time.Time
	AvailableBytes  uint64
	UsageBytes      uint64
	WorkingSetBytes uint64
	RSSBytes        uint64
	PageFaults      uint64
	MajorPageFaults uint64
}

// RawMetric Represent Container, Pod, Node Metric  Extractors.
// Kubelet summary and HNS stats will be converted to Raw Metric for parsing by Extractors.
type RawMetric struct {
	Id          string
	Name        string
	Namespace   string
	Time        time.Time
	CPUStats    CPUStat
	MemoryStats MemoryStat
}

type MetricExtractor interface {
	HasValue(summary *RawMetric) bool
	GetValue(summary *RawMetric, mInfo cExtractor.CPUMemInfoProvider, containerType string) []*cExtractor.CAdvisorMetric
	Shutdown() error
}
