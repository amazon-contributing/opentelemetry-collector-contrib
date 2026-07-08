// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsdeviceslurmjobcorrelationprocessor // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/awsdeviceslurmjobcorrelationprocessor"

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"

	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/awsdeviceslurmjobcorrelationprocessor/internal/slurm"
)

const (
	slurmJobIDKey        = "slurm.job.id"
	slurmJobNameKey      = "slurm.job.name"
	slurmJobUserKey      = "slurm.job.user"
	slurmJobAccountKey   = "slurm.job.account"
	slurmJobPartitionKey = "slurm.job.partition"
)

// jobLookup is the interface for resolving device→job mappings.
type jobLookup interface {
	GetJobInfo(deviceID string, deviceType string) *slurm.JobInfo
}

type slurmJobCorrelationProcessor struct {
	config     *Config
	logger     *zap.Logger
	resolver   *slurm.Resolver
	mockLookup jobLookup // for testing only
}

func newProcessor(cfg *Config, logger *zap.Logger) *slurmJobCorrelationProcessor {
	return &slurmJobCorrelationProcessor{
		config: cfg,
		logger: logger,
	}
}

func (p *slurmJobCorrelationProcessor) Start(ctx context.Context, _ component.Host) error {
	cgroupResolver := slurm.NewCgroupResolver(
		p.config.CgroupRoot,
		p.config.CgroupVersion,
		p.config.HostPath,
		p.logger,
	)

	scontrolClient := slurm.NewScontrolClient(p.logger)

	p.resolver = slurm.NewResolver(
		cgroupResolver,
		scontrolClient,
		p.config.NodeExclusive,
		p.config.MetadataPollInterval,
		p.logger,
	)

	return p.resolver.Start(ctx)
}

func (p *slurmJobCorrelationProcessor) Shutdown(_ context.Context) error {
	if p.resolver != nil {
		p.resolver.Stop()
	}
	return nil
}

func (p *slurmJobCorrelationProcessor) getLookup() jobLookup {
	if p.mockLookup != nil {
		return p.mockLookup
	}
	return p.resolver
}

func (p *slurmJobCorrelationProcessor) processMetrics(_ context.Context, md pmetric.Metrics) (pmetric.Metrics, error) {
	lookup := p.getLookup()
	if lookup == nil {
		return md, nil
	}

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
					processDatapoints(m.Gauge().DataPoints(), resourceAttrs, p.config.DeviceTypes, lookup, p.logger)
				case pmetric.MetricTypeSum:
					processDatapoints(m.Sum().DataPoints(), resourceAttrs, p.config.DeviceTypes, lookup, p.logger)
				case pmetric.MetricTypeHistogram:
					processDatapoints(m.Histogram().DataPoints(), resourceAttrs, p.config.DeviceTypes, lookup, p.logger)
				case pmetric.MetricTypeExponentialHistogram:
					processDatapoints(m.ExponentialHistogram().DataPoints(), resourceAttrs, p.config.DeviceTypes, lookup, p.logger)
				case pmetric.MetricTypeSummary:
					processDatapoints(m.Summary().DataPoints(), resourceAttrs, p.config.DeviceTypes, lookup, p.logger)
				default:
				}
			}
		}
	}
	return md, nil
}

func processDatapoints[DP interface{ Attributes() pcommon.Map }](
	datapoints interface {
		Len() int
		At(int) DP
	},
	resourceAttrs pcommon.Map,
	deviceTypes []DeviceTypeConfig,
	lookup jobLookup,
	logger *zap.Logger,
) {
	for i := 0; i < datapoints.Len(); i++ {
		dpAttrs := datapoints.At(i).Attributes()

		if _, exists := dpAttrs.Get(slurmJobIDKey); exists {
			continue
		}

		for _, dt := range deviceTypes {
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
			jobInfo := lookup.GetJobInfo(deviceID, dt.Name)
			if jobInfo == nil {
				continue
			}

			dpAttrs.PutStr(slurmJobIDKey, jobInfo.JobID)
			if jobInfo.JobName != "" {
				dpAttrs.PutStr(slurmJobNameKey, jobInfo.JobName)
			}
			if jobInfo.User != "" {
				dpAttrs.PutStr(slurmJobUserKey, jobInfo.User)
			}
			if jobInfo.Account != "" {
				dpAttrs.PutStr(slurmJobAccountKey, jobInfo.Account)
			}
			if jobInfo.Partition != "" {
				dpAttrs.PutStr(slurmJobPartitionKey, jobInfo.Partition)
			}

			logger.Debug("Correlated device to Slurm job",
				zap.String("device_type", dt.Name),
				zap.String("device_id", deviceID),
				zap.String("job_id", jobInfo.JobID),
				zap.String("job_name", jobInfo.JobName),
			)
			break
		}
	}
}
