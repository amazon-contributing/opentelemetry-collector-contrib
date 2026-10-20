// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsdevicepodcorrelationprocessor // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/awsdevicepodcorrelationprocessor"

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"

	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/awsdevicepodcorrelationprocessor/internal/dra"
	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/awsdevicepodcorrelationprocessor/internal/kubelet"
	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/awsdevicepodcorrelationprocessor/internal/podinfo"
)

const (
	k8sPodNameKey    = "k8s.pod.name"
	k8sNamespaceKey  = "k8s.namespace.name"
	containerNameKey = "k8s.container.name"
)

// deviceLookup is the interface used to look up device-to-pod mappings.
type deviceLookup interface {
	GetContainerInfo(deviceID, resourceName string) *podinfo.ContainerInfo
}

// draDeviceLookup is the interface used to look up DRA device-to-pod mappings.
// A device may be used by more than one pod, so it returns all owners.
type draDeviceLookup interface {
	GetDRAContainerInfo(deviceID, driverName string) []podinfo.ContainerInfo
}

type devicePodCorrelationProcessor struct {
	config    *Config
	logger    *zap.Logger
	client    *kubelet.Client
	lookup    deviceLookup
	draStore  *dra.Store
	draLookup draDeviceLookup
}

func newProcessor(cfg *Config, logger *zap.Logger) *devicePodCorrelationProcessor {
	return &devicePodCorrelationProcessor{
		config: cfg,
		logger: logger,
	}
}

// Start creates the Kubelet Pod Resources API client and registers
// all configured resource names. If DRA device types are configured,
// also starts the K8s API informer-based DRA store.
func (p *devicePodCorrelationProcessor) Start(ctx context.Context, _ component.Host) error {
	devicePlugin := len(p.config.DeviceTypes) > 0
	draEnabled := len(p.config.DRADeviceTypes) > 0

	// Both paths read the same node-local Kubelet Pod Resources API, so a single
	// client serves both: the device-plugin path keys devices by resource name,
	// the DRA path consumes the raw DynamicResources bindings via the store.
	if devicePlugin || draEnabled {
		opts := []kubelet.ClientOption{kubelet.WithSocketPath(p.config.KubeletSocketPath)}
		if draEnabled {
			opts = append(opts, kubelet.WithDRA())
		}
		p.client = kubelet.NewClient(p.logger, opts...)

		if devicePlugin {
			p.lookup = p.client
			seen := make(map[string]struct{})
			for _, dt := range p.config.DeviceTypes {
				for _, rn := range dt.ResourceNames {
					if _, ok := seen[rn]; !ok {
						seen[rn] = struct{}{}
						p.client.AddResourceName(rn)
					}
				}
			}
		}

		if err := p.client.Start(ctx); err != nil {
			return err
		}
	}

	// DRA path (only if dra_device_types configured).
	if draEnabled {
		var configs []dra.DeviceTypeConfig
		for _, dt := range p.config.DRADeviceTypes {
			configs = append(configs, dra.DeviceTypeConfig{
				Name:            dt.Name,
				DriverNames:     dt.DriverNames,
				SourceAttribute: dt.DRADeviceIDAttribute,
			})
		}

		storeOpts := []dra.StoreOption{
			dra.WithConfigs(configs),
			dra.WithBindingSource(p.client),
		}
		if p.config.NodeName != "" {
			storeOpts = append(storeOpts, dra.WithNodeName(p.config.NodeName))
		}

		p.draStore = dra.NewStore(p.logger, storeOpts...)

		if err := p.draStore.Start(ctx); err != nil {
			// DRA failure is non-fatal if the device-plugin path is also configured.
			if p.lookup == nil {
				return err
			}
			p.logger.Warn("DRA store failed to start, DRA correlation unavailable", zap.Error(err))
			p.draStore = nil
		} else {
			p.draLookup = p.draStore
		}
	}

	return nil
}

// Shutdown stops the kubelet client and DRA store.
func (p *devicePodCorrelationProcessor) Shutdown(_ context.Context) error {
	if p.client != nil {
		p.client.Stop()
	}
	if p.draStore != nil {
		p.draStore.Stop()
	}
	return nil
}

// processMetrics iterates all datapoints in the metric batch and enriches them
// with pod/namespace/container attributes when a device ID matches a configured
// device type and the correlation store has data.
func (p *devicePodCorrelationProcessor) processMetrics(_ context.Context, md pmetric.Metrics) (pmetric.Metrics, error) {
	rms := md.ResourceMetrics()
	for i := 0; i < rms.Len(); i++ {
		rm := rms.At(i)
		resourceAttrs := rm.Resource().Attributes()
		ilms := rm.ScopeMetrics()
		for j := 0; j < ilms.Len(); j++ {
			metrics := ilms.At(j).Metrics()
			for k := 0; k < metrics.Len(); k++ {
				m := metrics.At(k)
				switch m.Type() {
				case pmetric.MetricTypeGauge:
					processDatapoints(m.Gauge().DataPoints(), resourceAttrs, p)
				case pmetric.MetricTypeSum:
					processDatapoints(m.Sum().DataPoints(), resourceAttrs, p)
				case pmetric.MetricTypeHistogram:
					processDatapoints(m.Histogram().DataPoints(), resourceAttrs, p)
				case pmetric.MetricTypeExponentialHistogram:
					processDatapoints(m.ExponentialHistogram().DataPoints(), resourceAttrs, p)
				case pmetric.MetricTypeSummary:
					processDatapoints(m.Summary().DataPoints(), resourceAttrs, p)
				default:
				}
			}
		}
	}
	return md, nil
}

