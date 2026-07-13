// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsv2 // import "github.com/amazon-contributing/opentelemetry-collector-contrib/override/awsv2"

import (
	"github.com/amazon-contributing/opentelemetry-collector-contrib/override/awsv2/awsrulesfn"
)

// partitionPrimaryRegionOverrides is consulted before falling back to the SDK's
// PartitionConfig.ImplicitGlobalRegion. Add an entry only when the implicit-global
// for a partition diverges from the STS endpoint we want to use as fallback.
var partitionPrimaryRegionOverrides = map[string]string{
	"aws-cn": "cn-north-1",
}

// GetPartition returns the AWS partition ID for the given region (e.g.
// "aws", "aws-cn"). Returns "" if the region cannot be resolved.
func GetPartition(region string) string {
	p := awsrulesfn.GetPartition(region)
	if p == nil {
		return ""
	}
	return p.Name
}

// GetPartitionPrimaryRegion returns the primary region of the partition that contains region,
// used as a partitional STS fallback when the regional STS endpoint is disabled. Returns "" if
// the partition cannot be resolved.
func GetPartitionPrimaryRegion(region string) string {
	p := awsrulesfn.GetPartition(region)
	if p == nil {
		return ""
	}
	if v, ok := partitionPrimaryRegionOverrides[p.Name]; ok {
		return v
	}
	return p.ImplicitGlobalRegion
}
