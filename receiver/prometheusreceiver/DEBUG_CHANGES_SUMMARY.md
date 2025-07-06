# Debug Build Changes Summary

This document summarizes the changes made to create a debug build of the OpenTelemetry Collector's Prometheus receiver specifically for investigating the 5-minute delay issue with Prometheus metrics from Confluent Cloud.

## Problem Description

According to the support ticket, there is a consistent 5-minute delay between when metrics and logs are generated in Confluent Cloud and when they appear in CloudWatch. Additionally, metrics continue to be pushed for 5 minutes after stopping the agent.

## Changes Made

### 1. Enhanced Logging in `internal/transaction.go`

#### Append Method
- Added timestamp tracking to calculate and log the time difference between when metrics are scraped and when they're processed
- Added sampling to avoid excessive logging (only logs approximately 1% of metrics)
- Added job and instance information to help identify the source of metrics

Example log output:
```
DEBUG Received metric in Append metric_name=up job=confluent_cloud instance=10.0.0.1:9090 timestamp_ms=1625400000000 current_time_ms=1625400300000 time_diff_ms=300000 time_diff_minutes=5.0 value=1
```

#### Commit Method
- Added detailed timing information for the entire commit operation
- Added timestamp difference calculations for a sample metric
- Added performance metrics for each step in the commit process

Example log output:
```
DEBUG Starting Commit operation
DEBUG Got metrics in Commit num_points=100 elapsed_time=15ms
DEBUG Sample metric timestamp info metric_name=up metric_timestamp_ms=1625400000000 current_time_ms=1625400300000 time_diff_ms=300000 time_diff_minutes=5.0
DEBUG Adjusted metrics adjust_time=5ms
DEBUG Completed Commit operation num_points=100 consume_time=10ms total_time=30ms error=<nil>
```

### 2. Configuration Logging in `metrics_receiver.go`

#### Start Method
- Added logging for each scrape configuration
- Added detailed logging for honor_timestamps, scrape intervals, and timeouts

Example log output:
```
INFO Starting Prometheus receiver with configuration num_scrape_configs=1
INFO Scrape config details job_name=confluent_cloud honor_timestamps=true scrape_interval=60s scrape_timeout=10s target_count=1
```

#### initPrometheusComponents Method
- Added timing information for component initialization
- Added detailed logging for scrape options

Example log output:
```
INFO Initializing Prometheus components start_time=2025-07-04 16:30:00.000
INFO Scrape options pass_metadata_in_context=true extra_metrics=false enable_created_timestamp_zero_ingestion=true enable_native_histograms_ingestion=false
INFO Prometheus components initialized initialization_time=250ms
```

## Potential Causes to Investigate

1. **honor_timestamps Configuration**: If set to true, the receiver will use timestamps from Prometheus metrics which could be delayed if Confluent Cloud is providing old timestamps.

2. **Scrape Interval vs Processing Time**: If the scrape interval is shorter than the time it takes to process metrics, a backlog could build up.

3. **Timestamp Handling**: The receiver might be using timestamps from when metrics are scraped rather than when they're generated.

4. **Network Latency**: There might be significant network latency between Confluent Cloud and the collector.

## How to Analyze the Debug Logs

1. Look for consistent time differences in the `Append` method logs
2. Check if the `honor_timestamps` setting is true in the configuration logs
3. Monitor the processing times in the `Commit` method logs to identify bottlenecks
4. Compare the timestamps of metrics with the current time to see if there's a consistent delay

## Next Steps

After deploying this debug build:

1. Collect logs during normal operation
2. Collect logs when stopping the collector to observe the 5-minute continuation behavior
3. Analyze the logs to identify patterns in the timing differences
4. Consider modifying the `honor_timestamps` setting to false if it's currently true
