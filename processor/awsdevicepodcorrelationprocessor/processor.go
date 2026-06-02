// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsdevicepodcorrelationprocessor // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/awsdevicepodcorrelationprocessor"

import (
	"context"
	"regexp"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"

	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/awsdevicepodcorrelationprocessor/internal/dra"
	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/awsdevicepodcorrelationprocessor/internal/kubelet"
	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/awsdevicepodcorrelationprocessor/internal/types"
)

const (
	k8sPodNameKey    = "k8s.pod.name"
	k8sNamespaceKey  = "k8s.namespace.name"
	containerNameKey = "k8s.container.name"
)

// deviceLookup is the interface used to look up device-to-pod mappings.
type deviceLookup interface {
	GetContainerInfo(deviceID string, resourceName string) *types.ContainerInfo
}

// draDeviceLookup is the interface used to look up DRA device-to-pod mappings.
type draDeviceLookup interface {
	GetDRAContainerInfo(deviceID string, driverName string) *types.ContainerInfo
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
	// Device-plugin path (always initialized if device_types configured).
	if len(p.config.DeviceTypes) > 0 {
		p.client = kubelet.NewClient(p.logger, kubelet.WithSocketPath(p.config.KubeletSocketPath))
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

		if err := p.client.Start(ctx); err != nil {
			return err
		}
	}

	// DRA path (only if dra_device_types configured).
	if len(p.config.DRADeviceTypes) > 0 {
		var configs []dra.DeviceTypeConfig
		for _, dt := range p.config.DRADeviceTypes {
			var pattern *regexp.Regexp
			if dt.DeviceIDPattern != "" {
				pattern = regexp.MustCompile(dt.DeviceIDPattern)
			}
			configs = append(configs, dra.DeviceTypeConfig{
				Name:              dt.Name,
				DeviceIDAttribute: dt.DeviceIDAttribute,
				DeviceIDSource:    string(dt.DeviceIDSource),
				DriverNames:       dt.DriverNames,
				DeviceIDPattern:   pattern,
			})
		}

		storeOpts := []dra.StoreOption{dra.WithConfigs(configs)}
		if p.config.NodeName != "" {
			storeOpts = append(storeOpts, dra.WithNodeName(p.config.NodeName))
		}

		p.draStore = dra.NewStore(p.logger, storeOpts...)

		if err := p.draStore.Start(ctx); err != nil {
			// DRA failure is non-fatal if device-plugin path is also configured.
			if p.lookup != nil {
				p.logger.Warn("DRA store failed to start, DRA correlation unavailable", zap.Error(err))
				p.draStore = nil
			} else {
				return err
			}
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
func processDatapoints[DP interface{ Attributes() pcommon.Map }](
	datapoints interface {
		Len() int
		At(int) DP
	},
	resourceAttrs pcommon.Map,
	p *devicePodCorrelationProcessor,
) {
	for i := 0; i < datapoints.Len(); i++ {
		dpAttrs := datapoints.At(i).Attributes()

		if _, exists := dpAttrs.Get(k8sPodNameKey); exists {
			p.logger.Debug("Skipping datapoint, pod attributes already present")
			continue
		}

		// Try device-plugin path first.
		if p.lookup != nil && p.correlateDevicePlugin(dpAttrs, resourceAttrs) {
			continue
		}

		// Try DRA path.
		if p.draLookup != nil {
			p.correlateDRA(dpAttrs, resourceAttrs)
		}
	}
}

func (p *devicePodCorrelationProcessor) correlateDevicePlugin(dpAttrs pcommon.Map, resourceAttrs pcommon.Map) bool {
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
		var containerInfo *types.ContainerInfo
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

func (p *devicePodCorrelationProcessor) correlateDRA(dpAttrs pcommon.Map, resourceAttrs pcommon.Map) bool {
	for _, dt := range p.config.DRADeviceTypes {
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
		var containerInfo *types.ContainerInfo
		for _, dn := range dt.DriverNames {
			containerInfo = p.draLookup.GetDRAContainerInfo(deviceID, dn)
			if containerInfo != nil {
				break
			}
		}

		if containerInfo != nil {
			p.logger.Debug("Correlated DRA device to pod",
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
