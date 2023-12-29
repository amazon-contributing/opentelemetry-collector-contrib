package extractors

import (
	"time"

	cextractor "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/cadvisor/extractors"
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
	GetValue(summary *RawMetric, mInfo cextractor.CPUMemInfoProvider, containerType string) []*cextractor.CAdvisorMetric
	Shutdown() error
}
