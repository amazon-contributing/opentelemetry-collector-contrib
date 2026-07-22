// Copyright The OpenTelemetry Authors
// Portions of this file Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

// Package awsutilv2 provides a shared aws-sdk-go-v2 credential resolution chain for AWS-related
// components. It is the v2 counterpart of internal/aws/awsutil and is intended for components
// that have migrated to the v2 SDK.
//
// See GetAWSConfig for the entry point and chain semantics.
package awsutilv2 // import "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/awsutilv2"
