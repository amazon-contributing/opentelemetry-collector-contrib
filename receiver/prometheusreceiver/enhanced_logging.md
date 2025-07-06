# Enhanced Logging for OpenTelemetry Collector Prometheus Receiver

This document provides code changes to add enhanced logging to the OpenTelemetry Collector's Prometheus receiver to help debug the 5-minute delay issue.

## Changes to transaction.go

Add the following enhanced logging to the `Append` method in `internal/transaction.go`:

```go
func (t *transaction) Append(_ storage.SeriesRef, ls labels.Labels, atMs int64, val float64) (storage.SeriesRef, error) {
	// Add debug logging for timestamp tracking
	currentTimeMs := time.Now().UnixMilli()
	timeDiffMs := currentTimeMs - atMs
	
	// Only log a sample of metrics to avoid excessive logging
	if rand.Float64() < 0.01 { // Log approximately 1% of metrics
		metricName := ls.Get(model.MetricNameLabel)
		jobName := ls.Get(model.JobLabel)
		instanceName := ls.Get(model.InstanceLabel)
		t.logger.Debug("Received metric in Append",
			zap.String("metric_name", metricName),
			zap.String("job", jobName),
			zap.String("instance", instanceName),
			zap.Int64("timestamp_ms", atMs),
			zap.Int64("current_time_ms", currentTimeMs),
			zap.Int64("time_diff_ms", timeDiffMs),
			zap.Float64("time_diff_minutes", float64(timeDiffMs)/60000.0),
			zap.Float64("value", val))
	}

	select {
	case <-t.ctx.Done():
		return 0, errTransactionAborted
	default:
	}

	// Rest of the method...
}
```

Add the following enhanced logging to the `Commit` method in `internal/transaction.go`:

```go
func (t *transaction) Commit() error {
	if t.isNew {
		return nil
	}

	startTime := time.Now()
	t.logger.Debug("Starting Commit operation")

	ctx := t.obsrecv.StartMetricsOp(t.ctx)
	md, err := t.getMetrics()
	if err != nil {
		t.obsrecv.EndMetricsOp(ctx, dataformat, 0, err)
		t.logger.Debug("Failed to get metrics in Commit", zap.Error(err), zap.Duration("elapsed_time", time.Since(startTime)))
		return err
	}

	numPoints := md.DataPointCount()
	if numPoints == 0 {
		t.logger.Debug("No data points to commit", zap.Duration("elapsed_time", time.Since(startTime)))
		return nil
	}

	t.logger.Debug("Got metrics in Commit", 
		zap.Int("num_points", numPoints), 
		zap.Duration("elapsed_time", time.Since(startTime)))

	// Log timestamp information for a sample of metrics
	if md.ResourceMetrics().Len() > 0 {
		rm := md.ResourceMetrics().At(0)
		if rm.ScopeMetrics().Len() > 0 {
			sm := rm.ScopeMetrics().At(0)
			if sm.Metrics().Len() > 0 {
				metric := sm.Metrics().At(0)
				
				// Get the timestamp of the first datapoint
				var timestamp pcommon.Timestamp
				switch metric.Type() {
				case pmetric.MetricTypeGauge:
					if metric.Gauge().DataPoints().Len() > 0 {
						timestamp = metric.Gauge().DataPoints().At(0).Timestamp()
					}
				case pmetric.MetricTypeSum:
					if metric.Sum().DataPoints().Len() > 0 {
						timestamp = metric.Sum().DataPoints().At(0).Timestamp()
					}
				case pmetric.MetricTypeHistogram:
					if metric.Histogram().DataPoints().Len() > 0 {
						timestamp = metric.Histogram().DataPoints().At(0).Timestamp()
					}
				case pmetric.MetricTypeExponentialHistogram:
					if metric.ExponentialHistogram().DataPoints().Len() > 0 {
						timestamp = metric.ExponentialHistogram().DataPoints().At(0).Timestamp()
					}
				case pmetric.MetricTypeSummary:
					if metric.Summary().DataPoints().Len() > 0 {
						timestamp = metric.Summary().DataPoints().At(0).Timestamp()
					}
				}
				
				if timestamp != 0 {
					currentTimeMs := time.Now().UnixMilli()
					metricTimeMs := timestamp.AsTime().UnixMilli()
					timeDiffMs := currentTimeMs - metricTimeMs
					
					t.logger.Debug("Sample metric timestamp info",
						zap.String("metric_name", metric.Name()),
						zap.Int64("metric_timestamp_ms", metricTimeMs),
						zap.Int64("current_time_ms", currentTimeMs),
						zap.Int64("time_diff_ms", timeDiffMs),
						zap.Float64("time_diff_minutes", float64(timeDiffMs)/60000.0))
				}
			}
		}
	}

	adjustStartTime := time.Now()
	if !removeStartTimeAdjustment.IsEnabled() {
		if err = t.metricAdjuster.AdjustMetrics(md); err != nil {
			t.obsrecv.EndMetricsOp(ctx, dataformat, numPoints, err)
			t.logger.Debug("Failed to adjust metrics", zap.Error(err), zap.Duration("elapsed_time", time.Since(startTime)))
			return err
		}
	}
	t.logger.Debug("Adjusted metrics", zap.Duration("adjust_time", time.Since(adjustStartTime)))

	consumeStartTime := time.Now()
	err = t.sink.ConsumeMetrics(ctx, md)
	consumeTime := time.Since(consumeStartTime)
	totalTime := time.Since(startTime)
	
	t.logger.Debug("Completed Commit operation",
		zap.Int("num_points", numPoints),
		zap.Duration("consume_time", consumeTime),
		zap.Duration("total_time", totalTime),
		zap.Error(err))
		
	t.obsrecv.EndMetricsOp(ctx, dataformat, numPoints, err)
	return err
}
```

