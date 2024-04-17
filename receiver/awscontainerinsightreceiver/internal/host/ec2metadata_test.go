// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package host

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/awstesting/mock"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	ec2provider "github.com/open-telemetry/opentelemetry-collector-contrib/internal/metadataproviders/aws/ec2"
)

type mockMetadataProvider struct {
	ec2provider.Provider
	count int
}

func (m *mockMetadataProvider) Get(_ context.Context) (*ec2provider.Metadata, error) {
	m.count++
	if m.count == 1 {
		return nil, errors.New("error")
	}

	return &ec2provider.Metadata{
		Region:       "us-west-2",
		InstanceID:   "i-abcd1234",
		InstanceType: "c4.xlarge",
		PrivateIP:    "79.168.255.0",
	}, nil
}

func TestEC2Metadata(t *testing.T) {
	ctx := context.Background()
	sess := mock.Session
	instanceIDReadyC := make(chan bool)
	instanceIPReadyP := make(chan bool)
	clientOption := func(e *ec2Metadata) {
		e.provider = &mockMetadataProvider{}
	}
	e := newEC2Metadata(ctx, sess, 3*time.Millisecond, instanceIDReadyC, instanceIPReadyP, false, 0, zap.NewNop(), clientOption)
	assert.NotNil(t, e)

	<-instanceIDReadyC
	<-instanceIPReadyP
	assert.Equal(t, "i-abcd1234", e.getInstanceID())
	assert.Equal(t, "c4.xlarge", e.getInstanceType())
	assert.Equal(t, "us-west-2", e.getRegion())
	assert.Equal(t, "79.168.255.0", e.getInstanceIP())
}
