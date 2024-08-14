// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package k8sclient // import "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/k8s/k8sclient"

import (
	v1 "k8s.io/api/core/v1"
)

type Label int8

const (
	SageMakerNodeHealthStatus Label = iota
)

func (ct Label) String() string {
	return [...]string{"sagemaker.amazonaws.com/node-health-status"}[ct]
}

type nodeInfo struct {
	name         string
	conditions   []*NodeCondition
	capacity     v1.ResourceList
	allocatable  v1.ResourceList
	providerID   string
	instanceType string
	labels       map[Label]int8
}

type NodeCondition struct {
	Type   v1.NodeConditionType
	Status v1.ConditionStatus
}
