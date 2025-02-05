package entity

import (
	"go.opentelemetry.io/collector/pdata/pcommon"
)

const (
	// Entity resource attributes in OTLP payload
	AWSEntityPrefix                      = "com.amazonaws.cloudwatch.entity.internal."
	AttributeEntityType                  = AWSEntityPrefix + "type"
	AttributeEntityServiceName           = AWSEntityPrefix + "service.name"
	AttributeEntityDeploymentEnvironment = AWSEntityPrefix + "deployment.environment"
	AttributeEntityK8sClusterName        = AWSEntityPrefix + "k8s.cluster.name"
	AttributeEntityK8sNamespaceName      = AWSEntityPrefix + "k8s.namespace.name"
	AttributeEntityK8sWorkloadName       = AWSEntityPrefix + "k8s.workload.name"
	AttributeEntityK8sNodeName           = AWSEntityPrefix + "k8s.node.name"
	AttributeEntityServiceNameSource     = AWSEntityPrefix + "service.name.source"
	AttributeEntityPlatformType          = AWSEntityPrefix + "platform.type"
	AttributeEntityInstanceID            = AWSEntityPrefix + "instance.id"

	// Entity fields in EMF log
	Name                 = "Name"
	Environment          = "Environment"
	EntityType           = "Entity.Type"
	EksCluster           = "EKS.Cluster"
	K8sCluster           = "K8s.Cluster"
	K8sNamespace         = "K8s.Namespace"
	K8sWorkload          = "K8s.Workload"
	K8sNode              = "K8s.Node"
	AWSServiceNameSource = "AWS.ServiceNameSource"
	PlatformType         = "PlatformType"
	InstanceID           = "EC2.InstanceId"

	// Possible values for PlatformType
	AttributeEntityEKSPlatform = "AWS::EKS"
	AttributeEntityK8sPlatform = "K8s"
)

// attributeEntityToFieldMap maps attribute entity resource attributes to entity fields
var attributeEntityToFieldMap = map[string]string{
	AttributeEntityType:                  EntityType,
	AttributeEntityServiceName:           Name,
	AttributeEntityDeploymentEnvironment: Environment,
	AttributeEntityK8sNamespaceName:      K8sNamespace,
	AttributeEntityK8sWorkloadName:       K8sWorkload,
	AttributeEntityK8sNodeName:           K8sNode,
	AttributeEntityPlatformType:          PlatformType,
	AttributeEntityInstanceID:            InstanceID,
	AttributeEntityServiceNameSource:     AWSServiceNameSource,
}

func AddEntity(am pcommon.Map) {
	// Check if all resource attributes for entity exist, otherwise don't add entity
	for internalAttr := range attributeEntityToFieldMap {
		if _, found := am.Get(internalAttr); !found {
			return
		}
	}

	if _, found := am.Get(AttributeEntityK8sClusterName); !found {
		return
	}

	// Populate resource attributes with entity fields
	for internalAttr, entityField := range attributeEntityToFieldMap {
		val, _ := am.Get(internalAttr)
		val.CopyTo(am.PutEmpty(entityField))
	}

	val, _ := am.Get(AttributeEntityK8sClusterName)
	switch val.Str() {
	case AttributeEntityEKSPlatform:
		val.CopyTo(am.PutEmpty(EksCluster))
	case AttributeEntityK8sPlatform:
		val.CopyTo(am.PutEmpty(K8sCluster))
	}
}
