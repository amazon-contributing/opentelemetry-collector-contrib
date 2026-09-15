// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package aws

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetPartition(t *testing.T) {
	tests := map[string]string{
		"us-east-1":       "aws",
		"eu-west-2":       "aws",
		"ap-east-1":       "aws", // opt-in region in commercial partition
		"cn-north-1":      "aws-cn",
		"cn-northwest-1":  "aws-cn",
		"us-gov-west-1":   "aws-us-gov",
		"us-gov-east-1":   "aws-us-gov",
		"us-iso-east-1":   "aws-iso",
		"us-isob-east-1":  "aws-iso-b",
		"eu-isoe-west-1":  "aws-iso-e",
		"us-isof-south-1": "aws-iso-f",
		"eusc-de-east-1":  "aws-eusc",
		"":                "aws", // empty resolves to default partition
		"not-a-region":    "aws", // unknown patterns fall through to default
	}
	for region, want := range tests {
		t.Run(region, func(t *testing.T) {
			assert.Equal(t, want, GetPartition(region))
		})
	}
}

func TestGetPartitionPrimaryRegion(t *testing.T) {
	tests := map[string]string{
		"us-east-1":       "us-east-1",
		"eu-west-2":       "us-east-1",
		"ap-east-1":       "us-east-1",
		"cn-north-1":      "cn-north-1",
		"cn-northwest-1":  "cn-north-1",
		"us-gov-east-1":   "us-gov-west-1",
		"us-gov-west-1":   "us-gov-west-1",
		"us-iso-east-1":   "us-iso-east-1",
		"us-isob-east-1":  "us-isob-east-1",
		"eu-isoe-west-1":  "eu-isoe-west-1",
		"us-isof-south-1": "us-isof-south-1",
		"eusc-de-east-1":  "eusc-de-east-1",
		// Unknown patterns resolve to "aws" partition, mapping to its primary.
		"":             "us-east-1",
		"not-a-region": "us-east-1",
	}
	for region, want := range tests {
		t.Run(region, func(t *testing.T) {
			assert.Equal(t, want, GetPartitionPrimaryRegion(region))
		})
	}
}

func TestGetPartitionDNSSuffix(t *testing.T) {
	tests := map[string]string{
		"us-east-1":       "amazonaws.com",
		"eu-west-2":       "amazonaws.com",
		"cn-north-1":      "amazonaws.com.cn",
		"us-gov-west-1":   "amazonaws.com",
		"us-iso-east-1":   "c2s.ic.gov",
		"us-isob-east-1":  "sc2s.sgov.gov",
		"eu-isoe-west-1":  "cloud.adc-e.uk",
		"us-isof-south-1": "csp.hci.ic.gov",
		"eusc-de-east-1":  "amazonaws.eu",
		// Unknown patterns resolve to the default "aws" partition's suffix.
		"":             "amazonaws.com",
		"not-a-region": "amazonaws.com",
	}
	for region, want := range tests {
		t.Run(region, func(t *testing.T) {
			assert.Equal(t, want, GetPartitionDNSSuffix(region))
		})
	}

	// Every per-region suffix must be a member of the full suffix set, pinning
	// both exports to the same underlying partition data.
	suffixes := GetPartitionDNSSuffixes()
	for region := range tests {
		assert.Contains(t, suffixes, GetPartitionDNSSuffix(region), "region %q", region)
	}
}
