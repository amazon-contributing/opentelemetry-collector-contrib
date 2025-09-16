// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: MIT

package histograms

import (
	"testing"

	"github.com/amazon-contributing/opentelemetry-collector-contrib/share/testdata/histograms"
)

func TestGenerate(t *testing.T) {
	datasets := histograms.GenerateTestDataWithSeed(12345)

	for _, ds := range datasets {

	}
}
