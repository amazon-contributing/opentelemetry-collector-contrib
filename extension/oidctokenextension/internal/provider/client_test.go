// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewMetadataClient(t *testing.T) {
	c := NewMetadataClient()
	require.Equal(t, metadataClientTimeout, c.Timeout)

	tr, ok := c.Transport.(*http.Transport)
	require.True(t, ok)
	require.Nil(t, tr.Proxy)
}
