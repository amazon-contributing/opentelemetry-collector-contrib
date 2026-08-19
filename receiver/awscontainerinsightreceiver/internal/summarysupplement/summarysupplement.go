// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Package summarysupplement recovers container-scope Container Insights metrics
// for VM-isolated pods on Linux.
//
// A VM-isolated pod (e.g. one using a Nitro/Firecracker micro-VM RuntimeClass
// such as "isolated-sandbox" or "confidential-sandbox") runs its workload inside
// a guest VM. On the host there is only an empty pod-slice cgroup with no
// container children, so the cadvisor container metrics provider — which reads
// the host cgroup filesystem — emits nothing at all for these pods (not even a
// misleading zero). The kubelet Summary API, on the other hand, is backed by the
// CRI stats provider / in-guest agent and therefore reports the real in-guest
// container CPU and memory usage.
//
// This provider reads the Summary API and emits TypeContainer records for the
// gated (VM-isolated) pods only. It is meant to run alongside the cadvisor
// provider: because cadvisor emits nothing for isolated pods, the two are
// disjoint by pod and there is no double counting. Normal pods are untouched —
// cadvisor remains their source.
package summarysupplement // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/summarysupplement"

import (
	"errors"
	"fmt"
	"strconv"

	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"
	stats "k8s.io/kubelet/pkg/apis/stats/v1alpha1"

	ci "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/containerinsight"
	cExtractor "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/cadvisor/extractors"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/k8swindows/extractors"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/stores"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/stores/kubeletutil"
)

// DefaultIsolatedRuntimeClasses are the RuntimeClass names treated as VM-isolated
// when the caller does not supply an explicit list.
var DefaultIsolatedRuntimeClasses = []string{"isolated-sandbox", "confidential-sandbox"}

// SummarySupplement is a metricsProvider that emits container-scope metrics for
// VM-isolated pods sourced from the kubelet Summary API.
type SummarySupplement struct {
	logger        *zap.Logger
	decorator     stores.Decorator
	kubeletClient *kubeletutil.KubeletClient
	hostInfo      cExtractor.CPUMemInfoProvider
	extractors    []extractors.MetricExtractor
	isolated      map[string]bool
}

// New creates a SummarySupplement.
//
// decorator should be the same decorator used by the cadvisor provider (the
// LocalNodeDecorator wrapping the shared K8sDecorator), so that emitted records
// receive identical node/instance/cluster tags and pod-spec-derived fields
// (cpu/memory limit & request, and the *_over_container_limit ratios).
//
// runtimeClasses is the set of RuntimeClass names treated as VM-isolated; when
// empty, DefaultIsolatedRuntimeClasses is used.
func New(
	logger *zap.Logger,
	decorator stores.Decorator,
	kubeletClient *kubeletutil.KubeletClient,
	hostInfo cExtractor.CPUMemInfoProvider,
	runtimeClasses []string,
) (*SummarySupplement, error) {
	if logger == nil {
		return nil, errors.New("summarysupplement: logger must not be nil")
	}
	if decorator == nil {
		return nil, errors.New("summarysupplement: decorator must not be nil")
	}
	if kubeletClient == nil {
		return nil, errors.New("summarysupplement: kubelet client must not be nil")
	}
	if hostInfo == nil {
		return nil, errors.New("summarysupplement: host info must not be nil")
	}

	if len(runtimeClasses) == 0 {
		runtimeClasses = DefaultIsolatedRuntimeClasses
	}
	isolated := make(map[string]bool, len(runtimeClasses))
	for _, rc := range runtimeClasses {
		if rc != "" {
			isolated[rc] = true
		}
	}

	// Only CPU and memory container-scope metrics are in scope. Filesystem and
	// network are intentionally omitted: they are not part of the recovered set
	// and, for VM-isolated pods, network is sandbox-level and not attributable at
	// container scope.
	metricExtractors := []extractors.MetricExtractor{
		extractors.NewCPUMetricExtractor(logger),
		extractors.NewMemMetricExtractor(logger),
	}

	return &SummarySupplement{
		logger:        logger,
		decorator:     decorator,
		kubeletClient: kubeletClient,
		hostInfo:      hostInfo,
		extractors:    metricExtractors,
		isolated:      isolated,
	}, nil
}

