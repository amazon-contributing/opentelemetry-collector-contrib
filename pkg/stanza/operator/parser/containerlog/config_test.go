// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package containerlog

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component/componenttest"
)

func TestConfigBuild(t *testing.T) {
	cfg := NewConfig()
	set := componenttest.NewNopTelemetrySettings()
	op, err := cfg.Build(set)
	require.NoError(t, err)
	require.IsType(t, &Parser{}, op)
}

func TestConfigBuildWithID(t *testing.T) {
	cfg := NewConfigWithID("my_parser")
	set := componenttest.NewNopTelemetrySettings()
	op, err := cfg.Build(set)
	require.NoError(t, err)
	require.Equal(t, "my_parser", op.(*Parser).ID())
}

func TestConfigBuildInvalidOnError(t *testing.T) {
	cfg := NewConfig()
	cfg.OnError = "invalid_value"
	set := componenttest.NewNopTelemetrySettings()
	_, err := cfg.Build(set)
	require.Error(t, err)
	require.Contains(t, err.Error(), "on_error")
}

func TestConfigDefaultType(t *testing.T) {
	cfg := NewConfig()
	require.Equal(t, operatorType, cfg.Type())
}
