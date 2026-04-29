// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package eks

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/processor/processortest"
	"go.uber.org/zap"

	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/resourcedetectionprocessor/internal/aws/eks/internal/metadata"
)

const (
	clusterName    = "my-cluster"
	cloudAccountID = "cloud1234"
)

type MockDetectorUtils struct {
	mock.Mock
}

func (detectorUtils *MockDetectorUtils) getClusterName(_ context.Context, _ *zap.Logger) string {
	var reservations []types.Reservation
	return detectorUtils.getClusterNameTagFromReservations(reservations)
}

func (detectorUtils *MockDetectorUtils) getClusterNameTagFromReservations(_ []types.Reservation) string {
	return clusterName
}

func (detectorUtils *MockDetectorUtils) getCloudAccountID(_ context.Context, _ *zap.Logger) string {
	return cloudAccountID
}

func (detectorUtils *MockDetectorUtils) getOIDCIssuer(_ context.Context) (string, error) {
	args := detectorUtils.Called()
	return args.String(0), args.Error(1)
}

func (detectorUtils *MockDetectorUtils) getServerVersion(_ context.Context) (string, error) {
	args := detectorUtils.Called()
	return args.String(0), args.Error(1)
}

func TestNewDetector(t *testing.T) {
	dcfg := CreateDefaultConfig()
	detector, err := NewDetector(processortest.NewNopSettings(processortest.NopType), dcfg)
	assert.NoError(t, err)
	assert.NotNil(t, detector)
	// no-op
	gotResource, gotSchema, gotErr := detector.Detect(t.Context())
	assert.NoError(t, gotErr)
	assert.Equal(t, pcommon.NewResource(), gotResource)
	assert.Empty(t, gotSchema)
}

// Tests EKS resource detector running in EKS environment
func TestEKS(t *testing.T) {
	detectorUtils := new(MockDetectorUtils)
	ctx := t.Context()

	t.Setenv("KUBERNETES_SERVICE_HOST", "localhost")
	t.Setenv("AWS_WEB_IDENTITY_TOKEN_FILE", "/var/run/secrets/eks.amazonaws.com/serviceaccount/token")

	// Call EKS Resource detector to detect resources
	eksResourceDetector := &detector{utils: detectorUtils, logger: zap.NewNop(), ra: metadata.DefaultResourceAttributesConfig(), rb: metadata.NewResourceBuilder(metadata.DefaultResourceAttributesConfig())}
	res, _, err := eksResourceDetector.Detect(ctx)
	require.NoError(t, err)

	assert.Equal(t, map[string]any{
		"cloud.provider": "aws",
		"cloud.platform": "aws_eks",
	}, res.Attributes().AsRaw(), "Resource object returned is incorrect")
}

// Tests EKS resource detector not running in EKS environment by verifying resource is not running on k8s
func TestNotEKS(t *testing.T) {
	eksResourceDetector := detector{logger: zap.NewNop()}
	r, _, err := eksResourceDetector.Detect(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 0, r.Attributes().Len(), "Resource object should be empty")
}

func TestEKSResourceDetection_ForCloudAccountID(t *testing.T) {
	tests := []struct {
		name           string
		ra             metadata.ResourceAttributesConfig
		expectedOutput map[string]any
		shouldError    bool
	}{
		{
			name: "Detects CloudAccountID when enabled",
			ra: metadata.ResourceAttributesConfig{
				CloudAccountID: metadata.ResourceAttributeConfig{Enabled: true},
			},
			expectedOutput: map[string]any{
				"cloud.account.id": "cloud1234",
			},
			shouldError: false,
		},
		{
			name: "Does not detect CloudAccountID when disabled",
			ra: metadata.ResourceAttributesConfig{
				CloudAccountID: metadata.ResourceAttributeConfig{Enabled: false},
			},
			expectedOutput: map[string]any{},
			shouldError:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detectorUtils := new(MockDetectorUtils)
			ctx := t.Context()

			t.Setenv("KUBERNETES_SERVICE_HOST", "localhost")
			t.Setenv("AWS_WEB_IDENTITY_TOKEN_FILE", "/var/run/secrets/eks.amazonaws.com/serviceaccount/token")

			eksResourceDetector := &detector{
				utils:  detectorUtils,
				logger: zap.NewNop(),
				ra:     tt.ra,
				rb:     metadata.NewResourceBuilder(tt.ra),
			}
			res, _, err := eksResourceDetector.Detect(ctx)

			if tt.shouldError {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
			assert.Equal(t, tt.expectedOutput, res.Attributes().AsRaw())
		})
	}
}

func TestIsEKS(t *testing.T) {
	tests := []struct {
		name           string
		k8sServiceHost string
		webIdentity    string
		containerAuth  string
		oidcIssuer     string
		oidcErr        error
		serverVersion  string
		versionErr     error
		expectOIDC     bool
		expectVersion  bool
		expected       bool
		expectErr      bool
	}{
		{
			name:           "Not K8s",
			k8sServiceHost: "",
			expected:       false,
		},
		{
			name:           "IRSA token path",
			k8sServiceHost: "localhost",
			webIdentity:    "/var/run/secrets/eks.amazonaws.com/serviceaccount/token",
			expected:       true,
		},
		{
			name:           "Pod Identity path",
			k8sServiceHost: "localhost",
			containerAuth:  "/var/run/secrets/eks-pod-identity/token",
			expected:       true,
		},
		{
			name:           "OIDC issuer EKS",
			k8sServiceHost: "localhost",
			oidcIssuer:     "https://oidc.eks.us-west-2.amazonaws.com/id/ABC",
			expectOIDC:     true,
			expected:       true,
		},
		{
			name:           "OIDC error falls through to version",
			k8sServiceHost: "localhost",
			oidcErr:        errors.New("connection refused"),
			expectOIDC:     true,
			serverVersion:  "v1.28.2-eks-a5df82a",
			expectVersion:  true,
			expected:       true,
		},
		{
			name:           "Version -eks- match",
			k8sServiceHost: "localhost",
			oidcIssuer:     "https://other.issuer.com",
			expectOIDC:     true,
			serverVersion:  "v1.32.3-eks-d0fe756",
			expectVersion:  true,
			expected:       true,
		},
		{
			name:           "Version error (all fail)",
			k8sServiceHost: "localhost",
			oidcErr:        errors.New("connection refused"),
			expectOIDC:     true,
			versionErr:     errors.New("connection refused"),
			expectVersion:  true,
			expected:       false,
			expectErr:      true,
		},
		{
			name:           "Version not EKS",
			k8sServiceHost: "localhost",
			oidcIssuer:     "https://other.issuer.com",
			expectOIDC:     true,
			serverVersion:  "v1.25.0",
			expectVersion:  true,
			expected:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockUtils := new(MockDetectorUtils)

			t.Setenv("KUBERNETES_SERVICE_HOST", tt.k8sServiceHost)
			t.Setenv("AWS_WEB_IDENTITY_TOKEN_FILE", tt.webIdentity)
			t.Setenv("AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE", tt.containerAuth)

			if tt.expectOIDC {
				mockUtils.On("getOIDCIssuer").Return(tt.oidcIssuer, tt.oidcErr)
			}
			if tt.expectVersion {
				mockUtils.On("getServerVersion").Return(tt.serverVersion, tt.versionErr)
			}

			result, err := isEKS(t.Context(), mockUtils, zap.NewNop())

			assert.Equal(t, tt.expected, result)
			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			mockUtils.AssertExpectations(t)
		})
	}
}
