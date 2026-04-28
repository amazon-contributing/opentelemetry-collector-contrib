// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package costallocation // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/costallocation"

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/pricing"
	"github.com/aws/aws-sdk-go/service/pricing/pricingiface"
	"go.uber.org/zap"
)

// PricingClient retrieves and caches on-demand hourly pricing from the AWS Pricing API.
type PricingClient struct {
	client          pricingiface.PricingAPI
	cache           map[string]float64 // instanceType -> hourly price USD
	cacheMu         sync.RWMutex
	refreshInterval time.Duration
	region          string
	logger          *zap.Logger
	stopCh          chan struct{}
}

// NewPricingClient creates a new PricingClient.
func NewPricingClient(client pricingiface.PricingAPI, region string, refreshInterval time.Duration, logger *zap.Logger) *PricingClient {
	return &PricingClient{
		client:          client,
		cache:           make(map[string]float64),
		refreshInterval: refreshInterval,
		region:          region,
		logger:          logger,
		stopCh:          make(chan struct{}),
	}
}

// GetPrice returns the cached on-demand hourly price for the given instance type.
// Returns (price, true) on cache hit, or (0, false) on cache miss.
func (pc *PricingClient) GetPrice(instanceType string) (float64, bool) {
	pc.cacheMu.RLock()
	defer pc.cacheMu.RUnlock()
	price, ok := pc.cache[instanceType]
	return price, ok
}

// Refresh fetches on-demand pricing for all Linux EC2 instance types in the configured region.
func (pc *PricingClient) Refresh(ctx context.Context) error {

	filters := []*pricing.Filter{
		{Type: aws.String("TERM_MATCH"), Field: aws.String("operatingSystem"), Value: aws.String("Linux")},
		{Type: aws.String("TERM_MATCH"), Field: aws.String("tenancy"), Value: aws.String("Shared")},
		{Type: aws.String("TERM_MATCH"), Field: aws.String("preInstalledSw"), Value: aws.String("NA")},
		{Type: aws.String("TERM_MATCH"), Field: aws.String("regionCode"), Value: aws.String(pc.region)},
	}

	newCache := make(map[string]float64)
	input := &pricing.GetProductsInput{
		ServiceCode: aws.String("AmazonEC2"),
		Filters:     filters,
	}

	err := pc.client.GetProductsPagesWithContext(ctx, input, func(page *pricing.GetProductsOutput, lastPage bool) bool {
		for _, priceJSON := range page.PriceList {
			instType, price, parseErr := parsePriceListItem(priceJSON)
			if parseErr != nil {
				pc.logger.Debug("Skipping price list item", zap.Error(parseErr))
				continue
			}
			newCache[instType] = price
		}
		return !lastPage
	})
	if err != nil {
		return fmt.Errorf("GetProducts API call failed: %w", err)
	}

	if len(newCache) == 0 {
		return fmt.Errorf("pricing API returned no results for region %s", pc.region)
	}

	pc.cacheMu.Lock()
	pc.cache = newCache
	pc.cacheMu.Unlock()

	pc.logger.Info("Refreshed EC2 pricing cache", zap.Int("instanceTypes", len(newCache)))
	return nil
}

// parsePriceListItem extracts the instance type and on-demand hourly price from a Pricing API response item.
func parsePriceListItem(raw aws.JSONValue) (string, float64, error) {
	product, ok := raw["product"].(map[string]any)
	if !ok {
		return "", 0, errors.New("missing product field")
	}
	attrs, ok := product["attributes"].(map[string]any)
	if !ok {
		return "", 0, errors.New("missing product.attributes")
	}
	instType, ok := attrs["instanceType"].(string)
	if !ok || instType == "" {
		return "", 0, errors.New("missing instanceType attribute")
	}

	terms, ok := raw["terms"].(map[string]any)
	if !ok {
		return "", 0, errors.New("missing terms field")
	}
	onDemand, ok := terms["OnDemand"].(map[string]any)
	if !ok {
		return "", 0, errors.New("missing OnDemand terms")
	}

	for _, termValue := range onDemand {
		termMap, ok := termValue.(map[string]any)
		if !ok {
			continue
		}
		priceDimensions, ok := termMap["priceDimensions"].(map[string]any)
		if !ok {
			continue
		}
		for _, dim := range priceDimensions {
			dimMap, ok := dim.(map[string]any)
			if !ok {
				continue
			}
			pricePerUnit, ok := dimMap["pricePerUnit"].(map[string]any)
			if !ok {
				continue
			}
			usdStr, ok := pricePerUnit["USD"].(string)
			if !ok {
				continue
			}
			price, err := strconv.ParseFloat(usdStr, 64)
			if err != nil {
				continue
			}
			if price > 0 {
				return instType, price, nil
			}
		}
	}
	return "", 0, fmt.Errorf("no valid USD price found for %s", instType)
}

// Start begins the periodic cache refresh goroutine.
func (pc *PricingClient) Start(ctx context.Context) {
	// Do an initial refresh synchronously.
	if err := pc.Refresh(ctx); err != nil {
		pc.logger.Warn("Initial pricing refresh failed", zap.Error(err))
	}

	go func() {
		ticker := time.NewTicker(pc.refreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := pc.Refresh(ctx); err != nil {
					pc.logger.Warn("Pricing cache refresh failed, using stale data", zap.Error(err))
				}
			case <-pc.stopCh:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
}

// Shutdown stops the periodic refresh goroutine.
func (pc *PricingClient) Shutdown() {
	close(pc.stopCh)
}
