// Copyright The OpenTelemetry Authors
// Portions of this file Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package awsutilv2

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetFallbackSharedConfigFiles(t *testing.T) {
	homeProvider := func() string { return "home" }

	t.Run("EnvVarsLoadConfig", func(t *testing.T) {
		t.Setenv(envAwsSdkLoadConfig, "true")
		t.Setenv(envAwsSharedCredentialsFile, "credentials")
		t.Setenv(envAwsSharedConfigFile, "config")

		assert.Equal(t, []string{"config", "credentials"}, getFallbackSharedConfigFiles(homeProvider))
	})

	t.Run("LoadConfigFalse", func(t *testing.T) {
		t.Setenv(envAwsSdkLoadConfig, "false")
		t.Setenv(envAwsSharedCredentialsFile, "credentials")
		t.Setenv(envAwsSharedConfigFile, "config")

		assert.Equal(t, []string{"credentials"}, getFallbackSharedConfigFiles(homeProvider))
	})

	t.Run("EmptyFilePaths", func(t *testing.T) {
		t.Setenv(envAwsSdkLoadConfig, "true")
		t.Setenv(envAwsSharedCredentialsFile, "")
		t.Setenv(envAwsSharedConfigFile, "")

		assert.Equal(t,
			[]string{defaultSharedConfig("home"), defaultSharedCredentialsFile("home")},
			getFallbackSharedConfigFiles(homeProvider))
	})
}
