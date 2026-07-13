// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package sigv4authextension

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/confmap/confmaptest"
	"go.opentelemetry.io/collector/confmap/xconfmap"

	"github.com/open-telemetry/opentelemetry-collector-contrib/extension/sigv4authextension/internal/metadata"
	"github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/awsutilv2"
)

func TestLoadConfig(t *testing.T) {
	awsCredsProvider := mockCredentials()
	awsCreds, _ := (*awsCredsProvider).Retrieve(t.Context())

	t.Setenv("AWS_ACCESS_KEY_ID", awsCreds.AccessKeyID)
	t.Setenv("AWS_SECRET_ACCESS_KEY", awsCreds.SecretAccessKey)

	cm, err := confmaptest.LoadConf(filepath.Join("testdata", "config.yaml"))
	require.NoError(t, err)
	factory := NewFactory()
	cfg := factory.CreateDefaultConfig()
	sub, err := cm.Sub(component.NewID(metadata.Type).String())
	require.NoError(t, err)
	require.NoError(t, sub.Unmarshal(cfg))

	assert.NoError(t, xconfmap.Validate(cfg))
	expected := &Config{
		AWSSessionSettings: awsutilv2.CreateDefaultSessionConfig(),
		Service:            "service",
		AssumeRole: AssumeRole{
			SessionName: "role_session_name",
		},
	}
	expected.Region = "region"
	assert.Equal(t, expected, cfg)
}

func TestLoadWebIdentityConfig(t *testing.T) {
	cm, err := confmaptest.LoadConf(filepath.Join("testdata", "config.yaml"))
	require.NoError(t, err)
	factory := NewFactory()
	cfg := factory.CreateDefaultConfig()
	sub, err := cm.Sub(component.NewIDWithName(metadata.Type, "web_identity").String())
	require.NoError(t, err)
	require.NoError(t, sub.Unmarshal(cfg))

	assert.NoError(t, xconfmap.Validate(cfg))
	expected := &Config{
		AWSSessionSettings: awsutilv2.CreateDefaultSessionConfig(),
		Service:            "service",
		AssumeRole: AssumeRole{
			ARN:                  "arn:aws:iam::12345678910:role/my_role",
			WebIdentityTokenFile: "testdata/token_file",
		},
	}
	expected.Region = "region"
	assert.Equal(t, expected, cfg)
}

func TestLoadConfigError(t *testing.T) {
	cm, err := confmaptest.LoadConf(filepath.Join("testdata", "config.yaml"))
	require.NoError(t, err)
	factory := NewFactory()
	cfg := factory.CreateDefaultConfig()
	sub, err := cm.Sub(component.NewIDWithName(metadata.Type, "web_identity_no_arn").String())
	require.NoError(t, err)
	require.NoError(t, sub.Unmarshal(cfg))

	err = xconfmap.Validate(cfg)
	require.Error(t, err)
	assert.ErrorContains(t, err, "must specify role_arn or assume_role.arn")
}

func TestValidateRejectsBothRoleARNs(t *testing.T) {
	cfg := &Config{
		AWSSessionSettings: awsutilv2.AWSSessionSettings{
			RoleARN: "arn:aws:iam::123456789012:role/role1",
		},
		AssumeRole: AssumeRole{
			ARN: "arn:aws:iam::123456789012:role/role2",
		},
	}
	err := cfg.Validate()
	require.Error(t, err)
	assert.ErrorContains(t, err, "role_arn and assume_role.arn cannot both be set")
}

func TestValidateRejectsBothWebIdentityTokenFiles(t *testing.T) {
	cfg := &Config{
		AWSSessionSettings: awsutilv2.AWSSessionSettings{
			RoleARN:              "arn:aws:iam::123456789012:role/role1",
			WebIdentityTokenFile: "/path/to/token",
		},
		AssumeRole: AssumeRole{
			WebIdentityTokenFile: "/other/path/to/token",
		},
	}
	err := cfg.Validate()
	require.Error(t, err)
	assert.ErrorContains(t, err, "web_identity_token_file and assume_role.web_identity_token_file cannot both be set")
}

func TestValidateRequiresRoleARNWithWebIdentity(t *testing.T) {
	cfg := &Config{
		AWSSessionSettings: awsutilv2.AWSSessionSettings{
			WebIdentityTokenFile: "/path/to/token",
		},
	}
	err := cfg.Validate()
	require.Error(t, err)
	assert.ErrorContains(t, err, "must specify role_arn or assume_role.arn when using web_identity_token_file")
}

func TestResolvedRoleARN(t *testing.T) {
	const sessionARN = "arn:aws:iam::123456789012:role/session"
	const assumeARN = "arn:aws:iam::123456789012:role/assume"
	tests := []struct {
		name string
		cfg  *Config
		want string
	}{
		{
			name: "neither_set",
			cfg:  &Config{},
			want: "",
		},
		{
			name: "session_role_arn_only",
			cfg:  &Config{AWSSessionSettings: awsutilv2.AWSSessionSettings{RoleARN: sessionARN}},
			want: sessionARN,
		},
		{
			name: "assume_role_arn_only",
			cfg:  &Config{AssumeRole: AssumeRole{ARN: assumeARN}},
			want: assumeARN,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.cfg.resolvedRoleARN())
		})
	}
}
