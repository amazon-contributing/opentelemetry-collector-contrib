// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awscontainerinsightskueuereceiver // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightskueuereceiver"

import (
	"context"
	"fmt"
	"runtime"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/receiver"
	"go.uber.org/zap"

	ci "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/containerinsight"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightskueuereceiver/internal/kueuescraper"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightskueuereceiver/internal/tokenprovider"
)

var _ receiver.Metrics = (*awsContainerInsightKueueReceiver)(nil)

// awsContainerInsightKueueReceiver implements the receiver.Metrics
type awsContainerInsightKueueReceiver struct {
	settings        component.TelemetrySettings
	nextConsumer    consumer.Metrics
	config          *Config
	cancel          context.CancelFunc
	tokenProvider   *tokenprovider.BearerTokenProvider
	tokenGeneration int
	kueueScraper    *kueuescraper.KueuePrometheusScraper
}

// newAWSContainerInsightsKueueReceiver creates the AWS Container Insights Kueue receiver with the given parameters.
func newAWSContainerInsightsKueueReceiver(
	settings component.TelemetrySettings,
	config *Config,
	nextConsumer consumer.Metrics,
) (receiver.Metrics, error) {
	r := &awsContainerInsightKueueReceiver{
		settings:      settings,
		nextConsumer:  nextConsumer,
		config:        config,
		tokenProvider: tokenprovider.NewBearerTokenProvider(),
	}
	return r, nil
}

// Start collecting metrics from Kueue metrics prometheusendpoint
func (akr *awsContainerInsightKueueReceiver) Start(ctx context.Context, host component.Host) error {
	ctx, akr.cancel = context.WithCancel(ctx)

	// wait for kubelet availability, but don't block on it
	go func() {
		if err := akr.init(ctx, host); err != nil {
			akr.settings.Logger.Error("Unable to initialize receiver for Kueue metrics", zap.Error(err))
			return
		}
		akr.start(ctx, host)
	}()

	return nil
}

func (akr *awsContainerInsightKueueReceiver) init(ctx context.Context, host component.Host) error {
	if runtime.GOOS == ci.OperatingSystemWindows {
		return fmt.Errorf("unsupported operating system: %s", ci.OperatingSystemWindows)
	}

	bearerToken, tokenErr := akr.tokenProvider.GetToken()

	if tokenErr != nil {
		akr.settings.Logger.Warn("Unable to retrieve bearer token", zap.Error(tokenErr))
		return tokenErr
	}

	akr.tokenGeneration = akr.tokenProvider.TokenGeneration()

	err := akr.initKueuePrometheusScraper(ctx, host, bearerToken)
	if err != nil {
		akr.settings.Logger.Warn("Unable to start kueue prometheus scraper", zap.Error(err))
		return err
	}
	return nil
}

func (akr *awsContainerInsightKueueReceiver) start(ctx context.Context, host component.Host) {
	ticker := time.NewTicker(akr.config.CollectionInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			akr.collectData()
			// perform a token load to check if generation has changed
			_, err := akr.tokenProvider.GetToken()
			if err != nil {
				akr.settings.Logger.Warn("Unable to retrieve bearer token, receiver will continue without refreshing", zap.Error(err))
			} else {
				if akr.tokenGeneration != akr.tokenProvider.TokenGeneration() {
					akr.settings.Logger.Info("Token generation has changed, restarting receiver")
					akr.shutdownScraper()
					akr.init(ctx, host)
				}
			}
		case <-ctx.Done():
			return
		}
	}
}

func (akr *awsContainerInsightKueueReceiver) initKueuePrometheusScraper(
	ctx context.Context,
	host component.Host,
	bearerToken string,
) error {
	var err error
	akr.kueueScraper, err = kueuescraper.NewKueuePrometheusScraper(kueuescraper.KueuePrometheusScraperOpts{
		Ctx:               ctx,
		TelemetrySettings: akr.settings,
		Consumer:          akr.nextConsumer,
		Host:              host,
		ClusterName:       akr.config.ClusterName,
		BearerToken:       bearerToken,
	})
	return err
}

// Shutdown stops the awsContainerInsightKueueReceiver receiver.
func (akr *awsContainerInsightKueueReceiver) Shutdown(context.Context) error {
	if akr.cancel == nil {
		return nil
	}
	akr.cancel()

	akr.shutdownScraper()

	return nil
}

func (akr *awsContainerInsightKueueReceiver) shutdownScraper() {
	if akr.kueueScraper != nil {
		akr.kueueScraper.Shutdown()
	}
}

func (akr *awsContainerInsightKueueReceiver) collectData() {
	if akr.kueueScraper != nil {
		// this does not return any metrics, it just ensures scraping is running on elected leader node
		akr.kueueScraper.GetMetrics() //nolint:errcheck
	}
}
