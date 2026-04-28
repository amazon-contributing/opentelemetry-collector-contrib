// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package costallocation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/service/pricing"
	"github.com/aws/aws-sdk-go/service/pricing/pricingiface"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type mockPricingAPI struct {
	pricingiface.PricingAPI
	output *pricing.GetProductsOutput
	err    error
}

func (m *mockPricingAPI) GetProductsPagesWithContext(_ context.Context, _ *pricing.GetProductsInput, fn func(*pricing.GetProductsOutput, bool) bool, _ ...request.Option) error {
	if m.err != nil {
		return m.err
	}
	fn(m.output, true)
	return nil
}

func makePriceListItem(instanceType string, usdPrice string) aws.JSONValue {
	return aws.JSONValue{
		"product": map[string]any{
			"attributes": map[string]any{
				"instanceType": instanceType,
			},
		},
		"terms": map[string]any{
			"OnDemand": map[string]any{
				"term1": map[string]any{
					"priceDimensions": map[string]any{
						"dim1": map[string]any{
							"pricePerUnit": map[string]any{
								"USD": usdPrice,
							},
						},
					},
				},
			},
		},
	}
}

func TestPricingClient_GetPrice_CacheHitMiss(t *testing.T) {
	mock := &mockPricingAPI{
		output: &pricing.GetProductsOutput{
			PriceList: []aws.JSONValue{
				makePriceListItem("m5.large", "0.0960000000"),
				makePriceListItem("c5.xlarge", "0.1700000000"),
			},
		},
	}
	pc := NewPricingClient(mock, "us-east-1", time.Hour, zap.NewNop())
	err := pc.Refresh(t.Context())
	require.NoError(t, err)

	price, ok := pc.GetPrice("m5.large")
	assert.True(t, ok)
	assert.InDelta(t, 0.096, price, 1e-9)

	price, ok = pc.GetPrice("c5.xlarge")
	assert.True(t, ok)
	assert.InDelta(t, 0.17, price, 1e-9)

	_, ok = pc.GetPrice("nonexistent.type")
	assert.False(t, ok)
}

func TestPricingClient_Refresh_APIError_ColdCache(t *testing.T) {
	mock := &mockPricingAPI{err: errors.New("api error")}
	pc := NewPricingClient(mock, "us-east-1", time.Hour, zap.NewNop())
	err := pc.Refresh(t.Context())
	assert.Error(t, err)

	_, ok := pc.GetPrice("m5.large")
	assert.False(t, ok, "cold cache should return miss after API error")
}

func TestPricingClient_Refresh_APIError_WarmCache(t *testing.T) {
	mock := &mockPricingAPI{
		output: &pricing.GetProductsOutput{
			PriceList: []aws.JSONValue{
				makePriceListItem("m5.large", "0.0960000000"),
			},
		},
	}
	pc := NewPricingClient(mock, "us-east-1", time.Hour, zap.NewNop())

	// First refresh succeeds.
	err := pc.Refresh(t.Context())
	require.NoError(t, err)

	// Second refresh fails.
	mock.err = errors.New("api error")
	err = pc.Refresh(t.Context())
	assert.Error(t, err)

	// Stale cache should still work.
	price, ok := pc.GetPrice("m5.large")
	assert.True(t, ok, "warm cache should still return data after API error")
	assert.InDelta(t, 0.096, price, 1e-9)
}

func TestPricingClient_Refresh_EmptyResults(t *testing.T) {
	mock := &mockPricingAPI{
		output: &pricing.GetProductsOutput{PriceList: []aws.JSONValue{}},
	}
	pc := NewPricingClient(mock, "us-east-1", time.Hour, zap.NewNop())
	err := pc.Refresh(t.Context())
	assert.Error(t, err, "should error on empty results")
}

func TestParsePriceListItem_ZeroPrice(t *testing.T) {
	item := makePriceListItem("t2.micro", "0.0000000000")
	_, _, err := parsePriceListItem(item)
	assert.Error(t, err, "should reject zero price")
}

func TestParsePriceListItem_MissingFields(t *testing.T) {
	_, _, err := parsePriceListItem(aws.JSONValue{})
	assert.Error(t, err)

	_, _, err = parsePriceListItem(aws.JSONValue{"product": "not a map"})
	assert.Error(t, err)
}
