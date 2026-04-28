// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package costallocation // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/costallocation"

import (
	"math"
	"sync"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"

	ci "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/containerinsight"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/stores"
)

// ECIMetricsProvider implements MetricsDataProvider by reading from the existing
// Container Insights metrics that flow through the receiver's CIMetric pipeline.
// It collects metrics during the decoration phase and serves them to the CostAllocator.
type ECIMetricsProvider struct {
	mu sync.RWMutex

	// nodeMetrics maps NodeName -> latest CIMetric for that node (type=Node)
	nodeMetrics map[string]*stores.CIMetricImpl
	// podMetrics maps NodeName -> []*CIMetricImpl for pods on that node (type=Pod)
	podMetrics map[string][]*stores.CIMetricImpl
	// containerMetrics maps "namespace/podName" -> []*CIMetricImpl for containers (type=Container)
	containerMetrics map[string][]*stores.CIMetricImpl
	// nodeLimits maps NodeName -> node resource limits (cpu_limit, memory_limit, gpu_limit)
	nodeLimits map[string]nodeLimits

	logger *zap.Logger
}

// nodeLimits holds the resource limits for a node, used as denominators for container request_pct.
type nodeLimits struct {
	cpuLimit float64
	memLimit float64
	gpuLimit float64
}

// NewECIMetricsProvider creates a new ECIMetricsProvider.
func NewECIMetricsProvider(logger *zap.Logger) *ECIMetricsProvider {
	return &ECIMetricsProvider{
		nodeMetrics:      make(map[string]*stores.CIMetricImpl),
		podMetrics:       make(map[string][]*stores.CIMetricImpl),
		containerMetrics: make(map[string][]*stores.CIMetricImpl),
		nodeLimits:       make(map[string]nodeLimits),
		logger:           logger,
	}
}

// CollectMetrics is called each collection interval with the latest CIMetrics.
// It replaces the previous snapshot entirely.
func (p *ECIMetricsProvider) CollectMetrics(metrics []*stores.CIMetricImpl) {
	nodes := make(map[string]*stores.CIMetricImpl)
	pods := make(map[string][]*stores.CIMetricImpl)
	containers := make(map[string][]*stores.CIMetricImpl)
	limits := make(map[string]nodeLimits)

	for _, m := range metrics {
		mType := m.GetTag(ci.MetricType)
		switch mType {
		case ci.TypeNode:
			nodeName := m.GetTag(ci.NodeNameKey)
			if nodeName != "" {
				nodes[nodeName] = m
				limits[nodeName] = nodeLimits{
					cpuLimit: getFloatField(m, ci.CPULimit),
					memLimit: getFloatField(m, ci.MemLimit),
					gpuLimit: getFloatField(m, ci.GpuLimit),
				}
			}
		case ci.TypePod:
			nodeName := m.GetTag(ci.NodeNameKey)
			if nodeName != "" {
				pods[nodeName] = append(pods[nodeName], m)
			}
		case ci.TypeContainer:
			ns := m.GetTag(ci.K8sNamespace)
			podName := m.GetTag(ci.K8sPodNameKey)
			if ns != "" && podName != "" {
				key := ns + "/" + podName
				containers[key] = append(containers[key], m)
			}
		}
	}

	p.mu.Lock()
	p.nodeMetrics = nodes
	p.podMetrics = pods
	p.containerMetrics = containers
	p.nodeLimits = limits
	p.mu.Unlock()
}

// safeString extracts a string from a pcommon.Value returned by attrs.Get().
// Must be called with the ok bool from Get() to avoid nil dereference.
func safeStringFromAttrs(attrs pcommon.Map, key string) string {
	v, ok := attrs.Get(key)
	if !ok {
		return ""
	}
	return v.AsString()
}

