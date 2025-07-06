# Prometheus Receiver Debug Build

This debug build of the OpenTelemetry Collector's Prometheus receiver includes enhanced logging specifically for diagnosing the 5-minute delay issue with metrics ingestion from Confluent Cloud.

## What's Been Modified

The following files have been modified to include detailed debug logging:

1. `internal/transaction.go`
   - Added timestamp tracking in the `Append` method
   - Added detailed logging in the `Commit` method
   - Added time difference calculations between metric timestamps and processing time

2. `metrics_receiver.go`
   - Added logging for `honor_timestamps` configuration
   - Added logging for scrape intervals and timeouts
   - Added timing information for component initialization

## How to Use This Debug Build

1. Build the OpenTelemetry Collector with the debug modifications:
   ```
   cd /Users/tjstark/workplace/opentelemetry-collector-contrib
   make otelcol
   ```

2. Configure the collector to use the Prometheus receiver with appropriate logging level:
   ```yaml
   receivers:
     prometheus:
       config:
         scrape_configs:
           - job_name: 'confluent_cloud'
             honor_timestamps: true  # This setting is important for the investigation
             scrape_interval: 60s
             static_configs:
               - targets: ['your-confluent-cloud-endpoint:9090']

   exporters:
     logging:
       verbosity: detailed

   service:
     pipelines:
       metrics:
         receivers: [prometheus]
         exporters: [logging]

   logging:
     level: debug  # Set to debug to see the detailed logs
   ```

3. Run the collector with the debug configuration:
   ```
   ./otelcol --config=config.yaml
   ```

4. Monitor the logs for detailed timing information.

## What to Look For

The debug logs will provide detailed information about:

1. Timestamp differences between when metrics are scraped and when they're processed
2. Processing times for each step in the pipeline
3. Configuration details like `honor_timestamps` settings
4. Scrape intervals and timeouts

Pay special attention to:

- Logs with timestamp differences showing approximately 5 minutes
- The `honor_timestamps` configuration value
- Any patterns in the timing differences

## Potential Issues to Investigate

1. `honor_timestamps` configuration - If set to true, the receiver will use timestamps from Prometheus metrics which could be delayed
2. Scrape interval and timeout settings - These might affect when metrics are collected
3. Processing bottlenecks - Look for steps in the pipeline that take longer than expected
4. Network latency between Confluent Cloud and the collector

## Reporting Results

After collecting logs with this debug build, please provide:

1. The complete collector logs showing the timestamp differences
2. The Prometheus receiver configuration being used
3. Information about the environment (EC2 instance type, region, etc.)
4. Any patterns observed in the timing differences
