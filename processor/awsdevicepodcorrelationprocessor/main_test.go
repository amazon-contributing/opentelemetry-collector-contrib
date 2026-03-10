// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsdevicepodcorrelationprocessor

import (
	"testing"

	"go.uber.org/goleak"
	"go.uber.org/zap"
)

// TestMain sets up the DefaultStoreFactory with a no-op mock so that the
// mdatagen-generated component lifecycle tests can create the processor,
// and runs goleak verification.
func TestMain(m *testing.M) {
	DefaultStoreFactory = func(_ *zap.Logger) (PodResourcesStoreInterface, error) {
		return newMockStore(nil), nil
	}
	goleak.VerifyTestMain(m)
}