// getFloatField safely extracts a float64 from a CIMetric field.
func getFloatField(m *stores.CIMetricImpl, key string) float64 {
	v := m.GetField(key)
	if v == nil {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return val
	case int64:
		return float64(val)
	case int:
		return float64(val)
	case uint64:
		return float64(val)
	case int32:
		return float64(val)
	case uint32:
		return float64(val)
	default:
		return 0
	}
}

// GetNodeData returns cost input data for all nodes.
func (p *ECIMetricsProvider) GetNodeData() ([]NodeCostInput, []NodeAttributes) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var inputs []NodeCostInput
	var attrs []NodeAttributes

	for nodeName, m := range p.nodeMetrics {
		input := NodeCostInput{
			InstanceType:   m.GetTag(ci.InstanceType),
			CPUUtilization: getFloatField(m, ci.CPUUtilization),
			CPUReserved:    getFloatField(m, ci.CPUReservedCapacity),
			MemUtilization: getFloatField(m, ci.MemUtilization),
			MemReserved:    getFloatField(m, ci.MemReservedCapacity),
			GPUUtilization: getFloatField(m, ci.GpuUsageTotal),
			GPUReserved:    getFloatField(m, ci.GpuReservedCapacity),
		}
		inputs = append(inputs, input)

		attr := NodeAttributes{
			ClusterName: m.GetTag(ci.ClusterNameKey),
			InstanceID:  m.GetTag(ci.InstanceID),
			NodeName:    nodeName,
		}
		attrs = append(attrs, attr)
	}

	return inputs, attrs
}

// GetPodData returns cost input data for all pods on a given node.
func (p *ECIMetricsProvider) GetPodData(nodeName string) []PodCostInput {
	p.mu.RLock()
	defer p.mu.RUnlock()

	podList := p.podMetrics[nodeName]
	if len(podList) == 0 {
		return nil
	}

	results := make([]PodCostInput, 0, len(podList))
	for _, m := range podList {
		cpuReserved := getFloatField(m, ci.CPUReservedCapacity)
		cpuUtil := getFloatField(m, ci.CPUUtilization)
		memReserved := getFloatField(m, ci.MemReservedCapacity)
		memUtil := getFloatField(m, ci.MemUtilization)
		gpuReserved := getFloatField(m, ci.GpuReservedCapacity)
		gpuUtil := getFloatField(m, ci.GpuUsageTotal)

		results = append(results, PodCostInput{
			PodName:        m.GetTag(ci.K8sPodNameKey),
			Namespace:      m.GetTag(ci.K8sNamespace),
			NodeName:       nodeName,
			CPUEffective:   math.Max(cpuReserved, cpuUtil),
			MemEffective:   math.Max(memReserved, memUtil),
			GPUEffective:   math.Max(gpuReserved, gpuUtil),
			CPUUtilization: cpuUtil,
			MemUtilization: memUtil,
			GPUUtilization: gpuUtil,
		})
	}
	return results
}

