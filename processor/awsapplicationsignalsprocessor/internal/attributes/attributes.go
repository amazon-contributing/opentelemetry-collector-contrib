// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: MIT

package attributes // import "github.com/amazon-contributing/opentelemetry-collector-contrib/processor/awsapplicationsignalsprocessor/internal/attributes"

const (
	// aws attributes
	AWSSpanKind                           = "aws.span.kind"
	AWSLocalService                       = "aws.local.service"
	AWSLocalEnvironment                   = "aws.local.environment"
	AWSLocalOperation                     = "aws.local.operation"
	AWSRemoteService                      = "aws.remote.service"
	AWSRemoteOperation                    = "aws.remote.operation"
	AWSRemoteEnvironment                  = "aws.remote.environment"
	AWSRemoteTarget                       = "aws.remote.target"
	AWSRemoteResourceIdentifier           = "aws.remote.resource.identifier"
	AWSRemoteResourceType                 = "aws.remote.resource.type"
	AWSHostedInEnvironment                = "aws.hostedin.environment"
	AWSRemoteDbUser                       = "aws.remote.db.user"
	AWSRemoteResourceCfnPrimaryIdentifier = "aws.remote.resource.cfn.primary.identifier"

	AWSECSClusterName = "aws.ecs.cluster.name"
	AWSECSTaskID      = "aws.ecs.task.id"

	// resource detection processor attributes
	ResourceDetectionHostID      = "host.id"
	ResourceDetectionHostName    = "host.name"
	ResourceDetectionASG         = "ec2.tag.aws:autoscaling:groupName"
	ResourceDetectionClusterName = "k8s.cluster.name"
)
