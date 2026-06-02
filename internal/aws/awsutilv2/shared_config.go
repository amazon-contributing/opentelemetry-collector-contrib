// Copyright The OpenTelemetry Authors
// Portions of this file Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package awsutilv2 // import "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/awsutilv2"

import (
	"os"
	"os/user"
	"path/filepath"
	"strconv"
)

const (
	envAwsSdkLoadConfig         = "AWS_SDK_LOAD_CONFIG"
	envAwsSharedCredentialsFile = "AWS_SHARED_CREDENTIALS_FILE" //nolint:gosec
	envAwsSharedConfigFile      = "AWS_CONFIG_FILE"
)

// getFallbackSharedConfigFiles follows the same logic as the AWS SDK but accepts a userHomeDir
// provider so callers can override how the home directory is discovered.
func getFallbackSharedConfigFiles(userHomeDirProvider func() string) []string {
	home := userHomeDirProvider()
	sharedCredentialsFile := envOr(envAwsSharedCredentialsFile, defaultSharedCredentialsFile(home))
	sharedConfigFile := envOr(envAwsSharedConfigFile, defaultSharedConfig(home))

	var cfgFiles []string
	if enableSharedConfig, _ := strconv.ParseBool(os.Getenv(envAwsSdkLoadConfig)); enableSharedConfig {
		cfgFiles = append(cfgFiles, sharedConfigFile)
	}
	return append(cfgFiles, sharedCredentialsFile)
}

func defaultSharedCredentialsFile(home string) string {
	return filepath.Join(home, ".aws", "credentials")
}

func defaultSharedConfig(home string) string {
	return filepath.Join(home, ".aws", "config")
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// backwardsCompatibleUserHomeDir provides the home directory based on environment variables.
//
// Based on v1.44.106 of the AWS SDK.
func backwardsCompatibleUserHomeDir() string {
	home, _ := os.UserHomeDir()
	return home
}

// currentUserHomeDir attempts to use the environment variables before falling back on the current user's home directory.
//
// Based on v1.44.332 of the AWS SDK.
func currentUserHomeDir() string {
	home := backwardsCompatibleUserHomeDir()
	if len(home) > 0 {
		return home
	}

	currUser, _ := user.Current()
	if currUser != nil {
		home = currUser.HomeDir
	}

	return home
}