// GetContainerData returns cost input data for all containers in a given pod.
func (p *ECIMetricsProvider) GetContainerData(podName, namespace string) []ContainerCostInput {
	p.mu.RLock()
	defer p.mu.RUnlock()

	key := namespace + "/" + podName
	containerList := p.containerMetrics[key]
	if len(containerList) == 0 {
		return nil
	}

	// Find the node this pod runs on to get node limits for request_pct denominator.
	var nl nodeLimits
	for nodeName, podList := range p.podMetrics {
		for _, pm := range podList {
			if pm.GetTag(ci.K8sPodNameKey) == podName && pm.GetTag(ci.K8sNamespace) == namespace {
				nl = p.nodeLimits[nodeName]
				break
			}
		}
		if nl.cpuLimit > 0 || nl.memLimit > 0 || nl.gpuLimit > 0 {
			break
		}
	}

	results := make([]ContainerCostInput, 0, len(containerList))
	for _, m := range containerList {
		cpuUtil := getFloatField(m, ci.CPUUtilization)
		cpuRequest := getFloatField(m, ci.CPURequest)
		memUtil := getFloatField(m, ci.MemUtilization)
		memRequest := getFloatField(m, ci.MemRequest)
		gpuUtil := getFloatField(m, ci.GpuUsageTotal)
		gpuRequest := getFloatField(m, ci.GpuRequest)

		// container_cpu_request_pct = container_cpu_request / node_cpu_limit × 100
		cpuRequestPct := 0.0
		if nl.cpuLimit > 0 {
			cpuRequestPct = (cpuRequest / nl.cpuLimit) * 100
		}
		memRequestPct := 0.0
		if nl.memLimit > 0 {
			memRequestPct = (memRequest / nl.memLimit) * 100
		}
		gpuRequestPct := 0.0
		if nl.gpuLimit > 0 {
			gpuRequestPct = (gpuRequest / nl.gpuLimit) * 100
		}

		results = append(results, ContainerCostInput{
			ContainerName:  m.GetTag(ci.ContainerNamekey),
			PodName:        podName,
			Namespace:      namespace,
			CPUEffective:   math.Max(cpuUtil, cpuRequestPct),
			MemEffective:   math.Max(memUtil, memRequestPct),
			GPUEffective:   math.Max(gpuUtil, gpuRequestPct),
			CPUUtilization: cpuUtil,
			MemUtilization: memUtil,
			GPUUtilization: gpuUtil,
		})
	}
	return results
}

// GetNodeCount returns the total number of nodes.
func (p *ECIMetricsProvider) GetNodeCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.nodeMetrics)
}

// CollectFromOTLPMetrics extracts cost-relevant data from already-converted pmetric.Metrics.
// This is used when raw CIMetrics aren't directly accessible.
func (p *ECIMetricsProvider) CollectFromOTLPMetrics(mds []pmetric.Metrics) {
	nodes := make(map[string]*stores.CIMetricImpl)
	pods := make(map[string][]*stores.CIMetricImpl)
	containers := make(map[string][]*stores.CIMetricImpl)
	limits := make(map[string]nodeLimits)

	for _, md := range mds {
		for i := 0; i < md.ResourceMetrics().Len(); i++ {
			rm := md.ResourceMetrics().At(i)
			resAttrs := rm.Resource().Attributes()
			for j := 0; j < rm.ScopeMetrics().Len(); j++ {
				sm := rm.ScopeMetrics().At(j)
				p.extractFromScopeMetrics(sm, resAttrs, nodes, pods, containers, limits)
			}
		}
	}
	p.mu.Lock()
	p.nodeMetrics = nodes
	p.podMetrics = pods
	p.containerMetrics = containers
	p.nodeLimits = limits
	p.mu.Unlock()
}

