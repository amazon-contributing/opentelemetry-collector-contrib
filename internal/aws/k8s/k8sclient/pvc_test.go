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

var pvcObjects = []runtime.Object{
	&corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pvc-1",
			Namespace: "test-namespace",
			UID:       "pvc-1-uid",
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{
				corev1.ReadWriteOnce,
			},
		},
		Status: corev1.PersistentVolumeClaimStatus{
			Phase: corev1.ClaimBound,
		},
	},
	&corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pvc-2",
			Namespace: "test-namespace",
			UID:       "pvc-2-uid",
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{
				corev1.ReadWriteOnce,
			},
		},
		Status: corev1.PersistentVolumeClaimStatus{
			Phase: corev1.ClaimBound,
		},
	},
	&corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pvc-3",
			Namespace: "another-namespace",
			UID:       "pvc-3-uid",
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{
				corev1.ReadWriteOnce,
			},
		},
		Status: corev1.PersistentVolumeClaimStatus{
			Phase: corev1.ClaimBound,
		},
	},
}

func TestPVCClient_NamespaceToPVCCount(t *testing.T) {
	setOption := pvcSyncCheckerOption(&mockReflectorSyncChecker{})

	fakeClientSet := fake.NewSimpleClientset(pvcObjects...)
	client, _ := newPVCClient(fakeClientSet, zap.NewNop(), setOption)

	pvcs := make([]any, len(pvcObjects))
	for i := range pvcObjects {
		pvcs[i] = pvcObjects[i]
	}
	assert.NoError(t, client.store.Replace(pvcs, ""))

	expectedMap := map[string]int{
		"test-namespace":    2,
		"another-namespace": 1,
	}
	resultMap := client.NamespaceToPVCCount()
	assert.Equal(t, expectedMap, resultMap)

	client.shutdown()
	assert.True(t, client.stopped)
}

func TestPVCClient_TotalPVCCount(t *testing.T) {
	setOption := pvcSyncCheckerOption(&mockReflectorSyncChecker{})

	fakeClientSet := fake.NewSimpleClientset(pvcObjects...)
	client, err := newPVCClient(fakeClientSet, zap.NewNop(), setOption)
	assert.NoError(t, err)

	pvcs := make([]any, len(pvcObjects))
	for i := range pvcObjects {
		pvcs[i] = pvcObjects[i]
	}
	assert.NoError(t, client.store.Replace(pvcs, ""))

	// Set the refreshed flag to true to trigger a refresh in TotalPVCCount
	client.store.mu.Lock()
	client.store.refreshed = true
	client.store.mu.Unlock()

	expectedCount := 3
	actualCount := client.TotalPVCCount()
	assert.Equal(t, expectedCount, actualCount)

	client.shutdown()
	assert.True(t, client.stopped)
}

func TestTransformFuncPVC(t *testing.T) {
	info, err := transformFuncPVC(nil)
	assert.Nil(t, info)
	assert.Error(t, err)

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pvc",
			Namespace: "test-namespace",
		},
	}
	result, err := transformFuncPVC(pvc)
	assert.NoError(t, err)
	assert.Equal(t, pvc, result)
}

func TestNoOpPVCClient(t *testing.T) {
	client := &noOpPVCClient{}

	namespaceToPVCCount := client.NamespaceToPVCCount()
	assert.Equal(t, map[string]int{}, namespaceToPVCCount)

	totalCount := client.TotalPVCCount()
	assert.Equal(t, 0, totalCount)

	// Should not panic
	client.shutdown()
}
