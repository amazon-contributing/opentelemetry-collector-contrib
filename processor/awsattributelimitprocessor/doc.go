// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

//go:generate mdatagen metadata.yaml

// Package awsattributelimitprocessor implements an OTel metrics processor that
// enforces the aws backend 150-attribute limit per metric using
// a two-phase approach: unconditional removal of known-redundant attributes,
// followed by priority-based tier dropping.
package awsattributelimitprocessor // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/awsattributelimitprocessor"
