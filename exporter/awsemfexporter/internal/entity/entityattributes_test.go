package entity

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetEntityField(t *testing.T) {
	tests := []struct {
		name      string
		attribute string
		values    []string
		want      string
	}{
		{
			name:      "AttributeEntityType from map",
			attribute: AttributeEntityType,
			values:    nil,
			want:      EntityType,
		},
		{
			name:      "AttributeEntityServiceName from map",
			attribute: AttributeEntityServiceName,
			values:    nil,
			want:      Service,
		},
		{
			name:      "AttributeEntityDeploymentEnvironment from map",
			attribute: AttributeEntityDeploymentEnvironment,
			values:    nil,
			want:      Environment,
		},
		{
			name:      "AttributeEntityK8sNamespaceName from map",
			attribute: AttributeEntityK8sNamespaceName,
			values:    nil,
			want:      K8sNamespace,
		},
		{
			name:      "AttributeEntityK8sWorkloadName from map",
			attribute: AttributeEntityK8sWorkloadName,
			values:    nil,
			want:      K8sWorkload,
		},
		{
			name:      "AttributeEntityK8sNodeName from map",
			attribute: AttributeEntityK8sNodeName,
			values:    nil,
			want:      K8sNode,
		},
		{
			name:      "AttributeEntityPlatformType from map",
			attribute: AttributeEntityPlatformType,
			values:    nil,
			want:      PlatformType,
		},
		{
			name:      "AttributeEntityInstanceID from map",
			attribute: AttributeEntityInstanceID,
			values:    nil,
			want:      InstanceID,
		},
		{
			name:      "AttributeEntityServiceNameSource from map",
			attribute: AttributeEntityServiceNameSource,
			values:    nil,
			want:      AWSServiceNameSource,
		},
		{
			name:      "K8sClusterName with EKSPlatform",
			attribute: AttributeEntityK8sClusterName,
			values:    []string{AttributeEntityEKSPlatform},
			want:      EksCluster,
		},
		{
			name:      "K8sClusterName with K8sPlatform",
			attribute: AttributeEntityK8sClusterName,
			values:    []string{AttributeEntityK8sPlatform},
			want:      K8sCluster,
		},
		{
			name:      "K8sClusterName with unknown platform",
			attribute: AttributeEntityK8sClusterName,
			values:    []string{"unknown"},
			want:      "",
		},
		{
			name:      "Unknown attribute",
			attribute: "unknown",
			values:    nil,
			want:      "",
		},
		{
			name:      "K8sClusterName with no values provided",
			attribute: AttributeEntityK8sClusterName,
			values:    nil,
			want:      "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := GetEntityField(tc.attribute, tc.values...)
			assert.Equalf(t, tc.want, got,
				"GetEntityField(%q, %v) = %q; want %q",
				tc.attribute, tc.values, got, tc.want)
		})
	}
}
