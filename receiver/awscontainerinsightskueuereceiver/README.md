# AWS Kueue Receiver

The AWS Kueue receiver collects [Kueue](https://kueue.sigs.k8s.io/) metrics from
a Kubernetes cluster. It runs an embedded Prometheus scraper that discovers the
Kueue controller-manager endpoint, scrapes its `/metrics` endpoint, keeps an
allow-listed subset of Kueue metrics, relabels their dimensions, and forwards
them to the next consumer in the pipeline.

Because the metrics originate from Kueue's own Prometheus endpoint and are passed
through unchanged in value, this receiver does not define a metric schema of its
own. The metrics and dimensions below reflect what the receiver forwards.

## Configuration

| Field                 | Default | Description                                                                                              |
| --------------------- | ------- | -------------------------------------------------------------------------------------------------------- |
| `collection_interval` | `60s`   | Interval at which metrics are collected and forwarded.                                                   |
| `cluster_name`        | `""`    | Cluster name emitted as the `ClusterName` dimension. Auto-detected from EC2 tags when not set.           |

Example:

```yaml
receivers:
  awscontainerinsightskueuereceiver:
    collection_interval: 60s
    cluster_name: my-cluster
```

## Forwarded metrics

The receiver keeps only the following Kueue metrics; all others scraped from the
endpoint are dropped:

| Metric                            | Description                                                              |
| --------------------------------- | ------------------------------------------------------------------------ |
| `kueue_pending_workloads`         | Number of pending workloads, per cluster queue and status.               |
| `kueue_evicted_workloads_total`   | Number of evicted workloads, per cluster queue and eviction reason.      |
| `kueue_admitted_active_workloads` | Number of admitted workloads that are active, per cluster queue.         |
| `kueue_cluster_queue_resource_usage` | Total resources currently in use by a cluster queue, per flavor and resource. |
| `kueue_cluster_queue_nominal_quota`  | Nominal resource quota of a cluster queue, per flavor and resource.        |

Metric names, types, and values are preserved as emitted by Kueue. Refer to the
[Kueue metrics reference](https://kueue.sigs.k8s.io/docs/reference/metrics/) for
their authoritative definitions.

## Dimensions

The receiver renames the following Kueue labels to the dimension names below and
adds a `ClusterName` dimension:

| Source label    | Forwarded dimension |
| --------------- | ------------------- |
| `cluster_queue` | `ClusterQueue`      |
| `flavor`        | `Flavor`            |
| `reason`        | `Reason`            |
| `resource`      | `Resource`          |
| `status`        | `Status`            |
| (configured/auto-detected) | `ClusterName` |

The Prometheus `type` label is remapped to `kubernetes_type` and the original
`type` label is dropped.
