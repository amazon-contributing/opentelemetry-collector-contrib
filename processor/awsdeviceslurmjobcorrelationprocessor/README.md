# AWS Device Slurm Job Correlation Processor

| Status | |
|--------|---|
| Stability | alpha |
| Distributions | [amazon-cloudwatch-agent] |
| Issues | |
| Code Owners | |

## Overview

The `awsdevicejobcorrelation` processor enriches GPU (DCGM) and EFA device
metrics with Slurm job metadata. It answers: **"Which job owns this GPU/EFA
device right now?"**

It correlates device IDs to running Slurm jobs using two complementary
discovery layers:

1. **Cgroup inspection** (authoritative, local, fast) — walks the Slurm cgroup
   tree to find which GPU device indices are assigned to which job.
2. **slurmrestd metadata hydration** (background poll) — enriches job IDs with
   human-readable metadata: job name, user, account, partition.

## Output Attributes

The processor stamps each matching datapoint with:

| Attribute | Description | Example |
|-----------|-------------|---------|
| `slurm.job.id` | Slurm job ID | `590` |
| `slurm.job.name` | Job name | `train-llama-70b` |
| `slurm.job.user` | Submitting user | `miconeil` |
| `slurm.job.account` | Slurm account | `ml-team` |
| `slurm.job.partition` | Slurm partition | `gpu-queue` |

## Configuration

```yaml
processors:
  awsdevicejobcorrelation:
    # Path to cgroup filesystem root
    cgroup_root: /sys/fs/cgroup
    # Cgroup version: "v1" or "v2"
    cgroup_version: v1
    # slurmrestd endpoint for metadata hydration (optional)
    slurmrestd_endpoint: http://slurmctl:6820
    # How often to refresh job metadata from slurmrestd
    metadata_poll_interval: 30s
    # Whether nodes run a single job at a time (simplifies EFA attribution)
    node_exclusive: true
    # Prefix for host filesystem access from containers
    host_path: ""
    # Device types to correlate
    device_types:
      - name: gpu
        device_id_attribute: gpu
        device_id_source: datapoint
      - name: efa
        device_id_attribute: aws.efa.device
        device_id_source: resource
```

## How It Works

### GPU Correlation

1. The processor walks `/sys/fs/cgroup/devices/slurm/uid_*/job_*/devices.list`
   (cgroup v1) or `/sys/fs/cgroup/system.slice/slurmstepd.scope/job_*/` (v2).
2. NVIDIA GPU devices have major number 195; minor numbers map to GPU indices.
3. When a DCGM metric arrives with `gpu="0"`, the processor looks up which job
   owns `/dev/nvidia0` via the cgroup mapping.

### EFA Correlation

EFA counters are device-level (no per-process breakdown). For exclusive-node
jobs (standard for large GPU training), all EFA counters on a node belong to
the single running job. When `node_exclusive: true` and exactly one job is
detected, all EFA metrics are attributed to that job.

### slurmrestd Hydration

Once a job ID is known, the processor calls `GET /slurm/v0.0.41/job/{id}` to
fetch the job name, user, account, and partition. Results are cached and
refreshed at `metadata_poll_interval`.

## Example Pipeline

```yaml
receivers:
  prometheus/dcgm:
    config:
      scrape_configs:
        - job_name: dcgm
          static_configs:
            - targets: ['localhost:9400']
  awsefareceiver:
    collection_interval: 10s

processors:
  awsdevicejobcorrelation:
    cgroup_root: /sys/fs/cgroup
    slurmrestd_endpoint: http://slurmctl:6820
    node_exclusive: true
    device_types:
      - name: gpu
        device_id_attribute: gpu
        device_id_source: datapoint
      - name: efa
        device_id_attribute: aws.efa.device
        device_id_source: resource

exporters:
  otlphttp:
    endpoint: "https://monitoring.us-east-1.amazonaws.com:443"
    auth:
      authenticator: sigv4auth

service:
  pipelines:
    metrics:
      receivers: [prometheus/dcgm, awsefareceiver]
      processors: [awsdevicejobcorrelation]
      exporters: [otlphttp]
```

## Compatibility

| Platform | Slurm Version | cgroup | slurmrestd |
|----------|--------------|--------|------------|
| ParallelCluster 3.11+ | 23.11+ | v1 | Yes |
| ParallelCluster 3.15+ | 25.11+ | v1/v2 | Yes |
| SageMaker HyperPod | 23.11-24.11 | v1 | Likely |
