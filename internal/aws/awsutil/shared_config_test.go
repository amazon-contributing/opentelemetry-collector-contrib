// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetFallbackSharedConfigFiles(t *testing.T) {
	noOpGetUserHomeDir := func() string { return "home" }
	t.Setenv(envAwsSdkLoadConfig, "true")
	t.Setenv(envAwsSharedCredentialsFile, "credentials")
	t.Setenv(envAwsSharedConfigFile, "config")

	credFiles, cfgFiles := getFallbackSharedConfigFiles(noOpGetUserHomeDir)
	assert.Equal(t, []string{"credentials"}, credFiles)
	assert.Equal(t, []string{"config"}, cfgFiles)

	// AWS_SDK_LOAD_CONFIG disabled -> no config files (nil), so callers preserve
	// the SDK's default shared-config-file resolution via WithSharedConfigFiles(nil).
	t.Setenv(envAwsSdkLoadConfig, "false")
	credFiles, cfgFiles = getFallbackSharedConfigFiles(noOpGetUserHomeDir)
	assert.Equal(t, []string{"credentials"}, credFiles)
	assert.Nil(t, cfgFiles)

	t.Setenv(envAwsSdkLoadConfig, "true")
	t.Setenv(envAwsSharedCredentialsFile, "")
	t.Setenv(envAwsSharedConfigFile, "")

	credFiles, cfgFiles = getFallbackSharedConfigFiles(noOpGetUserHomeDir)
	assert.Equal(t, []string{defaultSharedCredentialsFile("home")}, credFiles)
	assert.Equal(t, []string{defaultSharedConfig("home")}, cfgFiles)
}
