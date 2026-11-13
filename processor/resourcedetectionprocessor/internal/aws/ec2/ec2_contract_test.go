// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package ec2

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/processor/processortest"
)

const (
	contractInstanceIDPath = "/latest/meta-data/instance-id"
	contractIdentityDoc    = `{"instanceId":"i-123","region":"us-west-2","accountId":"123456789012","availabilityZone":"us-west-2a","imageId":"ami-123","instanceType":"m5.large"}`
)

// fakeIMDS serves IMDSv2 and answers the first failCount requests to failPath with failStatus.
type fakeIMDS struct {
	mu         sync.Mutex
	failPath   string
	failStatus int
	failCount  int
	requests   map[string]int
}

func (f *fakeIMDS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests[r.URL.Path]++
	n := f.requests[r.URL.Path]
	f.mu.Unlock()

	if r.Method == http.MethodPut && r.URL.Path == "/latest/api/token" {
		w.Header().Set("X-Aws-Ec2-Metadata-Token-Ttl-Seconds", "21600")
		_, _ = w.Write([]byte("token"))
		return
	}
	if r.URL.Path == f.failPath && n <= f.failCount {
		w.WriteHeader(f.failStatus)
		return
	}
	switch r.URL.Path {
	case contractInstanceIDPath:
		_, _ = w.Write([]byte("i-123"))
	case "/latest/meta-data/hostname":
		_, _ = w.Write([]byte("ip-10-0-0-1.us-west-2.compute.internal"))
	case "/latest/dynamic/instance-identity/document":
		_, _ = w.Write([]byte(contractIdentityDoc))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakeIMDS) count(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[path]
}

// startFakeIMDS points the AWS SDK at a fake IMDS, independent of the caller's AWS environment.
func startFakeIMDS(t *testing.T, failPath string, failStatus, failCount int) *fakeIMDS {
	t.Helper()
	f := &fakeIMDS{failPath: failPath, failStatus: failStatus, failCount: failCount, requests: map[string]int{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	t.Setenv("AWS_CONFIG_FILE", dir+"/config")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", dir+"/credentials")
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_REGION", "us-west-2")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "false")
	t.Setenv("AWS_EC2_METADATA_V1_DISABLED", "")
	t.Setenv("AWS_EC2_METADATA_SERVICE_ENDPOINT", srv.URL)
	t.Setenv("AWS_EC2_METADATA_SERVICE_ENDPOINT_MODE", "")
	t.Setenv("IMDS_NUMBER_RETRY", "1")
	return f
}

// TestContract_EC2DetectRetriesTransientIMDSError pins the dual IMDS client in NewDetector.
// Two throttled (429) instance-id responses need the strict client's retry and the
// permissive fallback; the SDK default client treats a 429 as final.
func TestContract_EC2DetectRetriesTransientIMDSError(t *testing.T) {
	imdsSrv := startFakeIMDS(t, contractInstanceIDPath, http.StatusTooManyRequests, 2)

	det, err := NewDetector(processortest.NewNopSettings(processortest.NopType), CreateDefaultConfig(), false)
	require.NoError(t, err)

	res, _, err := det.Detect(t.Context())
	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"cloud.provider":          "aws",
		"cloud.platform":          "aws_ec2",
		"cloud.region":            "us-west-2",
		"cloud.account.id":        "123456789012",
		"cloud.availability_zone": "us-west-2a",
		"host.id":                 "i-123",
		"host.image.id":           "ami-123",
		"host.type":               "m5.large",
		"host.name":               "ip-10-0-0-1.us-west-2.compute.internal",
	}, res.Attributes().AsRaw())
	assert.Equal(t, 3, imdsSrv.count(contractInstanceIDPath), "strict attempt, strict retry, permissive attempt")
}
