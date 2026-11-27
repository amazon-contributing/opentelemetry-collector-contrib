// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awscontainerinsightreceiver

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/open-telemetry/opentelemetry-collector-contrib/internal/k8sconfig"
	"github.com/open-telemetry/opentelemetry-collector-contrib/internal/kubelet"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/stores/kubeletutil"
)

// newFakeKubelet starts a TLS server that fails the first failHandshakes TLS
// handshakes with an internal_error alert, the way a kubelet does before its
// serving certificate is signed. Later handshakes succeed and /pods returns an
// empty pod list. It returns a kubelet client pointed at the server and a
// counter of handshake attempts.
func newFakeKubelet(t *testing.T, failHandshakes int32) (*kubeletutil.KubeletClient, *atomic.Int32) {
	t.Helper()
	var attempts atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	srv.TLS = &tls.Config{
		GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
			if attempts.Add(1) <= failHandshakes {
				return nil, errors.New("serving certificate not yet signed")
			}
			return nil, nil
		},
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)

	host, port, err := net.SplitHostPort(srv.Listener.Addr().String())
	require.NoError(t, err)
	client, err := kubeletutil.NewKubeletClient(host, port, &kubelet.ClientConfig{
		APIConfig:          k8sconfig.APIConfig{AuthType: k8sconfig.AuthTypeTLS},
		InsecureSkipVerify: true,
	}, zap.NewNop())
	require.NoError(t, err)
	return client, &attempts
}

func setKubeletStartupTiming(t *testing.T, interval, timeout time.Duration) {
	t.Helper()
	oldInterval, oldTimeout := kubeletStartupPollInterval, kubeletStartupTimeout
	kubeletStartupPollInterval, kubeletStartupTimeout = interval, timeout
	t.Cleanup(func() {
		kubeletStartupPollInterval, kubeletStartupTimeout = oldInterval, oldTimeout
	})
}

func TestCheckKubeletTLSInternalError(t *testing.T) {
	client, _ := newFakeKubelet(t, 1)
	err := checkKubelet(client)
	require.Error(t, err)
	assert.ErrorContains(t, err, "cannot get pods from kubelet")
	assert.ErrorContains(t, err, "tls: internal error")
	require.NoError(t, checkKubelet(client))
}

func TestWaitForKubeletRecoversAfterTransientTLSFailures(t *testing.T) {
	client, attempts := newFakeKubelet(t, 3)
	require.NoError(t, waitForKubelet(t.Context(), client, time.Millisecond, zap.NewNop()))
	assert.GreaterOrEqual(t, attempts.Load(), int32(4))
}

func TestWaitForKubeletContextCanceled(t *testing.T) {
	client, _ := newFakeKubelet(t, 1<<30)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := waitForKubelet(ctx, client, time.Hour, zap.NewNop())
	require.ErrorIs(t, err, context.Canceled)
	assert.ErrorContains(t, err, "tls: internal error")
}

func TestWaitForKubeletOnStartupSucceeds(t *testing.T) {
	setKubeletStartupTiming(t, time.Millisecond, 5*time.Second)
	client, attempts := newFakeKubelet(t, 2)
	require.NoError(t, waitForKubeletOnStartup(t.Context(), client, zap.NewNop()))
	assert.GreaterOrEqual(t, attempts.Load(), int32(3))
}

func TestWaitForKubeletOnStartupTimesOut(t *testing.T) {
	setKubeletStartupTiming(t, 10*time.Millisecond, 100*time.Millisecond)
	client, attempts := newFakeKubelet(t, 1<<30)
	start := time.Now()
	err := waitForKubeletOnStartup(t.Context(), client, zap.NewNop())
	elapsed := time.Since(start)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.ErrorContains(t, err, "kubelet not reachable after 100ms")
	assert.ErrorContains(t, err, "cannot get pods from kubelet")
	assert.ErrorContains(t, err, "tls: internal error")
	assert.Greater(t, attempts.Load(), int32(1))
	assert.Less(t, elapsed, 2*time.Second)
}

func TestWaitForKubeletOnStartupHonorsParentCancel(t *testing.T) {
	setKubeletStartupTiming(t, time.Hour, time.Hour)
	client, _ := newFakeKubelet(t, 1<<30)
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(50*time.Millisecond, cancel)
	err := waitForKubeletOnStartup(ctx, client, zap.NewNop())
	require.ErrorIs(t, err, context.Canceled)
}
