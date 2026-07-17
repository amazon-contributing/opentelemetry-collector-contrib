// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awscloudwatchlogsprovisionerextension // import "github.com/open-telemetry/opentelemetry-collector-contrib/extension/awscloudwatchlogsprovisionerextension"

import (
	"context"
	"errors"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"go.uber.org/zap"

	"github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/awsutilv2"
)

type defaultCWLogsClient struct {
	svc *cloudwatchlogs.Client
}

func newDefaultCWLogsClient(ctx context.Context, logger *zap.Logger, settings *awsutilv2.AWSSessionSettings) (cwLogsClient, error) {
	cfg, err := awsutilv2.GetAWSConfig(ctx, logger, settings)
	if err != nil {
		return nil, err
	}
	return &defaultCWLogsClient{svc: cloudwatchlogs.NewFromConfig(cfg)}, nil
}

func (c *defaultCWLogsClient) CreateLogGroup(ctx context.Context, logGroupName string) error {
	_, err := c.svc.CreateLogGroup(ctx, &cloudwatchlogs.CreateLogGroupInput{
		LogGroupName: aws.String(logGroupName),
	})
	// AlreadyExists: group was created before us.
	// OperationAborted: another concurrent create is in flight.
	// Both mean the group exists or will exist momentarily.
	if err != nil && !isAlreadyExists(err) && !isOperationAborted(err) {
		return err
	}
	return nil
}

func (c *defaultCWLogsClient) CreateLogStream(ctx context.Context, logGroupName, logStreamName string) error {
	_, err := c.svc.CreateLogStream(ctx, &cloudwatchlogs.CreateLogStreamInput{
		LogGroupName:  aws.String(logGroupName),
		LogStreamName: aws.String(logStreamName),
	})
	if err != nil && !isAlreadyExists(err) {
		return err
	}
	return nil
}

func isAlreadyExists(err error) bool {
	var alreadyExists *types.ResourceAlreadyExistsException
	return errors.As(err, &alreadyExists)
}

func isOperationAborted(err error) bool {
	var aborted *types.OperationAbortedException
	return errors.As(err, &aborted)
}

func isNotFound(err error) bool {
	var notFound *types.ResourceNotFoundException
	return errors.As(err, &notFound)
}
