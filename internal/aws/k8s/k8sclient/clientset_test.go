// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package k8sclient

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/fields"
)

func TestGetShutdown(t *testing.T) {
	tmpConfigPath := setKubeConfigPath(t)
	k8sClient := Get(
		zap.NewNop(),
		KubeConfigPath(tmpConfigPath),
		InitSyncPollInterval(10*time.Nanosecond),
		InitSyncPollTimeout(20*time.Nanosecond),
		NodeSelector(fields.OneTermEqualSelector("testField", "testVal")),
		CaptureNodeLevelInfo(true),
	)
	assert.Len(t, optionsToK8sClient, 1)
	assert.NotNil(t, k8sClient.GetClientSet())
	assert.NotNil(t, k8sClient.GetEpClient())
	assert.NotNil(t, k8sClient.GetJobClient())
	assert.NotNil(t, k8sClient.GetNodeClient())
	assert.NotNil(t, k8sClient.GetPodClient())
	assert.NotNil(t, k8sClient.GetReplicaSetClient())
	assert.True(t, k8sClient.captureNodeLevelInfo)
	assert.Equal(t, "testField=testVal", k8sClient.nodeSelector.String())
	k8sClient.Shutdown()
	assert.Nil(t, k8sClient.ep)
	assert.Nil(t, k8sClient.job)
	assert.Nil(t, k8sClient.node)
	assert.Nil(t, k8sClient.pod)
	assert.Nil(t, k8sClient.replicaSet)
	assert.Empty(t, optionsToK8sClient)
	removeTempKubeConfig()
}

// SkipReplicaSetWatch must short-circuit before any informer or clientSet use,
// serving the no-op client whose empty map drives name-based owner parsing.
func TestSkipReplicaSetWatchServesNoOpClient(t *testing.T) {
	c := &K8sClient{skipReplicaSetWatch: true, logger: zap.NewNop()}

	rs := c.GetReplicaSetClient()
	assert.NotNil(t, rs)
	assert.Empty(t, rs.ReplicaSetToDeployment(), "no-op client must return an empty map so callers fall back to name parsing")
	assert.Empty(t, rs.ReplicaSetInfos(), "no-op client must emit no cluster ReplicaSet metrics")

	// The option sets the field...
	kc := &K8sClient{}
	SkipReplicaSetWatch(true).set(kc)
	assert.True(t, kc.skipReplicaSetWatch)

	// ...and keys the singleton distinctly, so leader and per-node clients differ.
	assert.NotEqual(t,
		getStringifiedOptions(SkipReplicaSetWatch(true)),
		getStringifiedOptions(SkipReplicaSetWatch(false)),
	)
}
