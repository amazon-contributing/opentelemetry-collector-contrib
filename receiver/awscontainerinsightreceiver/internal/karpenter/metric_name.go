// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package karpenter

const (
	KarpenterPodsStartupTime                  = "karpenter_pods_startup_time_seconds"
	KarpenterDeprovisioningReplacementMachine = "karpenter_deprovisioning_replacement_machine_initialized_seconds"
	KarpenterDisruptionReplacementNodeclaim   = "karpenter_disruption_replacement_nodeclaim_initialized_seconds"
	KarpenterCloudproviderDuration            = "karpenter_cloudprovider_duration_seconds"
	KarpenterNodepoolLimit                    = "karpenter_nodepool_limit"
	KarpenterNodepoolUsage                    = "karpenter_nodepool_usage"
	KarpenterProvisionerLimit                 = "karpenter_provisioner_limit"
	KarpenterProvisionerUsage                 = "karpenter_provisioner_usage"
)
