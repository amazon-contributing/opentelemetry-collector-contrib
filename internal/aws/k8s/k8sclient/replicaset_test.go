// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0
package k8sclient

import (
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
)

var replicaSetArray = []runtime.Object{
	&appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			UID:       "bc5f5839-f62e-44b9-a79e-af250d92dcb1",
			Name:      "cloudwatch-agent-statsd-7f8459d648",
			Namespace: "amazon-cloudwatch",
			OwnerReferences: []metav1.OwnerReference{
				{
					Kind: "Deployment",
					Name: "cloudwatch-agent-statsd",
					UID:  "219887d3-8d2e-11e9-9cbd-064a0c5a2714",
				},
			},
		},
		Spec: appsv1.ReplicaSetSpec{
			Replicas: &desired,
		},
		Status: appsv1.ReplicaSetStatus{
			Replicas:          5,
			AvailableReplicas: 5,
			ReadyReplicas:     5,
		},
	},
	&appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			UID:       "75ab40d2-552a-4c05-82c9-0ddcb3008657",
			Name:      "cloudwatch-agent-statsd-d6487f8459",
			Namespace: "amazon-cloudwatch",
			OwnerReferences: []metav1.OwnerReference{
				{
					Kind: "Deployment",
					Name: "cloudwatch-agent-statsd",
					UID:  "219887d3-8d2e-11e9-9cbd-064a0c5a2714",
				},
			},
		},
		Spec: appsv1.ReplicaSetSpec{
			Replicas: &desired,
		},
		Status: appsv1.ReplicaSetStatus{
			Replicas:          5,
			AvailableReplicas: 5,
			ReadyReplicas:     5,
		},
	},
	&appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test-replicaset-1",
			Namespace:       "amazon-cloudwatch",
			OwnerReferences: []metav1.OwnerReference{},
			UID:             types.UID("test-replicaset-1-uid"),
		},
		Spec: appsv1.ReplicaSetSpec{
			Replicas: &desired,
		},
		Status: appsv1.ReplicaSetStatus{
			Replicas:          5,
			AvailableReplicas: 5,
			ReadyReplicas:     5,
		},
	},
	&appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test-replicaset-2",
			Namespace:       "amazon-cloudwatch",
			OwnerReferences: []metav1.OwnerReference{},
			UID:             types.UID("test-replicaset-12uid"),
		},
		Spec: appsv1.ReplicaSetSpec{
			Replicas: &desired,
		},
		Status: appsv1.ReplicaSetStatus{
			Replicas:          10,
			AvailableReplicas: 10,
			ReadyReplicas:     10,
		},
	},
}

func TestReplicaSetClient_ReplicaSetToDeployment(t *testing.T) {
	setOption := replicaSetSyncCheckerOption(&mockReflectorSyncChecker{})

	fakeClientSet := fake.NewSimpleClientset(replicaSetArray...)
	client, _ := newReplicaSetClient(fakeClientSet, zap.NewNop(), setOption)

	replicaSets := make([]any, len(replicaSetArray))
	for i := range replicaSetArray {
		replicaSets[i] = replicaSetArray[i]
	}
	assert.NoError(t, client.store.Replace(replicaSets, ""))

	expectedMap := map[string]string{
		"cloudwatch-agent-statsd-7f8459d648": "cloudwatch-agent-statsd",
		"cloudwatch-agent-statsd-d6487f8459": "cloudwatch-agent-statsd",
	}
	client.cachedReplicaSetMap = map[string]time.Time{
		"cloudwatch-agent-statsd-7f8459d648": time.Now().Add(-24 * time.Hour),
		"cloudwatch-agent-statsd-d6487f8459": time.Now().Add(-24 * time.Hour),
	}
	resultMap := client.ReplicaSetToDeployment()
	assert.Equal(t, expectedMap, resultMap)
	client.shutdown()
	assert.True(t, client.stopped)
}

func TestReplicaSetClient(t *testing.T) {
	setOption := replicaSetSyncCheckerOption(&mockReflectorSyncChecker{})

	fakeClientSet := fake.NewSimpleClientset(replicaSetArray...)
	client, _ := newReplicaSetClient(fakeClientSet, zap.NewNop(), setOption)

	replicaSets := make([]any, len(replicaSetArray))
	for i := range replicaSetArray {
		replicaSets[i] = replicaSetArray[i]
	}
	assert.NoError(t, client.store.Replace(replicaSets, ""))

	expected := []*ReplicaSetInfo{
		{
			Name:      "test-replicaset-1",
			Namespace: "amazon-cloudwatch",
			Owners:    []*ReplicaSetOwner{},
			Spec: &ReplicaSetSpec{
				Replicas: 20,
			},
			Status: &ReplicaSetStatus{
				Replicas:          5,
				AvailableReplicas: 5,
				ReadyReplicas:     5,
			},
		},
		{
			Name:      "test-replicaset-2",
			Namespace: "amazon-cloudwatch",
			Owners:    []*ReplicaSetOwner{},
			Spec: &ReplicaSetSpec{
				Replicas: 20,
			},
			Status: &ReplicaSetStatus{
				Replicas:          10,
				AvailableReplicas: 10,
				ReadyReplicas:     10,
			},
		},
	}
	actual := client.ReplicaSetInfos()
	sort.Slice(actual, func(i, j int) bool {
		return actual[i].Name < actual[j].Name
	})
	assert.Equal(t, expected, actual)
	client.shutdown()
	assert.True(t, client.stopped)
}

func TestTransformFuncReplicaSet(t *testing.T) {
	info, err := transformFuncReplicaSet(nil)
	assert.Nil(t, info)
	assert.Error(t, err)
}
