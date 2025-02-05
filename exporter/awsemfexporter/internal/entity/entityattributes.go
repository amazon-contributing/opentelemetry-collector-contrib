package entity

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

// GetEntityField returns entity field for provided attribute
func GetEntityField(attribute string, value ...string) string {
	if attribute == AttributeEntityK8sClusterName && len(value) == 1 {
		switch value[0] {
		case AttributeEntityEKSPlatform:
			return EksCluster
		case AttributeEntityK8sPlatform:
			return K8sCluster
		}
	}

	if field, ok := attributeEntityToFieldMap[attribute]; ok {
		return field
	}

	return ""
}
