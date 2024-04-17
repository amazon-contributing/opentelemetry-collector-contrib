// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package ec2

import (
	"testing"

	awsmock "github.com/aws/aws-sdk-go/awstesting/mock"
	"github.com/stretchr/testify/assert"
)

func TestNewMetadataProvider(t *testing.T) {
	mp := NewProvider(
		awsmock.Session,
		WithIMDSv2Retries(0),
	)
	cmp, ok := mp.(*chainMetadataProvider)
	assert.True(t, ok)
	assert.Len(t, cmp.providers, 3)
}
