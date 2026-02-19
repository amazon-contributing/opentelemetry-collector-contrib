// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Package azure re-exports the internal Azure metadata provider for external use.
package azure // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/resourcedetectionprocessor/metadataproviders/azure"

import (
	internal "github.com/open-telemetry/opentelemetry-collector-contrib/internal/metadataproviders/azure"
)

type Provider = internal.Provider

type ComputeMetadata = internal.ComputeMetadata

type ComputeTagsListMetadata = internal.ComputeTagsListMetadata

var NewProvider = internal.NewProvider
