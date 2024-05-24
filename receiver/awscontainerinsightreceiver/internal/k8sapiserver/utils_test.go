// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package k8sapiserver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/pmetric"
	v1 "k8s.io/api/core/v1"

	"github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/k8s/k8sclient"
)

func TestUtils_parseDeploymentFromReplicaSet(t *testing.T) {
	assert.Equal(t, "", parseDeploymentFromReplicaSet("cloudwatch-agent"))
	assert.Equal(t, "cloudwatch-agent", parseDeploymentFromReplicaSet("cloudwatch-agent-42kcz"))
}

func TestUtils_parseCronJobFromJob(t *testing.T) {
	assert.Equal(t, "", parseCronJobFromJob("hello-123"))
	assert.Equal(t, "hello", parseCronJobFromJob("hello-1234567890"))
	assert.Equal(t, "", parseCronJobFromJob("hello-123456789a"))
}

func TestPodStore_addPodStatusMetrics(t *testing.T) {
	fields := map[string]any{}
	testPodInfo := k8sclient.PodInfo{
		Name:      "kube-proxy-csm88",
		Namespace: "kube-system",
		UID:       "bc5f5839-f62e-44b9-a79e-af250d92dcb1",
		Labels:    map[string]string{},
		Phase:     v1.PodRunning,
	}
	addPodStatusMetrics(fields, &testPodInfo)

	expectedFieldsArray := map[string]any{
		"pod_status_pending":   0,
		"pod_status_running":   1,
		"pod_status_succeeded": 0,
		"pod_status_failed":    0,
	}
	assert.Equal(t, expectedFieldsArray, fields)
}

func TestPodStore_addPodConditionMetrics(t *testing.T) {
	fields := map[string]any{}
	testPodInfo := k8sclient.PodInfo{
		Name:      "kube-proxy-csm88",
		Namespace: "kube-system",
		UID:       "bc5f5839-f62e-44b9-a79e-af250d92dcb1",
		Labels:    map[string]string{},
		Phase:     v1.PodRunning,
	}
	addPodConditionMetrics(fields, &testPodInfo)

	expectedFieldsArray := map[string]any{
		"pod_status_ready":     0,
		"pod_status_scheduled": 0,
		"pod_status_unknown":   0,
	}
	assert.Equal(t, expectedFieldsArray, fields)
}

func TestUtils_copyResourceAttributes(t *testing.T) {
	ms := pmetric.NewMetrics()
	rm := ms.ResourceMetrics().AppendEmpty()
	rattrs := rm.Resource().Attributes()
	rattrs.PutStr("key1", "value1")
	rattrs.PutStr("key2", "value2")

	// 1 datapoint with no existing attributes
	ilms := rm.ScopeMetrics().AppendEmpty()
	oneDp := ilms.Metrics().AppendEmpty()
	oneDp.SetName("test_metric1")
	oneDp.SetEmptyGauge().DataPoints().AppendEmpty().SetIntValue(1)
	// 2 dps including 1 with attributes and none with the other
	twoDp := ilms.Metrics().AppendEmpty()
	twoDp.SetName("test_metric2")
	gauge := twoDp.SetEmptyGauge()
	gauge.DataPoints().AppendEmpty().SetIntValue(2)
	dp2 := gauge.DataPoints().AppendEmpty()
	dp2.SetIntValue(2)
	dp2.Attributes().PutStr("del_key", "del_val")

	copyResourceAttributes(ms)

	resIlms := ms.ResourceMetrics().At(0).ScopeMetrics().At(0)
	res1 := resIlms.Metrics().At(0).Gauge().DataPoints()
	assert.Equal(t, rattrs.Len(), res1.At(0).Attributes().Len())
	assert.Equal(t, rattrs.AsRaw(), res1.At(0).Attributes().AsRaw())
	res2 := resIlms.Metrics().At(1).Gauge().DataPoints()
	assert.Equal(t, rattrs.Len(), res2.At(0).Attributes().Len())
	assert.Equal(t, rattrs.AsRaw(), res2.At(0).Attributes().AsRaw())
	assert.Equal(t, rattrs.Len(), res2.At(1).Attributes().Len())
	assert.Equal(t, rattrs.AsRaw(), res2.At(1).Attributes().AsRaw())
}
