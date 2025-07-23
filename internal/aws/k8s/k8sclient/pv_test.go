// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package k8sclient

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

var pvObjects = []runtime.Object{
	&corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pv-1",
			UID:  "pv-1-uid",
		},
		Spec: corev1.PersistentVolumeSpec{
			ClaimRef: &corev1.ObjectReference{
				Name:      "pvc-1",
				Namespace: "test-namespace",
			},
		},
	},
	&corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pv-2",
			UID:  "pv2-uid",
		},
		Spec: corev1.PersistentVolumeSpec{
			ClaimRef: &corev1.ObjectReference{
				Name:      "pvc-2",
				Namespace: "another-namespace",
			},
		},
	},
	&corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pv-3",
			UID:  "pv3-uid",
		},
		Spec: corev1.PersistentVolumeSpec{
			ClaimRef: &corev1.ObjectReference{
				Name:      "pvc-3",
				Namespace: "test-namespace",
			},
		},
	},
}

func TestPVClient_TotalPVCount(t *testing.T) {
	setOption := &mockReflectorSyncChecker{}

	fakeClientSet := fake.NewSimpleClientset(pvObjects...)
	client, err := NewPVClient(fakeClientSet, zap.NewNop(), setOption)
	assert.NoError(t, err)

	pvs := make([]any, len(pvObjects))
	for i := range pvObjects {
		pvs[i] = pvObjects[i]
	}
	assert.NoError(t, client.store.Replace(pvs, ""))

	// Set the refreshed flag to true to trigger a refresh in TotalPVCount
	client.store.mu.Lock()
	client.store.refreshed = true
	client.store.mu.Unlock()

	expectedCount := 3
	actualCount := client.TotalCount()
	assert.Equal(t, expectedCount, actualCount)

	client.shutdown()
	assert.True(t, client.stopped)
}

func TestTransformFuncPV(t *testing.T) {
	info, err := transformFuncPV(nil)
	assert.Nil(t, info)
	assert.Error(t, err)

	pv := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pv",
			UID:  "test-pv",
		},
		Spec: corev1.PersistentVolumeSpec{
			ClaimRef: &corev1.ObjectReference{
				Name:      "test-pv",
				Namespace: "test-namespace",
			},
		},
	}
	result, err := transformFuncPV(pv)
	assert.NoError(t, err)
	assert.Equal(t, pv, result)
}

func TestNoOpPVClient(t *testing.T) {
	client := &noOpPVClient{}

	totalCount := client.TotalCount()
	assert.Equal(t, 0, totalCount)

	// Should not panic
	client.shutdown()
}