// GetMetrics returns container-scope OTLP metrics for VM-isolated pods only.
func (s *SummarySupplement) GetMetrics() []pmetric.Metrics {
	var result []pmetric.Metrics

	// 1. Determine which pods are VM-isolated. RuntimeClassName is a pod-spec
	//    field that is not present in the Summary API, so it must come from the
	//    kubelet /pods endpoint.
	pods, err := s.kubeletClient.ListPods()
	if err != nil {
		s.logger.Warn("summarysupplement: failed to list pods from kubelet", zap.Error(err))
		return result
	}

	isolatedUIDs := make(map[string]bool)
	for i := range pods {
		rc := pods[i].Spec.RuntimeClassName
		if rc != nil && s.isolated[*rc] {
			isolatedUIDs[string(pods[i].UID)] = true
		}
	}
	if len(isolatedUIDs) == 0 {
		// No VM-isolated pods on this node; nothing to supplement.
		return result
	}

	// 2. Pull the Summary API and build container records for the gated pods.
	summary, err := s.kubeletClient.Summary(s.logger)
	if err != nil {
		s.logger.Warn("summarysupplement: failed to get kubelet summary", zap.Error(err))
		return result
	}
	if summary == nil {
		return result
	}

	var ciMetrics []*stores.CIMetricImpl
	for i := range summary.Pods {
		pod := summary.Pods[i]
		if !isolatedUIDs[pod.PodRef.UID] {
			continue
		}
		ciMetrics = append(ciMetrics, s.containerMetrics(pod)...)
	}
	if len(ciMetrics) == 0 {
		return result
	}

	// 3. Merge per-extractor records into one record per container, decorate
	//    (adds node/instance/cluster tags and pod-spec limit/request fields), and
	//    convert to OTLP.
	ciMetrics = cExtractor.MergeMetrics(ciMetrics)
	for _, m := range s.decorate(ciMetrics) {
		result = append(result, ci.ConvertToOTLPMetrics(m.GetFields(), m.GetTags(), s.logger))
	}

	return result
}

// containerMetrics builds container-scope CIMetrics for a single pod's containers.
func (s *SummarySupplement) containerMetrics(pod stats.PodStats) []*stores.CIMetricImpl {
	var metrics []*stores.CIMetricImpl

	for _, container := range pod.Containers {
		tags := map[string]string{
			ci.PodIDKey:         pod.PodRef.UID,
			ci.K8sPodNameKey:    pod.PodRef.Name,
			ci.K8sNamespace:     pod.PodRef.Namespace,
			ci.ContainerNamekey: container.Name,
			ci.ContainerIDkey:   fmt.Sprintf("%s-%s", pod.PodRef.UID, container.Name),
		}

		rawMetric := extractors.ConvertContainerToRaw(container, pod)
		tags[ci.Timestamp] = strconv.FormatInt(rawMetric.Time.UnixNano(), 10)

		var containerMetrics []*stores.CIMetricImpl
		for _, extractor := range s.extractors {
			if extractor.HasValue(rawMetric) {
				containerMetrics = append(containerMetrics, extractor.GetValue(rawMetric, s.hostInfo, ci.TypeContainer)...)
			}
		}
		// Apply this container's tags to this container's records only.
		for _, metric := range containerMetrics {
			metric.AddTags(tags)
		}
		metrics = append(metrics, containerMetrics...)
	}

	return metrics
}

// decorate runs each CIMetric through the decorator, dropping any the decorator
// rejects (returns nil for).
func (s *SummarySupplement) decorate(in []*stores.CIMetricImpl) []*stores.CIMetricImpl {
	var out []*stores.CIMetricImpl
	for _, m := range in {
		decorated := s.decorator.Decorate(m)
		if decorated == nil {
			continue
		}
		// The decorator chain returns the same *CIMetricImpl it was given (or
		// nil when a store rejects the record). Use the comma-ok form so an
		// unexpected concrete type drops the record instead of panicking a
		// metrics receiver.
		if impl, ok := decorated.(*stores.CIMetricImpl); ok {
			out = append(out, impl)
		}
	}
	return out
}

// Shutdown releases resources held by the metric extractors.
func (s *SummarySupplement) Shutdown() error {
	var errs error
	for _, extractor := range s.extractors {
		errs = errors.Join(errs, extractor.Shutdown())
	}
	return errs
}
