---
inclusion: always
---
# AWS Critical Components

This file tracks OpenTelemetry components that are **actively used by the Amazon CloudWatch Agent**. These components require extra scrutiny during OTel bump merge conflict resolution.

> **Note**: This list should be kept in sync with the CloudWatch Agent's dependencies. 
> Reference: https://github.com/aws/amazon-cloudwatch-agent/blob/main/go.mod

## High Priority Components

These components are **actively used** by CloudWatch Agent (verified from go.mod). They require the most careful analysis during OTel bump updates.

### Exporters

- **awsemfexporter** (`exporter/awsemfexporter/`)
  - Exports metrics in EMF (Embedded Metric Format) to CloudWatch
  - Critical for CloudWatch metrics ingestion
  
- **awscloudwatchlogsexporter** (`exporter/awscloudwatchlogsexporter/`)
  - Exports logs to CloudWatch Logs
  - Core logging functionality

- **awsxrayexporter** (`exporter/awsxrayexporter/`)
  - Exports traces to AWS X-Ray
  - Critical for distributed tracing

### Receivers

- **awscontainerinsightreceiver** (`receiver/awscontainerinsightreceiver/`)
  - Collects container and pod metrics from ECS and EKS
  - Core container monitoring functionality

- **awscontainerinsightskueuereceiver** (`receiver/awscontainerinsightskueuereceiver/`)
  - Collects Kueue metrics for Kubernetes job scheduling
  - EKS workload management

- **awsxrayreceiver** (`receiver/awsxrayreceiver/`)
  - Receives X-Ray trace data
  - Distributed tracing ingestion

- **jmxreceiver** (`receiver/jmxreceiver/`)
  - Collects JMX metrics from Java applications
  - Java application monitoring

- **prometheusreceiver** (`receiver/prometheusreceiver/`)
  - Scrapes Prometheus metrics endpoints
  - Prometheus-compatible metric collection

### Processors

- **cumulativetodeltaprocessor** (`processor/cumulativetodeltaprocessor/`)
  - Converts cumulative metrics to delta metrics
  - Metric transformation for CloudWatch

- **resourcedetectionprocessor** (`processor/resourcedetectionprocessor/`)
  - Detects resource attributes (EC2, ECS, EKS metadata)
  - Automatic resource tagging and context enrichment

### Extensions

- **awsmiddleware** (`extension/awsmiddleware/`)
  - AWS SDK middleware integration
  - Credential management and request signing

- **awsproxy** (`extension/awsproxy/`)
  - AWS proxy configuration
  - Network routing for AWS services

### Override Packages

- **override/aws** (`override/aws/`)
  - AWS credential handling
  - IMDS retry logic
  - Core AWS integration utilities

## Internal Packages

These internal packages are used by the components above and require careful attention during updates.

### AWS Internal Packages

- **internal/aws/awsutil** (`internal/aws/awsutil/`)
  - AWS utility functions

- **internal/aws/containerinsight** (`internal/aws/containerinsight/`)
  - Container Insights data processing

- **internal/aws/cwlogs** (`internal/aws/cwlogs/`)
  - CloudWatch Logs utilities

- **internal/aws/k8s** (`internal/aws/k8s/`)
  - Kubernetes integration utilities

- **internal/aws/metrics** (`internal/aws/metrics/`)
  - AWS metrics utilities

- **internal/aws/proxy** (`internal/aws/proxy/`)
  - AWS proxy utilities

- **internal/aws/xray** (`internal/aws/xray/`)
  - X-Ray utilities

### Other Internal Packages

- **internal/coreinternal** (`internal/coreinternal/`)
  - Core internal utilities

- **internal/k8sconfig** (`internal/k8sconfig/`)
  - Kubernetes configuration

- **internal/kubelet** (`internal/kubelet/`)
  - Kubelet client and utilities

- **internal/metadataproviders** (`internal/metadataproviders/`)
  - Cloud metadata providers (EC2, ECS, etc.)

## Shared Packages

- **pkg/resourcetotelemetry** (`pkg/resourcetotelemetry/`)
  - Resource attribute to telemetry conversion
  - Note: Has AWS-specific customization for "clear resource attributes after copy" functionality
  - Reference: https://github.com/amazon-contributing/opentelemetry-collector-contrib/pull/148

- **pkg/stanza** (`pkg/stanza/`)
  - Log parsing and processing library

- **pkg/translator/prometheus** (`pkg/translator/prometheus/`)
  - Prometheus metric translation utilities

## Verification Process

To verify if a component is used by CloudWatch Agent:

1. Check CloudWatch Agent's go.mod:
   ```bash
   curl -s https://raw.githubusercontent.com/aws/amazon-cloudwatch-agent/main/go.mod | grep "amazon-contributing/opentelemetry-collector-contrib"
   ```

2. Search for component imports in CloudWatch Agent codebase

3. Check component's go.mod for local overrides that indicate AWS customizations

## Update Guidelines

When resolving merge conflicts for components in this list:

1. **Always review git history** to understand AWS customizations
2. **Research upstream changes** via GitHub PRs and issues
3. **Test thoroughly** after resolution
4. **Document decisions** in commit messages
5. **Consider upstreaming** valuable AWS improvements

## Maintenance

This file should be updated:
- After each OTel bump
- When CloudWatch Agent adds/removes component dependencies
- When new AWS-specific components are added to the fork

**Last Updated**: January 27, 2026 (verified against CloudWatch Agent main branch)
