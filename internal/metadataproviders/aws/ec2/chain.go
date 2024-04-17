// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package ec2 // import "github.com/open-telemetry/opentelemetry-collector-contrib/internal/metadataproviders/aws/ec2"

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type chainMetadataProvider struct {
	providers []Provider
}

func newChainMetadataProvider(providers []Provider) *chainMetadataProvider {
	return &chainMetadataProvider{providers: providers}
}

func (p *chainMetadataProvider) ID() string {
	var providerIDs []string
	for _, provider := range p.providers {
		providerIDs = append(providerIDs, provider.ID())
	}
	return fmt.Sprintf("Chain [%s]", strings.Join(providerIDs, ","))
}

func (p *chainMetadataProvider) Get(ctx context.Context) (*Metadata, error) {
	var errs error
	for _, provider := range p.providers {
		if metadata, err := provider.Get(ctx); err != nil {
			errs = errors.Join(errs, fmt.Errorf("unable to get metadata from %s: %w", provider.ID(), err))
		} else {
			return metadata, nil
		}
	}
	return nil, errs
}

func (p *chainMetadataProvider) Hostname(ctx context.Context) (string, error) {
	var errs error
	for _, provider := range p.providers {
		if hostname, err := provider.Hostname(ctx); err != nil {
			errs = errors.Join(errs, fmt.Errorf("unable to get hostname from %s: %w", provider.ID(), err))
		} else {
			return hostname, nil
		}
	}
	return "", errs
}
