// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package prometheusscraper

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.uber.org/zap"
)

type mockHostInfoProvider struct{}

func (mockHostInfoProvider) GetClusterName() string {
	return "cluster-name"
}

func (mockHostInfoProvider) GetInstanceID() string {
	return "i-000000000"
}

func (mockHostInfoProvider) GetInstanceType() string {
	return "instance-type"
}

func TestSimplePrometheusScraperBadInputs(t *testing.T) {
	settings := componenttest.NewNopTelemetrySettings()
	settings.Logger, _ = zap.NewDevelopment()

	tests := []SimplePrometheusScraperOpts{
		{
			Ctx:               t.Context(),
			TelemetrySettings: settings,
			Consumer:          nil,
			Host:              componenttest.NewNopHost(),
			HostInfoProvider:  mockHostInfoProvider{},
		},
		{
			Ctx:               t.Context(),
			TelemetrySettings: settings,
			Consumer:          MockConsumer{},
			Host:              nil,
			HostInfoProvider:  mockHostInfoProvider{},
		},
		{
			Ctx:               t.Context(),
			TelemetrySettings: settings,
			Consumer:          MockConsumer{},
			Host:              componenttest.NewNopHost(),
			HostInfoProvider:  nil,
		},
	}

	for _, tt := range tests {
		scraper, err := NewSimplePrometheusScraper(tt)

		assert.Error(t, err)
		assert.Nil(t, scraper)
	}
}