// extractFromScopeMetrics processes a single ScopeMetrics and populates the maps.
func (p *ECIMetricsProvider) extractFromScopeMetrics(
	sm pmetric.ScopeMetrics,
	resAttrs pcommon.Map,
	nodes map[string]*stores.CIMetricImpl,
	pods map[string][]*stores.CIMetricImpl,
	containers map[string][]*stores.CIMetricImpl,
	nodeLimitsMap map[string]nodeLimits,
) {
	// Extract entity attributes from resource level.
	// Note: AddKubernetesInfo moves K8sPodNameKey into the kubernetes JSON blob,
	// so the resource attribute uses PodNameKey ("PodName") not K8sPodNameKey ("K8sPodName").
	nn := safeStringFromAttrs(resAttrs, ci.NodeNameKey)
	ns := safeStringFromAttrs(resAttrs, ci.K8sNamespace)
	pn := safeStringFromAttrs(resAttrs, ci.PodNameKey)
	if pn == "" {
		pn = safeStringFromAttrs(resAttrs, ci.K8sPodNameKey)
	}
	cn := safeStringFromAttrs(resAttrs, ci.ContainerNamekey)

	for i := 0; i < sm.Metrics().Len(); i++ {
		m := sm.Metrics().At(i)
		name := m.Name()

		// Determine the hierarchy level from the metric name prefix.
		var metricType string
		var measurement string
		switch {
		case len(name) > 5 && name[:5] == "node_":
			metricType = ci.TypeNode
			measurement = name[5:]
		case len(name) > 4 && name[:4] == "pod_":
			metricType = ci.TypePod
			measurement = name[4:]
		case len(name) > 10 && name[:10] == "container_":
			metricType = ci.TypeContainer
			measurement = name[10:]
		default:
			continue
		}

		if m.Type() != pmetric.MetricTypeGauge {
			continue
		}
		gauge := m.Gauge()
		for j := 0; j < gauge.DataPoints().Len(); j++ {
			dp := gauge.DataPoints().At(j)
			value := dp.DoubleValue()

			// Also check data point attributes as fallback.
			dpAttrs := dp.Attributes()
			if nn == "" {
				nn = safeStringFromAttrs(dpAttrs, ci.NodeNameKey)
			}

			switch metricType {
			case ci.TypeNode:
				if nn == "" {
					continue
				}
				if _, ok := nodes[nn]; !ok {
					impl := stores.NewCIMetric(ci.TypeNode, p.logger)
					impl.AddTag(ci.NodeNameKey, nn)
					it := safeStringFromAttrs(resAttrs, ci.InstanceType)
					if it == "" {
						it = safeStringFromAttrs(dpAttrs, ci.InstanceType)
					}
					iid := safeStringFromAttrs(resAttrs, ci.InstanceID)
					if iid == "" {
						iid = safeStringFromAttrs(dpAttrs, ci.InstanceID)
					}
					cln := safeStringFromAttrs(resAttrs, ci.ClusterNameKey)
					if cln == "" {
						cln = safeStringFromAttrs(dpAttrs, ci.ClusterNameKey)
					}
					impl.AddTag(ci.InstanceType, it)
					impl.AddTag(ci.InstanceID, iid)
					impl.AddTag(ci.ClusterNameKey, cln)
					nodes[nn] = impl
				}
				nodes[nn].AddField(measurement, value)

				nl := nodeLimitsMap[nn]
				switch measurement {
				case ci.CPULimit:
					nl.cpuLimit = value
				case ci.MemLimit:
					nl.memLimit = value
				case ci.GpuLimit:
					nl.gpuLimit = value
				}
				nodeLimitsMap[nn] = nl

			case ci.TypePod:
				if ns == "" || pn == "" {
					continue
				}
				key := ns + "/" + pn
				var found *stores.CIMetricImpl
				for _, existing := range pods[nn] {
					if existing.GetTag(ci.K8sPodNameKey) == pn && existing.GetTag(ci.K8sNamespace) == ns {
						found = existing
						break
					}
				}
				if found == nil {
					found = stores.NewCIMetric(ci.TypePod, p.logger)
					found.AddTag(ci.NodeNameKey, nn)
					found.AddTag(ci.K8sNamespace, ns)
					found.AddTag(ci.K8sPodNameKey, pn)
					pods[nn] = append(pods[nn], found)
					_ = key
				}
				found.AddField(measurement, value)

			case ci.TypeContainer:
				if ns == "" || pn == "" || cn == "" {
					continue
				}
				key := ns + "/" + pn
				var found *stores.CIMetricImpl
				for _, existing := range containers[key] {
					if existing.GetTag(ci.ContainerNamekey) == cn {
						found = existing
						break
					}
				}
				if found == nil {
					found = stores.NewCIMetric(ci.TypeContainer, p.logger)
					found.AddTag(ci.K8sNamespace, ns)
					found.AddTag(ci.K8sPodNameKey, pn)
					found.AddTag(ci.ContainerNamekey, cn)
					containers[key] = append(containers[key], found)
				}
				found.AddField(measurement, value)
			}
		}
	}
}
