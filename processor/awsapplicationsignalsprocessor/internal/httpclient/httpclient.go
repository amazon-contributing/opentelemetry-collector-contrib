// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: MIT

package httpclient // import "github.com/amazon-contributing/opentelemetry-collector-contrib/processor/awsapplicationsignalsprocessor/internal/httpclient"

import (
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"time"
)

const (
	defaultMaxRetries              = 3
	defaultTimeout                 = 1 * time.Second
	defaultBackoffRetryBaseInMills = 200
)

type HTTPClient struct {
	maxRetries              int
	backoffRetryBaseInMills int
	client                  *http.Client
}

func New() *HTTPClient {
	return &HTTPClient{
		maxRetries:              defaultMaxRetries,
		backoffRetryBaseInMills: defaultBackoffRetryBaseInMills,
		client:                  &http.Client{Timeout: defaultTimeout},
	}
}

func (h *HTTPClient) backoffSleep(currentRetryCount int) {
	backoffInMillis := int64(float64(h.backoffRetryBaseInMills) * math.Pow(2, float64(currentRetryCount)))
	sleepDuration := time.Millisecond * time.Duration(backoffInMillis)
	if sleepDuration > 60*1000 {
		sleepDuration = 60 * 1000
	}
	time.Sleep(sleepDuration)
}

func (h *HTTPClient) Request(endpoint string) (body []byte, err error) {
	for i := 0; i < h.maxRetries; i++ {
		body, err = h.request(endpoint)
		if err != nil {
			log.Printf("W! retry [%d/%d], unable to get http response from %s, error: %v", i, h.maxRetries, endpoint, err)
			h.backoffSleep(i)
		}
	}
	return
}

func (h *HTTPClient) request(endpoint string) ([]byte, error) {
	resp, err := h.client.Get(endpoint)
	if err != nil {
		return nil, fmt.Errorf("unable to get response from %s, error: %w", endpoint, err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unable to get response from %s, status code: %d", endpoint, resp.StatusCode)
	}

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("unable to read response body from %s, error: %w", endpoint, err)
	}

	return body, nil
}
