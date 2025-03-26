// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package nvme // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/gpu"

const (
	// Original Metric Names
	ebsReadOpsTotal        = "aws_ebs_csi_read_ops_total"
	ebsWriteOpsTotal       = "aws_ebs_csi_write_ops_total"
	ebsReadBytesTotal      = "aws_ebs_csi_read_bytes_total"
	ebsWriteBytesTotal     = "aws_ebs_csi_write_bytes_total"
	ebsReadTime            = "aws_ebs_csi_read_seconds_total"
	ebsWriteTime           = "aws_ebs_csi_write_seconds_total"
	ebsExceededIOPSTime    = "aws_ebs_csi_exceeded_iops_seconds_total"
	ebsExceededTPTime      = "aws_ebs_csi_exceeded_tp_seconds_total"
	ebsExceededEC2IOPSTime = "aws_ebs_csi_ec2_exceeded_iops_seconds_total"
	ebsExceededEC2TPTime   = "aws_ebs_csi_ec2_exceeded_tp_seconds_total"
	ebsVolumeQueueLength   = "aws_ebs_csi_volume_queue_length"

	// Converted Names
	nodeReadOpsTotal        = "node_diskio_ebs_total_read_ops"
	nodeWriteOpsTotal       = "node_diskio_ebs_total_write_ops"
	nodeReadBytesTotal      = "node_diskio_ebs_total_read_bytes"
	nodeWriteBytesTotal     = "node_diskio_ebs_total_write_bytes"
	nodeReadTime            = "node_diskio_ebs_total_read_time"
	nodeWriteTime           = "node_diskio_ebs_total_write_time"
	nodeExceededIOPSTime    = "node_diskio_ebs_volume_performance_exceeded_iops"
	nodeExceededTPTime      = "node_diskio_ebs_volume_performance_exceeded_tp"
	nodeExceededEC2IOPSTime = "node_diskio_ebs_ec2_instance_performance_exceeded_iops"
	nodeExceededEC2TPTime   = "node_diskio_ebs_ec2_instance_performance_exceeded_tp"
	nodeVolumeQueueLength   = "node_diskio_ebs_volume_queue_length"
)

var MetricToUnit = map[string]string{
	nodeReadOpsTotal:        "Count",
	nodeWriteOpsTotal:       "Count",
	nodeReadBytesTotal:      "Bytes",
	nodeWriteBytesTotal:     "Bytes",
	nodeReadTime:            "Seconds",
	nodeWriteTime:           "Seconds",
	nodeExceededIOPSTime:    "Seconds",
	nodeExceededTPTime:      "Seconds",
	nodeExceededEC2IOPSTime: "Seconds",
	nodeExceededEC2TPTime:   "Seconds",
	nodeVolumeQueueLength:   "Count",
}
