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

		credentialsFiles, configFiles := getFallbackSharedConfigFiles(homeProvider)
		assert.Equal(t, []string{"credentials"}, credentialsFiles)
		assert.Equal(t, []string{"config"}, configFiles)
	})

	t.Run("LoadConfigFalse", func(t *testing.T) {
		t.Setenv(envAwsSdkLoadConfig, "false")
		t.Setenv(envAwsSharedCredentialsFile, "credentials")
		t.Setenv(envAwsSharedConfigFile, "config")

		credentialsFiles, configFiles := getFallbackSharedConfigFiles(homeProvider)
		assert.Equal(t, []string{"credentials"}, credentialsFiles)
		assert.Empty(t, configFiles)
	})

	t.Run("EmptyFilePaths", func(t *testing.T) {
		t.Setenv(envAwsSdkLoadConfig, "true")
		t.Setenv(envAwsSharedCredentialsFile, "")
		t.Setenv(envAwsSharedConfigFile, "")

		credentialsFiles, configFiles := getFallbackSharedConfigFiles(homeProvider)
		assert.Equal(t, []string{defaultSharedCredentialsFile("home")}, credentialsFiles)
		assert.Equal(t, []string{defaultSharedConfig("home")}, configFiles)
	})
}
