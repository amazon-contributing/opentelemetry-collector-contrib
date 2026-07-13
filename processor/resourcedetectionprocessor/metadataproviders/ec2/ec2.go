// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Package ec2 re-exports the internal EC2 metadata provider for external use.
package ec2 // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/resourcedetectionprocessor/metadataproviders/ec2"

import internal "github.com/open-telemetry/opentelemetry-collector-contrib/internal/metadataproviders/aws/ec2"

type Provider = internal.Provider

var NewProvider = internal.NewProvider
