// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awscontainerinsightreceiver

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/confmap/confmaptest"

	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/metadata"
)

func TestLoadConfig(t *testing.T) {
	t.Parallel()

	cm, err := confmaptest.LoadConf(filepath.Join("testdata", "config.yaml"))
	require.NoError(t, err)

	tests := []struct {
		id       component.ID
		expected component.Config
	}{
		{
			id:       component.NewIDWithName(metadata.Type, ""),
			expected: createDefaultConfig(),
		},
		{
			id: component.NewIDWithName(metadata.Type, "collection_interval_settings"),
			expected: &Config{
				CollectionInterval:        60 * time.Second,
				ContainerOrchestrator:     "eks",
				TagService:                true,
				PrefFullPodName:           false,
				LeaderLockName:            "otel-container-insight-clusterleader",
				EnableControlPlaneMetrics: false,
			},
		},
		{
			id: component.NewIDWithName(metadata.Type, "cluster_name"),
			expected: &Config{
				CollectionInterval:        60 * time.Second,
				ContainerOrchestrator:     "eks",
				TagService:                true,
				PrefFullPodName:           false,
				ClusterName:               "override_cluster",
				LeaderLockName:            "otel-container-insight-clusterleader",
				EnableControlPlaneMetrics: false,
			},
		},
		{
			id: component.NewIDWithName(metadata.Type, "leader_lock_name"),
			expected: &Config{
				CollectionInterval:        60 * time.Second,
				ContainerOrchestrator:     "eks",
				TagService:                true,
				PrefFullPodName:           false,
				LeaderLockName:            "override-container-insight-clusterleader",
				EnableControlPlaneMetrics: false,
			},
		},
		{
			id: component.NewIDWithName(metadata.Type, "leader_lock_using_config_map_only"),
			expected: &Config{
				CollectionInterval:           60 * time.Second,
				ContainerOrchestrator:        "eks",
				TagService:                   true,
				PrefFullPodName:              false,
				LeaderLockName:               "otel-container-insight-clusterleader",
				LeaderLockUsingConfigMapOnly: true,
				EnableControlPlaneMetrics:    false,
			},
		},
		{
			id: component.NewIDWithName(metadata.Type, "enable_control_plane_metrics"),
			expected: &Config{
				CollectionInterval:        60 * time.Second,
				ContainerOrchestrator:     "eks",
				TagService:                true,
				PrefFullPodName:           false,
				LeaderLockName:            "otel-container-insight-clusterleader",
				EnableControlPlaneMetrics: true,
			},
		},
		{
			id: component.NewIDWithName(metadata.Type, "custom_kube_config_path"),
			expected: &Config{
				CollectionInterval:    60 * time.Second,
				ContainerOrchestrator: "eks",
				TagService:            true,
				PrefFullPodName:       false,
				LeaderLockName:        "otel-container-insight-clusterleader",
				KubeConfigPath:        "custom_kube_config_path",
				HostIP:                "1.2.3.4",
				HostName:              "test-hostname",
				RunOnSystemd:          true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.id.String(), func(t *testing.T) {
			factory := NewFactory()
			cfg := factory.CreateDefaultConfig()

			sub, err := cm.Sub(tt.id.String())
			require.NoError(t, err)
			require.NoError(t, sub.Unmarshal(cfg))

			assert.NoError(t, component.ValidateConfig(cfg))
			assert.Equal(t, tt.expected, cfg)
		})
	}
}