## Changes to metrics_receiver.go

Add the following enhanced logging to the `Start` method in `metrics_receiver.go`:

```go
func (r *pReceiver) Start(ctx context.Context, host component.Host) error {
	discoveryCtx, cancel := context.WithCancel(context.Background())
	r.cancelFunc = cancel

	logger := slog.New(zapslog.NewHandler(r.settings.Logger.Core()))

	// Log Prometheus configuration details
	r.settings.Logger.Info("Starting Prometheus receiver with configuration",
		zap.Int("num_scrape_configs", len(r.cfg.PrometheusConfig.ScrapeConfigs)))
	
	// Log details about each scrape config, especially honor_timestamps
	for _, sc := range r.cfg.PrometheusConfig.ScrapeConfigs {
		r.settings.Logger.Info("Scrape config details",
			zap.String("job_name", sc.JobName),
			zap.Bool("honor_timestamps", sc.HonorTimestamps),
			zap.String("scrape_interval", sc.ScrapeInterval.String()),
			zap.String("scrape_timeout", sc.ScrapeTimeout.String()),
			zap.Int("target_count", len(sc.ServiceDiscoveryConfigs)))
	}

	// Rest of the method...
}
```

Add the following enhanced logging to the `initPrometheusComponents` method in `metrics_receiver.go`:

```go
func (r *pReceiver) initPrometheusComponents(ctx context.Context, logger *slog.Logger, host component.Host) error {
	startTime := time.Now()
	r.settings.Logger.Info("Initializing Prometheus components", zap.String("start_time", startTime.String()))
	
	// Rest of the method...
	
	// Log scrape options
	r.settings.Logger.Info("Scrape options",
		zap.Bool("pass_metadata_in_context", opts.PassMetadataInContext),
		zap.Bool("extra_metrics", opts.ExtraMetrics),
		zap.Bool("enable_created_timestamp_zero_ingestion", opts.EnableCreatedTimestampZeroIngestion),
		zap.Bool("enable_native_histograms_ingestion", opts.EnableNativeHistogramsIngestion))

	// Rest of the method...
	
	r.settings.Logger.Info("Prometheus components initialized", 
		zap.Duration("initialization_time", time.Since(startTime)))
	return nil
}
```

## Changes to appendable.go

Add the following enhanced logging to the `Appender` method in `internal/appendable.go`:

```go
func (o *appendable) Appender(ctx context.Context) storage.Appender {
	o.settings.Logger.Debug("Creating new transaction for appending metrics")
	return newTransaction(ctx, o.metricAdjuster, o.sink, o.externalLabels, o.settings, o.obsrecv, o.trimSuffixes, o.enableNativeHistograms)
}
```

These enhanced logging changes will provide detailed information about:

1. Timestamp differences throughout the processing pipeline
2. Processing times for each step
3. Configuration details including honor_timestamps settings
4. Metrics processing statistics

This information will help diagnose the 5-minute delay issue by showing exactly where in the pipeline the delay is occurring.
