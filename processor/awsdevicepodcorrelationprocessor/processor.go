// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsdevicepodcorrelationprocessor

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"
)

const (
	k8sPodNameKey      = "k8s.pod.name"
	k8sNamespaceKey    = "k8s.namespace.name"
	containerNameKey   = "k8s.container.name"
)

// ContainerInfo holds Kubernetes pod/container metadata for a device.
// This mirrors the ContainerInfo struct from the PodResourcesStore in
// awscontainerinsightreceiver/internal/stores, defined locally to avoid
// importing an internal package across module boundaries.
type ContainerInfo struct {
	PodName       string
	ContainerName string
	Namespace     string
}

// PodResourcesStoreInterface abstracts the PodResourcesStore for testing
// and to decouple from the internal stores package.
type PodResourcesStoreInterface interface {
	GetContainerInfo(deviceID string, resourceName string) *ContainerInfo
	AddResourceName(resourceName string)
	Shutdown()
}

// PodResourcesStoreFactory is a function that creates a PodResourcesStoreInterface.
// This allows the factory to inject the real PodResourcesStore constructor at runtime
// while keeping the processor testable with mocks.
type PodResourcesStoreFactory func(logger *zap.Logger) (PodResourcesStoreInterface, error)

type devicePodCorrelationProcessor struct {
	config            *Config
	logger            *zap.Logger
	podResourcesStore PodResourcesStoreInterface
	storeFactory      PodResourcesStoreFactory
}

func newProcessor(cfg *Config, logger *zap.Logger, storeFactory PodResourcesStoreFactory) *devicePodCorrelationProcessor {
	return &devicePodCorrelationProcessor{
		config:       cfg,
		logger:       logger,
		storeFactory: storeFactory,
	}
}

// Start initializes the processor by obtaining the PodResourcesStore singleton
// and registering all configured resource names.
func (p *devicePodCorrelationProcessor) Start(_ context.Context, _ component.Host) error {
	store, err := p.storeFactory(p.logger)
	if err != nil {
		return err
	}
	p.podResourcesStore = store

	// Register each unique resource name across all device types.
	seen := make(map[string]struct{})
	for _, dt := range p.config.DeviceTypes {
		for _, rn := range dt.ResourceNames {
			if _, ok := seen[rn]; !ok {
				seen[rn] = struct{}{}
				p.podResourcesStore.AddResourceName(rn)
			}
		}
	}
	return nil
}

// Shutdown releases the PodResourcesStore resources.
func (p *devicePodCorrelationProcessor) Shutdown(_ context.Context) error {
	if p.podResourcesStore != nil {
		p.podResourcesStore.Shutdown()
	}
	return nil
}

// processMetrics iterates all datapoints in the metric batch and enriches them
// with pod/namespace/container attributes when a device ID matches a configured
// device type and the PodResourcesStore has correlation data.
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
					processDatapoints(m.Gauge().DataPoints(), resourceAttrs, p.config.DeviceTypes, p.podResourcesStore, p.logger)
				case pmetric.MetricTypeSum:
					processDatapoints(m.Sum().DataPoints(), resourceAttrs, p.config.DeviceTypes, p.podResourcesStore, p.logger)
				case pmetric.MetricTypeHistogram:
					processDatapoints(m.Histogram().DataPoints(), resourceAttrs, p.config.DeviceTypes, p.podResourcesStore, p.logger)
				case pmetric.MetricTypeExponentialHistogram:
					processDatapoints(m.ExponentialHistogram().DataPoints(), resourceAttrs, p.config.DeviceTypes, p.podResourcesStore, p.logger)
				case pmetric.MetricTypeSummary:
					processDatapoints(m.Summary().DataPoints(), resourceAttrs, p.config.DeviceTypes, p.podResourcesStore, p.logger)
				default:
					// Skip metrics with unsupported or empty type without error.
				}
			}
		}
	}
	return md, nil
}

// processDatapoints is a generic helper that enriches datapoints with pod
// correlation attributes. It works with any datapoint type that exposes
// Attributes() pcommon.Map. For device types with DeviceIDSource "resource",
// the device ID is read from the resource attributes instead of the datapoint.
func processDatapoints[DP interface{ Attributes() pcommon.Map }](
	datapoints interface {
		Len() int
		At(int) DP
	},
	resourceAttrs pcommon.Map,
	deviceTypes []DeviceTypeConfig,
	store PodResourcesStoreInterface,
	logger *zap.Logger,
) {
	for i := 0; i < datapoints.Len(); i++ {
		dpAttrs := datapoints.At(i).Attributes()

		// Skip if pod attributes are already present.
		if _, exists := dpAttrs.Get(k8sPodNameKey); exists {
			logger.Debug("Skipping datapoint, pod attributes already present")
			continue
		}

		for _, dt := range deviceTypes {
			// Choose the attribute source based on config.
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
			var containerInfo *ContainerInfo
			for _, rn := range dt.ResourceNames {
				containerInfo = store.GetContainerInfo(deviceID, rn)
				if containerInfo != nil {
					break
				}
			}

			if containerInfo != nil {
				logger.Debug("Correlated device to pod",
					zap.String("device_type", dt.Name),
					zap.String("device_id", deviceID),
					zap.String("pod", containerInfo.PodName),
					zap.String("namespace", containerInfo.Namespace),
					zap.String("container", containerInfo.ContainerName),
				)
				dpAttrs.PutStr(k8sPodNameKey, containerInfo.PodName)
				dpAttrs.PutStr(k8sNamespaceKey, containerInfo.Namespace)
				dpAttrs.PutStr(containerNameKey, containerInfo.ContainerName)
				break // First matching device type wins.
			}

			logger.Debug("No pod correlation found for device",
				zap.String("device_type", dt.Name),
				zap.String("device_id", deviceID),
			)
		}
	}
}
