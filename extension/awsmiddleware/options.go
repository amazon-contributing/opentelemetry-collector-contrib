// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsmiddleware // import "github.com/amazon-contributing/opentelemetry-collector-contrib/extension/awsmiddleware"

import (
	"github.com/aws/aws-sdk-go-v2/aws"
)

type SDKVersion interface {
	unused()
}

type sdkVersion2 struct {
	SDKVersion
	cfg *aws.Config
}

// SDKv2 takes in an AWS SDKv2 config.
func SDKv2(cfg *aws.Config) SDKVersion {
	return sdkVersion2{cfg: cfg}
}
