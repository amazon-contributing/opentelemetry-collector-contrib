// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package metadata

import "go.opentelemetry.io/collector/component"

var (
	Type             = component.MustNewType("awsdevicejobcorrelation")
	MetricsStability = component.StabilityLevelAlpha
)