// processDatapoints enriches datapoints with pod correlation attributes.
//
// The DRA path may fan a datapoint out to multiple pods (a shared device), so
// the datapoint collection must support appending clones. Only the datapoints
// present at entry are processed (the length is snapshotted), so appended clones
// are not re-processed.
func processDatapoints[DP interface {
	Attributes() pcommon.Map
	CopyTo(DP)
}](
	datapoints interface {
		Len() int
		At(int) DP
		AppendEmpty() DP
	},
	resourceAttrs pcommon.Map,
	p *devicePodCorrelationProcessor,
) {
	original := datapoints.Len()
	for i := 0; i < original; i++ {
		dp := datapoints.At(i)
		dpAttrs := dp.Attributes()

		if _, exists := dpAttrs.Get(k8sPodNameKey); exists {
			p.logger.Debug("Skipping datapoint, pod attributes already present")
			continue
		}

		// Try device-plugin path first (single owner, enriched in place).
		if p.lookup != nil && p.correlateDevicePlugin(dpAttrs, resourceAttrs) {
			continue
		}

		// Try DRA path (may fan out to multiple pods).
		if p.draLookup != nil {
			correlateDRA(dp, datapoints, dpAttrs, resourceAttrs, p)
		}
	}
}

// setPodAttrs stamps pod/namespace/container identity onto a datapoint's attributes.
func setPodAttrs(attrs pcommon.Map, info podinfo.ContainerInfo) {
	attrs.PutStr(k8sPodNameKey, info.PodName)
	attrs.PutStr(k8sNamespaceKey, info.Namespace)
	attrs.PutStr(containerNameKey, info.ContainerName)
}

func (p *devicePodCorrelationProcessor) correlateDevicePlugin(dpAttrs, resourceAttrs pcommon.Map) bool {
	for _, dt := range p.config.DeviceTypes {
		var sourceAttrs pcommon.Map
		if dt.DeviceIDSource == DeviceIDSourceResource {
			sourceAttrs = resourceAttrs
		} else {
			sourceAttrs = dpAttrs
		}

		deviceIDVal, found := sourceAttrs.Get(dt.DeviceIDAttribute)
		if !found {
			continue
		}

		deviceID := deviceIDVal.AsString()
		var containerInfo *podinfo.ContainerInfo
		for _, rn := range dt.ResourceNames {
			containerInfo = p.lookup.GetContainerInfo(deviceID, rn)
			if containerInfo != nil {
				break
			}
		}

		if containerInfo != nil {
			p.logger.Debug("Correlated device to pod",
				zap.String("device_type", dt.Name),
				zap.String("device_id", deviceID),
				zap.String("pod", containerInfo.PodName),
				zap.String("namespace", containerInfo.Namespace),
				zap.String("container", containerInfo.ContainerName),
			)
			dpAttrs.PutStr(k8sPodNameKey, containerInfo.PodName)
			dpAttrs.PutStr(k8sNamespaceKey, containerInfo.Namespace)
			dpAttrs.PutStr(containerNameKey, containerInfo.ContainerName)
			return true
		}
	}
	return false
}

// correlateDRA enriches a datapoint with DRA pod correlation. When the matched
// device is used by more than one pod, the datapoint is fanned out — one clone
// per additional owning pod — so each consuming pod gets its own series.
func correlateDRA[DP interface {
	Attributes() pcommon.Map
	CopyTo(DP)
}](
	dp DP,
	appender interface{ AppendEmpty() DP },
	dpAttrs, resourceAttrs pcommon.Map,
	p *devicePodCorrelationProcessor,
) {
	for _, dt := range p.config.DRADeviceTypes {
		sourceAttrs := dpAttrs
		if dt.DeviceIDSource == DeviceIDSourceResource {
			sourceAttrs = resourceAttrs
		}

		deviceIDVal, found := sourceAttrs.Get(dt.DeviceIDAttribute)
		if !found {
			continue
		}

		deviceID := deviceIDVal.AsString()
		var owners []podinfo.ContainerInfo
		for _, dn := range dt.DriverNames {
			if owners = p.draLookup.GetDRAContainerInfo(deviceID, dn); len(owners) > 0 {
				break
			}
		}
		if len(owners) == 0 {
			continue
		}

		p.logger.Debug("Correlated DRA device to pod(s)",
			zap.String("device_type", dt.Name),
			zap.String("device_id", deviceID),
			zap.Int("pod_count", len(owners)),
		)

		// Clone the pristine datapoint for each additional owner first, then stamp
		// identities, so a device shared by N pods yields N series (one per pod).
		clones := make([]DP, len(owners)-1)
		for k := 1; k < len(owners); k++ {
			clone := appender.AppendEmpty()
			dp.CopyTo(clone)
			clones[k-1] = clone
		}
		setPodAttrs(dpAttrs, owners[0])
		for k, clone := range clones {
			setPodAttrs(clone.Attributes(), owners[k+1])
		}
		return
	}
}
