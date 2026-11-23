// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsutil

import (
	"crypto/tls"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// The httptest certificate is valid for example.com, so requests through
// the proxy use that host and the proxy dials the test server.
const proxiedURL = "https://example.com/"

const (
	envProxyHelper       = "AWSUTIL_TEST_HTTPS_PROXY_HELPER"
	envProxyHelperTarget = "AWSUTIL_TEST_HTTPS_PROXY_TARGET"
)

func newHTTP2TLSServer(t *testing.T) (srv *httptest.Server, caFile string) {
	t.Helper()
	srv = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, r.Proto)
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)

	caFile = filepath.Join(t.TempDir(), "ca.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	require.NoError(t, os.WriteFile(caFile, pemBytes, 0o600))
	return srv, caFile
}

// connectProxy accepts CONNECT requests, records the requested host and
// tunnels every connection to target.
type connectProxy struct {
	target   string
	mu       sync.Mutex
	connects []string
}

func newConnectProxy(t *testing.T, target string) (*connectProxy, string) {
	t.Helper()
	p := &connectProxy{target: target}
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)
	return p, srv.URL
}

func (p *connectProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodConnect {
		http.Error(w, "CONNECT only", http.StatusMethodNotAllowed)
		return
	}
	p.mu.Lock()
	p.connects = append(p.connects, r.Host)
	p.mu.Unlock()

	upstream, err := net.Dial("tcp", p.target)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer upstream.Close()
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	conn, buf, err := hj.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()
	go func() { _, _ = io.Copy(upstream, buf) }()
	_, _ = io.Copy(conn, upstream)
}

func (p *connectProxy) Connects() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.connects...)
}

func doGet(t *testing.T, client aws.HTTPClient, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, http.NoBody)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	_, err = io.Copy(io.Discard, resp.Body)
	require.NoError(t, err)
	return resp
}

func TestNewHTTPClient_HTTP2(t *testing.T) {
	srv, caFile := newHTTP2TLSServer(t)

	t.Run("Direct", func(t *testing.T) {
		client, err := newHTTPClient(zap.NewNop(), httpClientSettings{NumberOfWorkers: 1, RequestTimeoutSeconds: 10, caBundleEnv: caFile})
		require.NoError(t, err)
		assert.Nil(t, client.(*awshttp.BuildableClient).GetTransport().TLSNextProto)

		resp := doGet(t, client, srv.URL)
		assert.Equal(t, 2, resp.ProtoMajor)
	})

	t.Run("ThroughProxyAddress", func(t *testing.T) {
		proxy, proxyURL := newConnectProxy(t, srv.Listener.Addr().String())
		client, err := newHTTPClient(zap.NewNop(), httpClientSettings{NumberOfWorkers: 1, RequestTimeoutSeconds: 10, caBundleEnv: caFile, ProxyAddress: proxyURL})
		require.NoError(t, err)
		assert.Nil(t, client.(*awshttp.BuildableClient).GetTransport().TLSNextProto)

		resp := doGet(t, client, proxiedURL)
		assert.Equal(t, 2, resp.ProtoMajor)
		assert.Equal(t, []string{"example.com:443"}, proxy.Connects())
	})
}

func TestGetHTTPClient_CABundleHandshake(t *testing.T) {
	srv, caFile := newHTTP2TLSServer(t)

	t.Run("TrustedThroughAWSCABundle", func(t *testing.T) {
		t.Setenv("AWS_CA_BUNDLE", caFile)
		client, err := getHTTPClient(zap.NewNop(), &AWSSessionSettings{NumberOfWorkers: 1, RequestTimeoutSeconds: 10})
		require.NoError(t, err)
		resp := doGet(t, client, srv.URL)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("UntrustedWithoutBundle", func(t *testing.T) {
		t.Setenv("AWS_CA_BUNDLE", "")
		client, err := getHTTPClient(zap.NewNop(), &AWSSessionSettings{NumberOfWorkers: 1, RequestTimeoutSeconds: 10})
		require.NoError(t, err)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, http.NoBody)
		require.NoError(t, err)
		resp, err := client.Do(req)
		if resp != nil {
			resp.Body.Close()
		}
		var verifyErr *tls.CertificateVerificationError
		require.ErrorAs(t, err, &verifyErr)
	})
}

// http.ProxyFromEnvironment reads the environment once per process, so the
// HTTPS_PROXY case runs in a child test process.
func TestGetHTTPClient_HTTPSProxyEnv(t *testing.T) {
	if os.Getenv(envProxyHelper) == "1" {
		client, err := getHTTPClient(zap.NewNop(), &AWSSessionSettings{NumberOfWorkers: 1, RequestTimeoutSeconds: 10})
		require.NoError(t, err)
		resp := doGet(t, client, os.Getenv(envProxyHelperTarget))
		assert.Equal(t, 2, resp.ProtoMajor)
		return
	}

	srv, caFile := newHTTP2TLSServer(t)
	proxy, proxyURL := newConnectProxy(t, srv.Listener.Addr().String())

	// #nosec G204 -- re-runs this test binary
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestGetHTTPClient_HTTPSProxyEnv$", "-test.count=1")
	cmd.Env = append(envWithoutProxy(),
		envProxyHelper+"=1",
		envProxyHelperTarget+"="+proxiedURL,
		"HTTPS_PROXY="+proxyURL,
		"AWS_CA_BUNDLE="+caFile,
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	assert.Equal(t, []string{"example.com:443"}, proxy.Connects())
}

func envWithoutProxy() []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(name) {
		case "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "AWS_CA_BUNDLE", "REQUEST_METHOD":
			continue
		}
		env = append(env, kv)
	}
	return env
}
