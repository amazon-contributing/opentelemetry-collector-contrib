# AWS EFA Receiver

| Status        |           |
|---------------|-----------|
| Stability     | [beta]: metrics |
| Distributions | [] |

[beta]: https://github.com/open-telemetry/opentelemetry-collector/blob/main/docs/component-stability.md#beta

The AWS EFA receiver collects metrics from Amazon Elastic Fabric Adapter (EFA) devices
by reading hardware counters from `/sys/class/infiniband/*/ports/*/hw_counters/`.

EFA is a network device that can be attached to Amazon EC2 instances to accelerate
AI/ML, HPC, and other distributed workloads. This receiver exposes EFA driver metrics
including RDMA operations, network traffic, retransmissions, and connection health.

## Configuration

```yaml
receivers:
  awsefareceiver:
    collection_interval: 60s
    # host_path is the root path to the host filesystem when running in a container.
    # Set to "/host" or "/rootfs" when the host filesystem is mounted into the container.
    host_path: ""
```

## Metrics

All metrics are cumulative monotonic sums representing EFA driver hardware counters:

| Metric Name | Description | Unit |
|---|---|---|
| `efa_rdma_read_bytes` | Bytes received using RDMA read operations | By |
| `efa_rdma_write_bytes` | Bytes written by other instances using RDMA write operations | By |
| `efa_rdma_write_recv_bytes` | Bytes received by RDMA write operations | By |
| `efa_rx_bytes` | Bytes received | By |
| `efa_rx_dropped` | Packets received and then dropped | 1 |
| `efa_tx_bytes` | Bytes transmitted | By |
| `efa_retrans_bytes` | EFA SRD bytes retransmitted | By |
| `efa_retrans_pkts` | EFA SRD packets retransmitted | 1 |
| `efa_retrans_timeout_events` | Times EFA SRD traffic timed out causing network path change | 1 |
| `efa_unresponsive_remote_events` | Times an EFA SRD remote connection was unresponsive | 1 |
| `efa_impaired_remote_conn_events` | Times EFA SRD connections entered impaired state | 1 |

## Resource Attributes

| Attribute | Description |
|---|---|
| `device` | The EFA device name (e.g. `rdmap0s31`) |
| `port` | The EFA port number |
| `pod` | The Kubernetes pod name assigned to this device (empty string if unassigned) |
| `namespace` | The Kubernetes namespace of the pod assigned to this device (empty string if unassigned) |
| `container` | The container name within the pod assigned to this device (empty string if unassigned) |
