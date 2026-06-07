// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package aws

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/ec2/imds"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fastTestOptions points the client at a test server and removes the default
// timeout/backoff so fallback tests run quickly and deterministically.
func fastTestOptions(endpoint string) func(*imds.Options) {
	return func(o *imds.Options) {
		o.Endpoint = endpoint
		o.DisableDefaultTimeout = true
		o.DisableDefaultMaxBackoff = true
	}
}

func enableIMDS(t *testing.T) {
	t.Helper()
	t.Setenv("AWS_EC2_METADATA_DISABLED", "false")
}

func TestStrictOptions(t *testing.T) {
	var o imds.Options
	for _, fn := range strictOptions(2, nil) {
		fn(&o)
	}
	assert.Equal(t, aws.FalseTernary, o.EnableFallback, "strict client must disable IMDSv1 fallback")
	require.NotNil(t, o.Retryer, "strict client must use the IMDSRetryer")
	r, ok := o.Retryer.(*IMDSRetryer)
	require.True(t, ok, "strict client retryer must be *IMDSRetryer")
	assert.Equal(t, 3, r.MaxAttempts(), "retries=2 → MaxAttempts=3 (v2 counts the first attempt)")
}

func TestPermissiveOptions(t *testing.T) {
	var o imds.Options
	for _, fn := range permissiveOptions(nil) {
		fn(&o)
	}
	assert.Equal(t, aws.TrueTernary, o.EnableFallback, "permissive client must enable IMDSv1 fallback")
	assert.Nil(t, o.Retryer, "permissive client uses the default retryer")
}

// TestStrictOptions_CallerOptionsApplyFirst verifies that caller-supplied
// options cannot override the strict fallback setting.
func TestStrictOptions_CallerOptionsApplyFirst(t *testing.T) {
	var o imds.Options
	caller := func(opt *imds.Options) {
		opt.EnableFallback = aws.TrueTernary // caller tries to enable fallback
		opt.Endpoint = "http://example"
	}
	for _, fn := range strictOptions(0, []func(*imds.Options){caller}) {
		fn(&o)
	}
	assert.Equal(t, "http://example", o.Endpoint, "caller option must flow through")
	assert.Equal(t, aws.FalseTernary, o.EnableFallback, "strict setting must win over caller")
}

// imdsTestServer emulates the parts of IMDS exercised by these tests. When
// failTokens is true it rejects the IMDSv2 token request (PUT
// /latest/api/token) with 403, which forces the strict (IMDSv2-only) client to
// fail and the permissive client to fall back to the token-less IMDSv1 flow.
// It serves the instance identity document (used by GetRegion and
// GetInstanceIdentityDocument) and any keys in the metadata map.
func imdsTestServer(t *testing.T, failTokens bool, region string, metadata map[string]string) (*httptest.Server, *int) {
	t.Helper()
	var tokenRequests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/latest/api/token":
			tokenRequests++
			if failTokens {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			w.Header().Set("X-aws-ec2-metadata-token-ttl-seconds", "21600")
			_, _ = w.Write([]byte("test-token"))
		case r.URL.Path == "/latest/dynamic/instance-identity/document":
			_, _ = w.Write([]byte(`{"region":"` + region + `","instanceId":"i-test","instanceType":"t3.micro"}`))
		default:
			key := strings.TrimPrefix(r.URL.Path, "/latest/meta-data/")
			if val, ok := metadata[key]; ok {
				_, _ = w.Write([]byte(val))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &tokenRequests
}

func TestIMDSClient_StrictSucceeds(t *testing.T) {
	enableIMDS(t)
	srv, tokenRequests := imdsTestServer(t, false, "us-west-2", nil)

	c := NewIMDSClient(nil, 0, fastTestOptions(srv.URL))
	out, err := c.GetRegion(context.Background(), &imds.GetRegionInput{})

	require.NoError(t, err)
	assert.Equal(t, "us-west-2", out.Region)
	assert.Positive(t, *tokenRequests, "strict client should request an IMDSv2 token")
}

func TestIMDSClient_FallsBackToPermissive(t *testing.T) {
	enableIMDS(t)
	// Token endpoint fails → strict (IMDSv2-only) errors → permissive client
	// falls back to the token-less IMDSv1 flow and succeeds.
	srv, _ := imdsTestServer(t, true, "eu-central-1", nil)

	c := NewIMDSClient(nil, 0, fastTestOptions(srv.URL))
	out, err := c.GetRegion(context.Background(), &imds.GetRegionInput{})

	require.NoError(t, err)
	assert.Equal(t, "eu-central-1", out.Region)
}

func TestIMDSClient_GetInstanceIdentityDocumentFallback(t *testing.T) {
	enableIMDS(t)
	srv, _ := imdsTestServer(t, true, "ap-south-1", nil)

	c := NewIMDSClientFromConfig(aws.Config{}, nil, 0, fastTestOptions(srv.URL))
	out, err := c.GetInstanceIdentityDocument(context.Background(), &imds.GetInstanceIdentityDocumentInput{})

	require.NoError(t, err)
	assert.Equal(t, "ap-south-1", out.Region)
	assert.Equal(t, "i-test", out.InstanceID)
}

func TestIMDSClient_GetMetadataFallback(t *testing.T) {
	enableIMDS(t)
	srv, _ := imdsTestServer(t, true, "us-west-2", map[string]string{"instance-id": "i-0123456789abcdef0"})

	c := NewIMDSClientFromConfig(aws.Config{}, nil, 0, fastTestOptions(srv.URL))
	out, err := c.GetMetadata(context.Background(), &imds.GetMetadataInput{Path: "instance-id"})

	require.NoError(t, err)
	defer out.Content.Close()
	body := make([]byte, 64)
	n, _ := out.Content.Read(body)
	assert.Equal(t, "i-0123456789abcdef0", string(body[:n]))
}

// TestIMDSClient_BothFail confirms an error is returned (not a panic) when both
// the strict and permissive clients fail.
func TestIMDSClient_BothFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)

	c := NewIMDSClient(nil, 0, fastTestOptions(srv.URL))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := c.GetRegion(ctx, &imds.GetRegionInput{})
	assert.Error(t, err)
}
