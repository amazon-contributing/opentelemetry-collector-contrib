// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awscloudwatchlogsprovisionerextension // import "github.com/open-telemetry/opentelemetry-collector-contrib/extension/awscloudwatchlogsprovisionerextension"

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"go.uber.org/zap"

	"github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/awsutilv2"
)

type defaultCWLogsClient struct {
	svc *cloudwatchlogs.Client
	// Set when DescribeLogGroups rejects the identifiers filter. Once set,
	// all calls go directly to the prefix-based fallback.
	describeByPrefix atomic.Bool
}

func newDefaultCWLogsClient(ctx context.Context, logger *zap.Logger, settings *awsutilv2.AWSSessionSettings) (cwLogsClient, error) {
	cfg, err := awsutilv2.GetAWSConfig(ctx, logger, settings)
	if err != nil {
		return nil, err
	}
	return &defaultCWLogsClient{svc: cloudwatchlogs.NewFromConfig(cfg)}, nil
}

func (c *defaultCWLogsClient) CreateLogGroup(ctx context.Context, logGroupName string, logGroupClass types.LogGroupClass) error {
	// The SDK omits LogGroupClass from the request when empty.
	_, err := c.svc.CreateLogGroup(ctx, &cloudwatchlogs.CreateLogGroupInput{
		LogGroupName:  aws.String(logGroupName),
		LogGroupClass: logGroupClass,
	})
	if err != nil && !isAlreadyExists(err) {
		return err
	}
	return nil
}

func (c *defaultCWLogsClient) PutRetentionPolicy(ctx context.Context, logGroupName string, retentionInDays int32) error {
	_, err := c.svc.PutRetentionPolicy(ctx, &cloudwatchlogs.PutRetentionPolicyInput{
		LogGroupName:    aws.String(logGroupName),
		RetentionInDays: &retentionInDays,
	})
	return err
}

func (c *defaultCWLogsClient) DescribeLogGroupsRetention(ctx context.Context, logGroupNames []string) (map[string]int32, error) {
	if c.describeByPrefix.Load() {
		return c.describeLogGroupsRetentionByPrefix(ctx, logGroupNames)
	}

	resp, err := c.svc.DescribeLogGroups(ctx, &cloudwatchlogs.DescribeLogGroupsInput{
		LogGroupIdentifiers: logGroupNames,
		Limit:               aws.Int32(50),
	})
	if err != nil {
		if isIdentifiersNotSupported(err) {
			c.describeByPrefix.Store(true)
			return c.describeLogGroupsRetentionByPrefix(ctx, logGroupNames)
		}
		return nil, err
	}

	return extractRetention(resp.LogGroups), nil
}

// isIdentifiersNotSupported reports whether DescribeLogGroups rejected the
// LogGroupIdentifiers filter. Matched loosely to survive message rewording.
func isIdentifiersNotSupported(err error) bool {
	var ipErr *types.InvalidParameterException
	return errors.As(err, &ipErr) &&
		strings.Contains(strings.ToLower(aws.ToString(ipErr.Message)), "identifiers")
}

func (c *defaultCWLogsClient) describeLogGroupsRetentionByPrefix(ctx context.Context, logGroupNames []string) (map[string]int32, error) {
	result := make(map[string]int32, len(logGroupNames))
	for _, name := range logGroupNames {
		resp, err := c.svc.DescribeLogGroups(ctx, &cloudwatchlogs.DescribeLogGroupsInput{
			LogGroupNamePrefix: aws.String(name),
			Limit:              aws.Int32(50),
		})
		if err != nil {
			return nil, err
		}
		for _, group := range resp.LogGroups {
			if aws.ToString(group.LogGroupName) == name {
				result[name] = aws.ToInt32(group.RetentionInDays)
				break
			}
		}
	}
	return result, nil
}

func extractRetention(logGroups []types.LogGroup) map[string]int32 {
	result := make(map[string]int32, len(logGroups))
	for _, group := range logGroups {
		result[aws.ToString(group.LogGroupName)] = aws.ToInt32(group.RetentionInDays)
	}
	return result
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
