// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package nvme

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetLisScraperConfig(t *testing.T) {
	mockProvider := mockHostInfoProvider{}

	config := GetLisScraperConfig(mockProvider)

	assert.Equal(t, lisJobName, config.JobName)
	assert.Equal(t, lisScraperMetricsPath, config.MetricsPath)
	assert.Len(t, config.ServiceDiscoveryConfigs, 1)
	assert.NotEmpty(t, config.MetricRelabelConfigs)
}

func TestLisJobName(t *testing.T) {
	mockProvider := mockHostInfoProvider{}

	config := GetLisScraperConfig(mockProvider)
	assert.Equal(t, "containerInsightsNVMeLisExporterScraper", config.JobName)
}
