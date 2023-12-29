package extractors

import (
	"time"

	cExtractor "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/cadvisor/extractors"
	stats "k8s.io/kubelet/pkg/apis/stats/v1alpha1"
)

// RawMetric Represent Container, Pod, Node Metric  Extractors.
// Kubelet summary and HNS stats will be converted to Raw Metric for parsing by Extractors.
type RawMetric struct {
	Id          string
	Name        string
	Namespace   string
	Time        time.Time
	CPUStats    *stats.CPUStats
	MemoryStats *stats.MemoryStats
}

type MetricExtractor interface {
	HasValue(summary *RawMetric) bool
	GetValue(summary *RawMetric, mInfo cExtractor.CPUMemInfoProvider, containerType string) []*cExtractor.CAdvisorMetric
	Shutdown() error
}
