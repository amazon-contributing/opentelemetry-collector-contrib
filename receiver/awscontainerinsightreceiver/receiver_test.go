// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awscontainerinsightreceiver

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pmetric"

	ci "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/containerinsight"
)

// Mock cadvisor
type mockCadvisor struct{}

func (*mockCadvisor) GetMetrics() []pmetric.Metrics {
	md := pmetric.NewMetrics()
	return []pmetric.Metrics{md}
}

func (*mockCadvisor) Shutdown() error {
	return nil
}

// Mock k8sapiserver
type mockK8sAPIServer struct{}

func (*mockK8sAPIServer) Shutdown() error {
	return nil
}

func (*mockK8sAPIServer) GetMetrics() []pmetric.Metrics {
	md := pmetric.NewMetrics()
	return []pmetric.Metrics{md}
}

func TestReceiver(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	metricsReceiver, err := newAWSContainerInsightReceiver(
		componenttest.NewNopTelemetrySettings(),
		cfg,
		consumertest.NewNop(),
	)

	require.NoError(t, err)
	require.NotNil(t, metricsReceiver)

	r := metricsReceiver.(*awsContainerInsightReceiver)
	ctx := t.Context()

	err = r.Start(ctx, componenttest.NewNopHost())
	require.Error(t, err)

	err = r.Shutdown(ctx)
	require.NoError(t, err)
}

func TestCollectData(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	metricsReceiver, err := newAWSContainerInsightReceiver(
		componenttest.NewNopTelemetrySettings(),
		cfg,
		new(consumertest.MetricsSink),
	)

	require.NoError(t, err)
	require.NotNil(t, metricsReceiver)

	r := metricsReceiver.(*awsContainerInsightReceiver)
	_ = r.Start(t.Context(), componenttest.NewNopHost())
	ctx := t.Context()
	r.k8sapiserver = &mockK8sAPIServer{}
	r.containerMetricsProvider = &mockCadvisor{}
	err = r.collectData(ctx)
	require.NoError(t, err)

	// test the case when cadvisor and k8sapiserver failed to initialize
	r.containerMetricsProvider = nil
	r.k8sapiserver = nil
	err = r.collectData(ctx)
	require.Error(t, err)
}

func TestCollectDataWithErrConsumer(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	metricsReceiver, err := newAWSContainerInsightReceiver(
		componenttest.NewNopTelemetrySettings(),
		cfg,
		consumertest.NewErr(errors.New("an error")),
	)

	require.NoError(t, err)
	require.NotNil(t, metricsReceiver)

	r := metricsReceiver.(*awsContainerInsightReceiver)
	_ = r.Start(t.Context(), componenttest.NewNopHost())
	r.containerMetricsProvider = &mockCadvisor{}
	r.k8sapiserver = &mockK8sAPIServer{}
	ctx := t.Context()

	err = r.collectData(ctx)
	require.Error(t, err)
}

func TestCollectDataWithECS(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cfg.ContainerOrchestrator = ci.ECS
	metricsReceiver, err := newAWSContainerInsightReceiver(
		componenttest.NewNopTelemetrySettings(),
		cfg,
		new(consumertest.MetricsSink),
	)

	require.NoError(t, err)
	require.NotNil(t, metricsReceiver)

	r := metricsReceiver.(*awsContainerInsightReceiver)
	_ = r.Start(t.Context(), componenttest.NewNopHost())
	ctx := t.Context()

	r.containerMetricsProvider = &mockCadvisor{}
	err = r.collectData(ctx)
	require.NoError(t, err)

	// test the case when cadvisor and k8sapiserver failed to initialize
	r.containerMetricsProvider = nil
	err = r.collectData(ctx)
	require.Error(t, err)
}

func TestCollectDataWithSystemd(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cfg.ContainerOrchestrator = ci.EKS
	cfg.KubeConfigPath = "/tmp/kube-config"
	cfg.HostIP = "1.2.3.4"
	metricsReceiver, err := newAWSContainerInsightReceiver(
		componenttest.NewNopTelemetrySettings(),
		cfg,
		new(consumertest.MetricsSink),
	)

	require.NoError(t, err)
	require.NotNil(t, metricsReceiver)

	r := metricsReceiver.(*awsContainerInsightReceiver)
	_ = r.Start(t.Context(), nil)
	ctx := t.Context()

	r.containerMetricsProvider = &mockCadvisor{}
	err = r.collectData(ctx)
	require.NoError(t, err)
}

// mockHost is a mock implementation of component.Host
type mockHost struct {
	mock.Mock
}

func (m *mockHost) GetExtensions() map[component.ID]component.Component {
	args := m.Called()
	return args.Get(0).(map[component.ID]component.Component)
}

// mockConfigurer is a mock implementation of awsmiddleware.Configurer
type mockConfigurer struct {
	mock.Mock
}

func (m *mockConfigurer) Start(context.Context, component.Host) error {
	return nil
}

func (m *mockConfigurer) Shutdown(context.Context) error {
	return nil
}

func (m *mockHost) GetFactory(_ component.Kind, _ component.Type) component.Factory {
	return nil
}

func TestAWSContainerInsightReceiverStart(t *testing.T) {
	// Create a mock host
	mockHost := new(mockHost)
	testType, _ := component.NewType("awsmiddleware")

	// Create a mock configurer
	mockConfigurer := new(mockConfigurer)
	agenthealth, _ := component.NewType("agenthealth")
	// Set up the mock host to return a map with the mock configurer
	mockHost.On("GetExtensions").Return(map[component.ID]component.Component{
		component.NewID(testType): mockConfigurer,
	})

	statusCodeID := component.NewIDWithName(agenthealth, "statuscode")

	// Create a receiver instance
	config := &Config{
		CollectionInterval:    60,
		ContainerOrchestrator: "eks",
		MiddlewareID:          &statusCodeID,
	}
	consumer := consumertest.NewNop()
	receiver, err := newAWSContainerInsightReceiver(component.TelemetrySettings{}, config, consumer)
	assert.NoError(t, err)
	err = receiver.Start(t.Context(), mockHost)
	assert.Error(t, err)

	mockHost.AssertCalled(t, "GetExtensions")
}
