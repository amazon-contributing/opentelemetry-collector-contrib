// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: MIT

package prune // import "github.com/amazon-contributing/opentelemetry-collector-contrib/processor/awsapplicationsignalsprocessor/internal/prune"

import (
	"errors"
	"fmt"

	"github.com/amazon-contributing/opentelemetry-collector-contrib/processor/awsapplicationsignalsprocessor/common"
	"go.opentelemetry.io/collector/pdata/pcommon"
)

type MetricPruner struct {
}

func (p *MetricPruner) ShouldBeDropped(attributes pcommon.Map) (bool, error) {
	for _, attributeKey := range common.CWMetricAttributes {
		if val, ok := attributes.Get(attributeKey); ok {
			if !isASCIIPrintable(val.Str()) {
				return true, errors.New("Metric attribute " + attributeKey + " must contain only ASCII characters.")
			}
		}
		if _, ok := attributes.Get(common.MetricAttributeTelemetrySource); !ok {
			return true, fmt.Errorf("Metric must contain %s", common.MetricAttributeTelemetrySource)
		}
	}
	return false, nil
}

func NewPruner() *MetricPruner {
	return &MetricPruner{}
}

func isASCIIPrintable(val string) bool {
	nonWhitespaceFound := false
	for _, c := range val {
		if c < 32 || c > 126 {
			return false
		} else if !nonWhitespaceFound && c != 32 {
			nonWhitespaceFound = true
		}
	}
	return nonWhitespaceFound
}
