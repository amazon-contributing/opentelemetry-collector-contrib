// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsrulesfn

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetPartitionDNSSuffixes(t *testing.T) {
	suffixes := GetPartitionDNSSuffixes()

	// Suffixes the generated data is known to carry; failures here mean the
	// partition data changed shape — re-check consumers, don't just update.
	for _, want := range []string{"amazonaws.com", "amazonaws.com.cn", "api.aws"} {
		assert.Contains(t, suffixes, want)
	}

	seen := make(map[string]struct{}, len(suffixes))
	for _, s := range suffixes {
		assert.NotEmpty(t, s)
		_, dup := seen[s]
		assert.False(t, dup, "duplicate suffix %q", s)
		seen[s] = struct{}{}
	}

	// Every partition must contribute its standard suffix.
	for _, p := range partitions {
		assert.Contains(t, suffixes, p.DefaultConfig.DnsSuffix, "partition %s standard suffix missing", p.ID)
	}
}
