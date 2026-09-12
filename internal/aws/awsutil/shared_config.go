// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsutil // import "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/awsutil"

import (
	"os"
	"os/user"
	"path/filepath"
	"strconv"
)

const (
	envAwsSdkLoadConfig = "AWS_SDK_LOAD_CONFIG"
	//nolint:gosec
	envAwsSharedCredentialsFile = "AWS_SHARED_CREDENTIALS_FILE"
	envAwsSharedConfigFile      = "AWS_CONFIG_FILE"
)

// getFallbackSharedConfigFiles follows the same logic as the AWS SDK but takes a getUserHomeDir
// function. It returns the shared-credentials file list and the shared-config file list
// separately: the v2 SDK drops format-mismatched sections, so a config-style "[profile foo]"
// header passed via WithSharedCredentialsFiles is silently ignored (and a credentials-style
// header passed via WithSharedConfigFiles likewise). The shared config file is consulted only
// when AWS_SDK_LOAD_CONFIG is set to a truthy value; otherwise configFiles is a non-nil empty
// list so the SDK does not fall back to loading the default ~/.aws/config.
func getFallbackSharedConfigFiles(userHomeDirProvider func() string) (credentialsFiles, configFiles []string) {
	var sharedCredentialsFile, sharedConfigFile string
	setFromEnvVal(&sharedCredentialsFile, envAwsSharedCredentialsFile)
	if sharedCredentialsFile == "" {
		sharedCredentialsFile = defaultSharedCredentialsFile(userHomeDirProvider())
	}
	credentialsFiles = []string{sharedCredentialsFile}

	// Non-nil empty result when the gate is off: WithSharedConfigFiles treats
	// nil as "not set" and the SDK then loads the default ~/.aws/config.
	configFiles = []string{}
	enableSharedConfig, _ := strconv.ParseBool(os.Getenv(envAwsSdkLoadConfig))
	if enableSharedConfig {
		setFromEnvVal(&sharedConfigFile, envAwsSharedConfigFile)
		if sharedConfigFile == "" {
			sharedConfigFile = defaultSharedConfig(userHomeDirProvider())
		}
		configFiles = []string{sharedConfigFile}
	}
	return credentialsFiles, configFiles
}

func setFromEnvVal(dst *string, keys ...string) {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			*dst = v
			break
		}
	}
}

func defaultSharedCredentialsFile(dir string) string {
	return filepath.Join(dir, ".aws", "credentials")
}

func defaultSharedConfig(dir string) string {
	return filepath.Join(dir, ".aws", "config")
}

// backwardsCompatibleUserHomeDir provides the home directory based on
// environment variables.
//
// Based on v1.44.106 of the AWS SDK.
func backwardsCompatibleUserHomeDir() string {
	home, _ := os.UserHomeDir()
	return home
}

// currentUserHomeDir attempts to use the environment variables before falling
// back on the current user's home directory.
//
// Based on v1.44.332 of the AWS SDK.
func currentUserHomeDir() string {
	var home string

	home = backwardsCompatibleUserHomeDir()
	if home != "" {
		return home
	}

	currUser, _ := user.Current()
	if currUser != nil {
		home = currUser.HomeDir
	}

	return home
}
