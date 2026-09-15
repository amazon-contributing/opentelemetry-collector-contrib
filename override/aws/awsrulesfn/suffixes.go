// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Not generated; augments the generated partition data with derived helpers.
package awsrulesfn // import "github.com/amazon-contributing/opentelemetry-collector-contrib/override/aws/awsrulesfn"

// GetPartitionDNSSuffixes returns the unique set of DNS suffixes across all
// AWS partitions (both standard and dual-stack), e.g. "amazonaws.com",
// "amazonaws.com.cn", "api.aws".
func GetPartitionDNSSuffixes() []string {
	seen := make(map[string]struct{})
	var suffixes []string
	add := func(s string) {
		if s == "" {
			return
		}
		if _, ok := seen[s]; ok {
			return
		}
		seen[s] = struct{}{}
		suffixes = append(suffixes, s)
	}
	for _, p := range partitions {
		add(p.DefaultConfig.DnsSuffix)
		add(p.DefaultConfig.DualStackDnsSuffix)
	}
	return suffixes
}
