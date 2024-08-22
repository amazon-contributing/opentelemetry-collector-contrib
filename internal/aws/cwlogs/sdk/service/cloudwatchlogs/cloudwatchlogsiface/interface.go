// Code generated by private/model/cli/gen-api/main.go. DO NOT EDIT.

// Package cloudwatchlogsiface provides an interface to enable mocking the Amazon CloudWatch Logs service client
// for testing your code.
//
// It is important to note that this interface will have breaking changes
// when the service model is updated and adds new API operations, paginators,
// and waiters.
package cloudwatchlogsiface

import (
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/request"

	"github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/cwlogs/sdk/service/cloudwatchlogs"
)


// CloudWatchLogsAPI provides an interface to enable mocking the
// cloudwatchlogs.CloudWatchLogs service client's API operation,
// paginators, and waiters. This make unit testing your code that calls out
// to the SDK's service client's calls easier.
//
// The best way to use this interface is so the SDK's service client's calls
// can be stubbed out for unit testing your code with the SDK without needing
// to inject custom request handlers into the SDK's request pipeline.
//
//	// myFunc uses an SDK service client to make a request to
//	// Amazon CloudWatch Logs.
//	func myFunc(svc cloudwatchlogsiface.CloudWatchLogsAPI) bool {
//	    // Make svc.AssociateKmsKey request
//	}
//
//	func main() {
//	    sess := session.New()
//	    svc := cloudwatchlogs.New(sess)
//
//	    myFunc(svc)
//	}
//
// In your _test.go file:
//
//	// Define a mock struct to be used in your unit tests of myFunc.
//	type mockCloudWatchLogsClient struct {
//	    cloudwatchlogsiface.CloudWatchLogsAPI
//	}
//	func (m *mockCloudWatchLogsClient) AssociateKmsKey(input *cloudwatchlogs.AssociateKmsKeyInput) (*cloudwatchlogs.AssociateKmsKeyOutput, error) {
//	    // mock response/functionality
//	}
//
//	func TestMyFunc(t *testing.T) {
//	    // Setup Test
//	    mockSvc := &mockCloudWatchLogsClient{}
//
//	    myfunc(mockSvc)
//
//	    // Verify myFunc's functionality
//	}
//
// It is important to note that this interface will have breaking changes
// when the service model is updated and adds new API operations, paginators,
// and waiters. Its suggested to use the pattern above for testing, or using
// tooling to generate mocks to satisfy the interfaces.
type CloudWatchLogsAPI interface {
	AssociateKmsKey(*cloudwatchlogs.AssociateKmsKeyInput) (*cloudwatchlogs.AssociateKmsKeyOutput, error)
	AssociateKmsKeyWithContext(aws.Context, *cloudwatchlogs.AssociateKmsKeyInput, ...request.Option) (*cloudwatchlogs.AssociateKmsKeyOutput, error)
	AssociateKmsKeyRequest(*cloudwatchlogs.AssociateKmsKeyInput) (*request.Request, *cloudwatchlogs.AssociateKmsKeyOutput)

	CancelExportTask(*cloudwatchlogs.CancelExportTaskInput) (*cloudwatchlogs.CancelExportTaskOutput, error)
	CancelExportTaskWithContext(aws.Context, *cloudwatchlogs.CancelExportTaskInput, ...request.Option) (*cloudwatchlogs.CancelExportTaskOutput, error)
	CancelExportTaskRequest(*cloudwatchlogs.CancelExportTaskInput) (*request.Request, *cloudwatchlogs.CancelExportTaskOutput)

	CreateDelivery(*cloudwatchlogs.CreateDeliveryInput) (*cloudwatchlogs.CreateDeliveryOutput, error)
	CreateDeliveryWithContext(aws.Context, *cloudwatchlogs.CreateDeliveryInput, ...request.Option) (*cloudwatchlogs.CreateDeliveryOutput, error)
	CreateDeliveryRequest(*cloudwatchlogs.CreateDeliveryInput) (*request.Request, *cloudwatchlogs.CreateDeliveryOutput)

	CreateExportTask(*cloudwatchlogs.CreateExportTaskInput) (*cloudwatchlogs.CreateExportTaskOutput, error)
	CreateExportTaskWithContext(aws.Context, *cloudwatchlogs.CreateExportTaskInput, ...request.Option) (*cloudwatchlogs.CreateExportTaskOutput, error)
	CreateExportTaskRequest(*cloudwatchlogs.CreateExportTaskInput) (*request.Request, *cloudwatchlogs.CreateExportTaskOutput)

	CreateLogAnomalyDetector(*cloudwatchlogs.CreateLogAnomalyDetectorInput) (*cloudwatchlogs.CreateLogAnomalyDetectorOutput, error)
	CreateLogAnomalyDetectorWithContext(aws.Context, *cloudwatchlogs.CreateLogAnomalyDetectorInput, ...request.Option) (*cloudwatchlogs.CreateLogAnomalyDetectorOutput, error)
	CreateLogAnomalyDetectorRequest(*cloudwatchlogs.CreateLogAnomalyDetectorInput) (*request.Request, *cloudwatchlogs.CreateLogAnomalyDetectorOutput)

	CreateLogGroup(*cloudwatchlogs.CreateLogGroupInput) (*cloudwatchlogs.CreateLogGroupOutput, error)
	CreateLogGroupWithContext(aws.Context, *cloudwatchlogs.CreateLogGroupInput, ...request.Option) (*cloudwatchlogs.CreateLogGroupOutput, error)
	CreateLogGroupRequest(*cloudwatchlogs.CreateLogGroupInput) (*request.Request, *cloudwatchlogs.CreateLogGroupOutput)

	CreateLogStream(*cloudwatchlogs.CreateLogStreamInput) (*cloudwatchlogs.CreateLogStreamOutput, error)
	CreateLogStreamWithContext(aws.Context, *cloudwatchlogs.CreateLogStreamInput, ...request.Option) (*cloudwatchlogs.CreateLogStreamOutput, error)
	CreateLogStreamRequest(*cloudwatchlogs.CreateLogStreamInput) (*request.Request, *cloudwatchlogs.CreateLogStreamOutput)

	DeleteAccountPolicy(*cloudwatchlogs.DeleteAccountPolicyInput) (*cloudwatchlogs.DeleteAccountPolicyOutput, error)
	DeleteAccountPolicyWithContext(aws.Context, *cloudwatchlogs.DeleteAccountPolicyInput, ...request.Option) (*cloudwatchlogs.DeleteAccountPolicyOutput, error)
	DeleteAccountPolicyRequest(*cloudwatchlogs.DeleteAccountPolicyInput) (*request.Request, *cloudwatchlogs.DeleteAccountPolicyOutput)

	DeleteDataProtectionPolicy(*cloudwatchlogs.DeleteDataProtectionPolicyInput) (*cloudwatchlogs.DeleteDataProtectionPolicyOutput, error)
	DeleteDataProtectionPolicyWithContext(aws.Context, *cloudwatchlogs.DeleteDataProtectionPolicyInput, ...request.Option) (*cloudwatchlogs.DeleteDataProtectionPolicyOutput, error)
	DeleteDataProtectionPolicyRequest(*cloudwatchlogs.DeleteDataProtectionPolicyInput) (*request.Request, *cloudwatchlogs.DeleteDataProtectionPolicyOutput)

	DeleteDelivery(*cloudwatchlogs.DeleteDeliveryInput) (*cloudwatchlogs.DeleteDeliveryOutput, error)
	DeleteDeliveryWithContext(aws.Context, *cloudwatchlogs.DeleteDeliveryInput, ...request.Option) (*cloudwatchlogs.DeleteDeliveryOutput, error)
	DeleteDeliveryRequest(*cloudwatchlogs.DeleteDeliveryInput) (*request.Request, *cloudwatchlogs.DeleteDeliveryOutput)

	DeleteDeliveryDestination(*cloudwatchlogs.DeleteDeliveryDestinationInput) (*cloudwatchlogs.DeleteDeliveryDestinationOutput, error)
	DeleteDeliveryDestinationWithContext(aws.Context, *cloudwatchlogs.DeleteDeliveryDestinationInput, ...request.Option) (*cloudwatchlogs.DeleteDeliveryDestinationOutput, error)
	DeleteDeliveryDestinationRequest(*cloudwatchlogs.DeleteDeliveryDestinationInput) (*request.Request, *cloudwatchlogs.DeleteDeliveryDestinationOutput)

	DeleteDeliveryDestinationPolicy(*cloudwatchlogs.DeleteDeliveryDestinationPolicyInput) (*cloudwatchlogs.DeleteDeliveryDestinationPolicyOutput, error)
	DeleteDeliveryDestinationPolicyWithContext(aws.Context, *cloudwatchlogs.DeleteDeliveryDestinationPolicyInput, ...request.Option) (*cloudwatchlogs.DeleteDeliveryDestinationPolicyOutput, error)
	DeleteDeliveryDestinationPolicyRequest(*cloudwatchlogs.DeleteDeliveryDestinationPolicyInput) (*request.Request, *cloudwatchlogs.DeleteDeliveryDestinationPolicyOutput)

	DeleteDeliverySource(*cloudwatchlogs.DeleteDeliverySourceInput) (*cloudwatchlogs.DeleteDeliverySourceOutput, error)
	DeleteDeliverySourceWithContext(aws.Context, *cloudwatchlogs.DeleteDeliverySourceInput, ...request.Option) (*cloudwatchlogs.DeleteDeliverySourceOutput, error)
	DeleteDeliverySourceRequest(*cloudwatchlogs.DeleteDeliverySourceInput) (*request.Request, *cloudwatchlogs.DeleteDeliverySourceOutput)

	DeleteDestination(*cloudwatchlogs.DeleteDestinationInput) (*cloudwatchlogs.DeleteDestinationOutput, error)
	DeleteDestinationWithContext(aws.Context, *cloudwatchlogs.DeleteDestinationInput, ...request.Option) (*cloudwatchlogs.DeleteDestinationOutput, error)
	DeleteDestinationRequest(*cloudwatchlogs.DeleteDestinationInput) (*request.Request, *cloudwatchlogs.DeleteDestinationOutput)

	DeleteLogAnomalyDetector(*cloudwatchlogs.DeleteLogAnomalyDetectorInput) (*cloudwatchlogs.DeleteLogAnomalyDetectorOutput, error)
	DeleteLogAnomalyDetectorWithContext(aws.Context, *cloudwatchlogs.DeleteLogAnomalyDetectorInput, ...request.Option) (*cloudwatchlogs.DeleteLogAnomalyDetectorOutput, error)
	DeleteLogAnomalyDetectorRequest(*cloudwatchlogs.DeleteLogAnomalyDetectorInput) (*request.Request, *cloudwatchlogs.DeleteLogAnomalyDetectorOutput)

	DeleteLogGroup(*cloudwatchlogs.DeleteLogGroupInput) (*cloudwatchlogs.DeleteLogGroupOutput, error)
	DeleteLogGroupWithContext(aws.Context, *cloudwatchlogs.DeleteLogGroupInput, ...request.Option) (*cloudwatchlogs.DeleteLogGroupOutput, error)
	DeleteLogGroupRequest(*cloudwatchlogs.DeleteLogGroupInput) (*request.Request, *cloudwatchlogs.DeleteLogGroupOutput)

	DeleteLogStream(*cloudwatchlogs.DeleteLogStreamInput) (*cloudwatchlogs.DeleteLogStreamOutput, error)
	DeleteLogStreamWithContext(aws.Context, *cloudwatchlogs.DeleteLogStreamInput, ...request.Option) (*cloudwatchlogs.DeleteLogStreamOutput, error)
	DeleteLogStreamRequest(*cloudwatchlogs.DeleteLogStreamInput) (*request.Request, *cloudwatchlogs.DeleteLogStreamOutput)

	DeleteMetricFilter(*cloudwatchlogs.DeleteMetricFilterInput) (*cloudwatchlogs.DeleteMetricFilterOutput, error)
	DeleteMetricFilterWithContext(aws.Context, *cloudwatchlogs.DeleteMetricFilterInput, ...request.Option) (*cloudwatchlogs.DeleteMetricFilterOutput, error)
	DeleteMetricFilterRequest(*cloudwatchlogs.DeleteMetricFilterInput) (*request.Request, *cloudwatchlogs.DeleteMetricFilterOutput)

	DeleteQueryDefinition(*cloudwatchlogs.DeleteQueryDefinitionInput) (*cloudwatchlogs.DeleteQueryDefinitionOutput, error)
	DeleteQueryDefinitionWithContext(aws.Context, *cloudwatchlogs.DeleteQueryDefinitionInput, ...request.Option) (*cloudwatchlogs.DeleteQueryDefinitionOutput, error)
	DeleteQueryDefinitionRequest(*cloudwatchlogs.DeleteQueryDefinitionInput) (*request.Request, *cloudwatchlogs.DeleteQueryDefinitionOutput)

	DeleteResourcePolicy(*cloudwatchlogs.DeleteResourcePolicyInput) (*cloudwatchlogs.DeleteResourcePolicyOutput, error)
	DeleteResourcePolicyWithContext(aws.Context, *cloudwatchlogs.DeleteResourcePolicyInput, ...request.Option) (*cloudwatchlogs.DeleteResourcePolicyOutput, error)
	DeleteResourcePolicyRequest(*cloudwatchlogs.DeleteResourcePolicyInput) (*request.Request, *cloudwatchlogs.DeleteResourcePolicyOutput)

	DeleteRetentionPolicy(*cloudwatchlogs.DeleteRetentionPolicyInput) (*cloudwatchlogs.DeleteRetentionPolicyOutput, error)
	DeleteRetentionPolicyWithContext(aws.Context, *cloudwatchlogs.DeleteRetentionPolicyInput, ...request.Option) (*cloudwatchlogs.DeleteRetentionPolicyOutput, error)
	DeleteRetentionPolicyRequest(*cloudwatchlogs.DeleteRetentionPolicyInput) (*request.Request, *cloudwatchlogs.DeleteRetentionPolicyOutput)

	DeleteSubscriptionFilter(*cloudwatchlogs.DeleteSubscriptionFilterInput) (*cloudwatchlogs.DeleteSubscriptionFilterOutput, error)
	DeleteSubscriptionFilterWithContext(aws.Context, *cloudwatchlogs.DeleteSubscriptionFilterInput, ...request.Option) (*cloudwatchlogs.DeleteSubscriptionFilterOutput, error)
	DeleteSubscriptionFilterRequest(*cloudwatchlogs.DeleteSubscriptionFilterInput) (*request.Request, *cloudwatchlogs.DeleteSubscriptionFilterOutput)

	DescribeAccountPolicies(*cloudwatchlogs.DescribeAccountPoliciesInput) (*cloudwatchlogs.DescribeAccountPoliciesOutput, error)
	DescribeAccountPoliciesWithContext(aws.Context, *cloudwatchlogs.DescribeAccountPoliciesInput, ...request.Option) (*cloudwatchlogs.DescribeAccountPoliciesOutput, error)
	DescribeAccountPoliciesRequest(*cloudwatchlogs.DescribeAccountPoliciesInput) (*request.Request, *cloudwatchlogs.DescribeAccountPoliciesOutput)

	DescribeDeliveries(*cloudwatchlogs.DescribeDeliveriesInput) (*cloudwatchlogs.DescribeDeliveriesOutput, error)
	DescribeDeliveriesWithContext(aws.Context, *cloudwatchlogs.DescribeDeliveriesInput, ...request.Option) (*cloudwatchlogs.DescribeDeliveriesOutput, error)
	DescribeDeliveriesRequest(*cloudwatchlogs.DescribeDeliveriesInput) (*request.Request, *cloudwatchlogs.DescribeDeliveriesOutput)

	DescribeDeliveriesPages(*cloudwatchlogs.DescribeDeliveriesInput, func(*cloudwatchlogs.DescribeDeliveriesOutput, bool) bool) error
	DescribeDeliveriesPagesWithContext(aws.Context, *cloudwatchlogs.DescribeDeliveriesInput, func(*cloudwatchlogs.DescribeDeliveriesOutput, bool) bool, ...request.Option) error

	DescribeDeliveryDestinations(*cloudwatchlogs.DescribeDeliveryDestinationsInput) (*cloudwatchlogs.DescribeDeliveryDestinationsOutput, error)
	DescribeDeliveryDestinationsWithContext(aws.Context, *cloudwatchlogs.DescribeDeliveryDestinationsInput, ...request.Option) (*cloudwatchlogs.DescribeDeliveryDestinationsOutput, error)
	DescribeDeliveryDestinationsRequest(*cloudwatchlogs.DescribeDeliveryDestinationsInput) (*request.Request, *cloudwatchlogs.DescribeDeliveryDestinationsOutput)

	DescribeDeliveryDestinationsPages(*cloudwatchlogs.DescribeDeliveryDestinationsInput, func(*cloudwatchlogs.DescribeDeliveryDestinationsOutput, bool) bool) error
	DescribeDeliveryDestinationsPagesWithContext(aws.Context, *cloudwatchlogs.DescribeDeliveryDestinationsInput, func(*cloudwatchlogs.DescribeDeliveryDestinationsOutput, bool) bool, ...request.Option) error

	DescribeDeliverySources(*cloudwatchlogs.DescribeDeliverySourcesInput) (*cloudwatchlogs.DescribeDeliverySourcesOutput, error)
	DescribeDeliverySourcesWithContext(aws.Context, *cloudwatchlogs.DescribeDeliverySourcesInput, ...request.Option) (*cloudwatchlogs.DescribeDeliverySourcesOutput, error)
	DescribeDeliverySourcesRequest(*cloudwatchlogs.DescribeDeliverySourcesInput) (*request.Request, *cloudwatchlogs.DescribeDeliverySourcesOutput)

	DescribeDeliverySourcesPages(*cloudwatchlogs.DescribeDeliverySourcesInput, func(*cloudwatchlogs.DescribeDeliverySourcesOutput, bool) bool) error
	DescribeDeliverySourcesPagesWithContext(aws.Context, *cloudwatchlogs.DescribeDeliverySourcesInput, func(*cloudwatchlogs.DescribeDeliverySourcesOutput, bool) bool, ...request.Option) error

	DescribeDestinations(*cloudwatchlogs.DescribeDestinationsInput) (*cloudwatchlogs.DescribeDestinationsOutput, error)
	DescribeDestinationsWithContext(aws.Context, *cloudwatchlogs.DescribeDestinationsInput, ...request.Option) (*cloudwatchlogs.DescribeDestinationsOutput, error)
	DescribeDestinationsRequest(*cloudwatchlogs.DescribeDestinationsInput) (*request.Request, *cloudwatchlogs.DescribeDestinationsOutput)

	DescribeDestinationsPages(*cloudwatchlogs.DescribeDestinationsInput, func(*cloudwatchlogs.DescribeDestinationsOutput, bool) bool) error
	DescribeDestinationsPagesWithContext(aws.Context, *cloudwatchlogs.DescribeDestinationsInput, func(*cloudwatchlogs.DescribeDestinationsOutput, bool) bool, ...request.Option) error

	DescribeExportTasks(*cloudwatchlogs.DescribeExportTasksInput) (*cloudwatchlogs.DescribeExportTasksOutput, error)
	DescribeExportTasksWithContext(aws.Context, *cloudwatchlogs.DescribeExportTasksInput, ...request.Option) (*cloudwatchlogs.DescribeExportTasksOutput, error)
	DescribeExportTasksRequest(*cloudwatchlogs.DescribeExportTasksInput) (*request.Request, *cloudwatchlogs.DescribeExportTasksOutput)

	DescribeLogGroups(*cloudwatchlogs.DescribeLogGroupsInput) (*cloudwatchlogs.DescribeLogGroupsOutput, error)
	DescribeLogGroupsWithContext(aws.Context, *cloudwatchlogs.DescribeLogGroupsInput, ...request.Option) (*cloudwatchlogs.DescribeLogGroupsOutput, error)
	DescribeLogGroupsRequest(*cloudwatchlogs.DescribeLogGroupsInput) (*request.Request, *cloudwatchlogs.DescribeLogGroupsOutput)

	DescribeLogGroupsPages(*cloudwatchlogs.DescribeLogGroupsInput, func(*cloudwatchlogs.DescribeLogGroupsOutput, bool) bool) error
	DescribeLogGroupsPagesWithContext(aws.Context, *cloudwatchlogs.DescribeLogGroupsInput, func(*cloudwatchlogs.DescribeLogGroupsOutput, bool) bool, ...request.Option) error

	DescribeLogStreams(*cloudwatchlogs.DescribeLogStreamsInput) (*cloudwatchlogs.DescribeLogStreamsOutput, error)
	DescribeLogStreamsWithContext(aws.Context, *cloudwatchlogs.DescribeLogStreamsInput, ...request.Option) (*cloudwatchlogs.DescribeLogStreamsOutput, error)
	DescribeLogStreamsRequest(*cloudwatchlogs.DescribeLogStreamsInput) (*request.Request, *cloudwatchlogs.DescribeLogStreamsOutput)

	DescribeLogStreamsPages(*cloudwatchlogs.DescribeLogStreamsInput, func(*cloudwatchlogs.DescribeLogStreamsOutput, bool) bool) error
	DescribeLogStreamsPagesWithContext(aws.Context, *cloudwatchlogs.DescribeLogStreamsInput, func(*cloudwatchlogs.DescribeLogStreamsOutput, bool) bool, ...request.Option) error

	DescribeMetricFilters(*cloudwatchlogs.DescribeMetricFiltersInput) (*cloudwatchlogs.DescribeMetricFiltersOutput, error)
	DescribeMetricFiltersWithContext(aws.Context, *cloudwatchlogs.DescribeMetricFiltersInput, ...request.Option) (*cloudwatchlogs.DescribeMetricFiltersOutput, error)
	DescribeMetricFiltersRequest(*cloudwatchlogs.DescribeMetricFiltersInput) (*request.Request, *cloudwatchlogs.DescribeMetricFiltersOutput)

	DescribeMetricFiltersPages(*cloudwatchlogs.DescribeMetricFiltersInput, func(*cloudwatchlogs.DescribeMetricFiltersOutput, bool) bool) error
	DescribeMetricFiltersPagesWithContext(aws.Context, *cloudwatchlogs.DescribeMetricFiltersInput, func(*cloudwatchlogs.DescribeMetricFiltersOutput, bool) bool, ...request.Option) error

	DescribeQueries(*cloudwatchlogs.DescribeQueriesInput) (*cloudwatchlogs.DescribeQueriesOutput, error)
	DescribeQueriesWithContext(aws.Context, *cloudwatchlogs.DescribeQueriesInput, ...request.Option) (*cloudwatchlogs.DescribeQueriesOutput, error)
	DescribeQueriesRequest(*cloudwatchlogs.DescribeQueriesInput) (*request.Request, *cloudwatchlogs.DescribeQueriesOutput)

	DescribeQueryDefinitions(*cloudwatchlogs.DescribeQueryDefinitionsInput) (*cloudwatchlogs.DescribeQueryDefinitionsOutput, error)
	DescribeQueryDefinitionsWithContext(aws.Context, *cloudwatchlogs.DescribeQueryDefinitionsInput, ...request.Option) (*cloudwatchlogs.DescribeQueryDefinitionsOutput, error)
	DescribeQueryDefinitionsRequest(*cloudwatchlogs.DescribeQueryDefinitionsInput) (*request.Request, *cloudwatchlogs.DescribeQueryDefinitionsOutput)

	DescribeResourcePolicies(*cloudwatchlogs.DescribeResourcePoliciesInput) (*cloudwatchlogs.DescribeResourcePoliciesOutput, error)
	DescribeResourcePoliciesWithContext(aws.Context, *cloudwatchlogs.DescribeResourcePoliciesInput, ...request.Option) (*cloudwatchlogs.DescribeResourcePoliciesOutput, error)
	DescribeResourcePoliciesRequest(*cloudwatchlogs.DescribeResourcePoliciesInput) (*request.Request, *cloudwatchlogs.DescribeResourcePoliciesOutput)

	DescribeSubscriptionFilters(*cloudwatchlogs.DescribeSubscriptionFiltersInput) (*cloudwatchlogs.DescribeSubscriptionFiltersOutput, error)
	DescribeSubscriptionFiltersWithContext(aws.Context, *cloudwatchlogs.DescribeSubscriptionFiltersInput, ...request.Option) (*cloudwatchlogs.DescribeSubscriptionFiltersOutput, error)
	DescribeSubscriptionFiltersRequest(*cloudwatchlogs.DescribeSubscriptionFiltersInput) (*request.Request, *cloudwatchlogs.DescribeSubscriptionFiltersOutput)

	DescribeSubscriptionFiltersPages(*cloudwatchlogs.DescribeSubscriptionFiltersInput, func(*cloudwatchlogs.DescribeSubscriptionFiltersOutput, bool) bool) error
	DescribeSubscriptionFiltersPagesWithContext(aws.Context, *cloudwatchlogs.DescribeSubscriptionFiltersInput, func(*cloudwatchlogs.DescribeSubscriptionFiltersOutput, bool) bool, ...request.Option) error

	DisassociateKmsKey(*cloudwatchlogs.DisassociateKmsKeyInput) (*cloudwatchlogs.DisassociateKmsKeyOutput, error)
	DisassociateKmsKeyWithContext(aws.Context, *cloudwatchlogs.DisassociateKmsKeyInput, ...request.Option) (*cloudwatchlogs.DisassociateKmsKeyOutput, error)
	DisassociateKmsKeyRequest(*cloudwatchlogs.DisassociateKmsKeyInput) (*request.Request, *cloudwatchlogs.DisassociateKmsKeyOutput)

	FilterLogEvents(*cloudwatchlogs.FilterLogEventsInput) (*cloudwatchlogs.FilterLogEventsOutput, error)
	FilterLogEventsWithContext(aws.Context, *cloudwatchlogs.FilterLogEventsInput, ...request.Option) (*cloudwatchlogs.FilterLogEventsOutput, error)
	FilterLogEventsRequest(*cloudwatchlogs.FilterLogEventsInput) (*request.Request, *cloudwatchlogs.FilterLogEventsOutput)

	FilterLogEventsPages(*cloudwatchlogs.FilterLogEventsInput, func(*cloudwatchlogs.FilterLogEventsOutput, bool) bool) error
	FilterLogEventsPagesWithContext(aws.Context, *cloudwatchlogs.FilterLogEventsInput, func(*cloudwatchlogs.FilterLogEventsOutput, bool) bool, ...request.Option) error

	GetDataProtectionPolicy(*cloudwatchlogs.GetDataProtectionPolicyInput) (*cloudwatchlogs.GetDataProtectionPolicyOutput, error)
	GetDataProtectionPolicyWithContext(aws.Context, *cloudwatchlogs.GetDataProtectionPolicyInput, ...request.Option) (*cloudwatchlogs.GetDataProtectionPolicyOutput, error)
	GetDataProtectionPolicyRequest(*cloudwatchlogs.GetDataProtectionPolicyInput) (*request.Request, *cloudwatchlogs.GetDataProtectionPolicyOutput)

	GetDelivery(*cloudwatchlogs.GetDeliveryInput) (*cloudwatchlogs.GetDeliveryOutput, error)
	GetDeliveryWithContext(aws.Context, *cloudwatchlogs.GetDeliveryInput, ...request.Option) (*cloudwatchlogs.GetDeliveryOutput, error)
	GetDeliveryRequest(*cloudwatchlogs.GetDeliveryInput) (*request.Request, *cloudwatchlogs.GetDeliveryOutput)

	GetDeliveryDestination(*cloudwatchlogs.GetDeliveryDestinationInput) (*cloudwatchlogs.GetDeliveryDestinationOutput, error)
	GetDeliveryDestinationWithContext(aws.Context, *cloudwatchlogs.GetDeliveryDestinationInput, ...request.Option) (*cloudwatchlogs.GetDeliveryDestinationOutput, error)
	GetDeliveryDestinationRequest(*cloudwatchlogs.GetDeliveryDestinationInput) (*request.Request, *cloudwatchlogs.GetDeliveryDestinationOutput)

	GetDeliveryDestinationPolicy(*cloudwatchlogs.GetDeliveryDestinationPolicyInput) (*cloudwatchlogs.GetDeliveryDestinationPolicyOutput, error)
	GetDeliveryDestinationPolicyWithContext(aws.Context, *cloudwatchlogs.GetDeliveryDestinationPolicyInput, ...request.Option) (*cloudwatchlogs.GetDeliveryDestinationPolicyOutput, error)
	GetDeliveryDestinationPolicyRequest(*cloudwatchlogs.GetDeliveryDestinationPolicyInput) (*request.Request, *cloudwatchlogs.GetDeliveryDestinationPolicyOutput)

	GetDeliverySource(*cloudwatchlogs.GetDeliverySourceInput) (*cloudwatchlogs.GetDeliverySourceOutput, error)
	GetDeliverySourceWithContext(aws.Context, *cloudwatchlogs.GetDeliverySourceInput, ...request.Option) (*cloudwatchlogs.GetDeliverySourceOutput, error)
	GetDeliverySourceRequest(*cloudwatchlogs.GetDeliverySourceInput) (*request.Request, *cloudwatchlogs.GetDeliverySourceOutput)

	GetLogAnomalyDetector(*cloudwatchlogs.GetLogAnomalyDetectorInput) (*cloudwatchlogs.GetLogAnomalyDetectorOutput, error)
	GetLogAnomalyDetectorWithContext(aws.Context, *cloudwatchlogs.GetLogAnomalyDetectorInput, ...request.Option) (*cloudwatchlogs.GetLogAnomalyDetectorOutput, error)
	GetLogAnomalyDetectorRequest(*cloudwatchlogs.GetLogAnomalyDetectorInput) (*request.Request, *cloudwatchlogs.GetLogAnomalyDetectorOutput)

	GetLogEvents(*cloudwatchlogs.GetLogEventsInput) (*cloudwatchlogs.GetLogEventsOutput, error)
	GetLogEventsWithContext(aws.Context, *cloudwatchlogs.GetLogEventsInput, ...request.Option) (*cloudwatchlogs.GetLogEventsOutput, error)
	GetLogEventsRequest(*cloudwatchlogs.GetLogEventsInput) (*request.Request, *cloudwatchlogs.GetLogEventsOutput)

	GetLogEventsPages(*cloudwatchlogs.GetLogEventsInput, func(*cloudwatchlogs.GetLogEventsOutput, bool) bool) error
	GetLogEventsPagesWithContext(aws.Context, *cloudwatchlogs.GetLogEventsInput, func(*cloudwatchlogs.GetLogEventsOutput, bool) bool, ...request.Option) error

	GetLogGroupFields(*cloudwatchlogs.GetLogGroupFieldsInput) (*cloudwatchlogs.GetLogGroupFieldsOutput, error)
	GetLogGroupFieldsWithContext(aws.Context, *cloudwatchlogs.GetLogGroupFieldsInput, ...request.Option) (*cloudwatchlogs.GetLogGroupFieldsOutput, error)
	GetLogGroupFieldsRequest(*cloudwatchlogs.GetLogGroupFieldsInput) (*request.Request, *cloudwatchlogs.GetLogGroupFieldsOutput)

	GetLogRecord(*cloudwatchlogs.GetLogRecordInput) (*cloudwatchlogs.GetLogRecordOutput, error)
	GetLogRecordWithContext(aws.Context, *cloudwatchlogs.GetLogRecordInput, ...request.Option) (*cloudwatchlogs.GetLogRecordOutput, error)
	GetLogRecordRequest(*cloudwatchlogs.GetLogRecordInput) (*request.Request, *cloudwatchlogs.GetLogRecordOutput)

	GetQueryResults(*cloudwatchlogs.GetQueryResultsInput) (*cloudwatchlogs.GetQueryResultsOutput, error)
	GetQueryResultsWithContext(aws.Context, *cloudwatchlogs.GetQueryResultsInput, ...request.Option) (*cloudwatchlogs.GetQueryResultsOutput, error)
	GetQueryResultsRequest(*cloudwatchlogs.GetQueryResultsInput) (*request.Request, *cloudwatchlogs.GetQueryResultsOutput)

	ListAnomalies(*cloudwatchlogs.ListAnomaliesInput) (*cloudwatchlogs.ListAnomaliesOutput, error)
	ListAnomaliesWithContext(aws.Context, *cloudwatchlogs.ListAnomaliesInput, ...request.Option) (*cloudwatchlogs.ListAnomaliesOutput, error)
	ListAnomaliesRequest(*cloudwatchlogs.ListAnomaliesInput) (*request.Request, *cloudwatchlogs.ListAnomaliesOutput)

	ListAnomaliesPages(*cloudwatchlogs.ListAnomaliesInput, func(*cloudwatchlogs.ListAnomaliesOutput, bool) bool) error
	ListAnomaliesPagesWithContext(aws.Context, *cloudwatchlogs.ListAnomaliesInput, func(*cloudwatchlogs.ListAnomaliesOutput, bool) bool, ...request.Option) error

	ListLogAnomalyDetectors(*cloudwatchlogs.ListLogAnomalyDetectorsInput) (*cloudwatchlogs.ListLogAnomalyDetectorsOutput, error)
	ListLogAnomalyDetectorsWithContext(aws.Context, *cloudwatchlogs.ListLogAnomalyDetectorsInput, ...request.Option) (*cloudwatchlogs.ListLogAnomalyDetectorsOutput, error)
	ListLogAnomalyDetectorsRequest(*cloudwatchlogs.ListLogAnomalyDetectorsInput) (*request.Request, *cloudwatchlogs.ListLogAnomalyDetectorsOutput)

	ListLogAnomalyDetectorsPages(*cloudwatchlogs.ListLogAnomalyDetectorsInput, func(*cloudwatchlogs.ListLogAnomalyDetectorsOutput, bool) bool) error
	ListLogAnomalyDetectorsPagesWithContext(aws.Context, *cloudwatchlogs.ListLogAnomalyDetectorsInput, func(*cloudwatchlogs.ListLogAnomalyDetectorsOutput, bool) bool, ...request.Option) error

	ListTagsForResource(*cloudwatchlogs.ListTagsForResourceInput) (*cloudwatchlogs.ListTagsForResourceOutput, error)
	ListTagsForResourceWithContext(aws.Context, *cloudwatchlogs.ListTagsForResourceInput, ...request.Option) (*cloudwatchlogs.ListTagsForResourceOutput, error)
	ListTagsForResourceRequest(*cloudwatchlogs.ListTagsForResourceInput) (*request.Request, *cloudwatchlogs.ListTagsForResourceOutput)

	ListTagsLogGroup(*cloudwatchlogs.ListTagsLogGroupInput) (*cloudwatchlogs.ListTagsLogGroupOutput, error)
	ListTagsLogGroupWithContext(aws.Context, *cloudwatchlogs.ListTagsLogGroupInput, ...request.Option) (*cloudwatchlogs.ListTagsLogGroupOutput, error)
	ListTagsLogGroupRequest(*cloudwatchlogs.ListTagsLogGroupInput) (*request.Request, *cloudwatchlogs.ListTagsLogGroupOutput)

	PutAccountPolicy(*cloudwatchlogs.PutAccountPolicyInput) (*cloudwatchlogs.PutAccountPolicyOutput, error)
	PutAccountPolicyWithContext(aws.Context, *cloudwatchlogs.PutAccountPolicyInput, ...request.Option) (*cloudwatchlogs.PutAccountPolicyOutput, error)
	PutAccountPolicyRequest(*cloudwatchlogs.PutAccountPolicyInput) (*request.Request, *cloudwatchlogs.PutAccountPolicyOutput)

	PutDataProtectionPolicy(*cloudwatchlogs.PutDataProtectionPolicyInput) (*cloudwatchlogs.PutDataProtectionPolicyOutput, error)
	PutDataProtectionPolicyWithContext(aws.Context, *cloudwatchlogs.PutDataProtectionPolicyInput, ...request.Option) (*cloudwatchlogs.PutDataProtectionPolicyOutput, error)
	PutDataProtectionPolicyRequest(*cloudwatchlogs.PutDataProtectionPolicyInput) (*request.Request, *cloudwatchlogs.PutDataProtectionPolicyOutput)

	PutDeliveryDestination(*cloudwatchlogs.PutDeliveryDestinationInput) (*cloudwatchlogs.PutDeliveryDestinationOutput, error)
	PutDeliveryDestinationWithContext(aws.Context, *cloudwatchlogs.PutDeliveryDestinationInput, ...request.Option) (*cloudwatchlogs.PutDeliveryDestinationOutput, error)
	PutDeliveryDestinationRequest(*cloudwatchlogs.PutDeliveryDestinationInput) (*request.Request, *cloudwatchlogs.PutDeliveryDestinationOutput)

	PutDeliveryDestinationPolicy(*cloudwatchlogs.PutDeliveryDestinationPolicyInput) (*cloudwatchlogs.PutDeliveryDestinationPolicyOutput, error)
	PutDeliveryDestinationPolicyWithContext(aws.Context, *cloudwatchlogs.PutDeliveryDestinationPolicyInput, ...request.Option) (*cloudwatchlogs.PutDeliveryDestinationPolicyOutput, error)
	PutDeliveryDestinationPolicyRequest(*cloudwatchlogs.PutDeliveryDestinationPolicyInput) (*request.Request, *cloudwatchlogs.PutDeliveryDestinationPolicyOutput)

	PutDeliverySource(*cloudwatchlogs.PutDeliverySourceInput) (*cloudwatchlogs.PutDeliverySourceOutput, error)
	PutDeliverySourceWithContext(aws.Context, *cloudwatchlogs.PutDeliverySourceInput, ...request.Option) (*cloudwatchlogs.PutDeliverySourceOutput, error)
	PutDeliverySourceRequest(*cloudwatchlogs.PutDeliverySourceInput) (*request.Request, *cloudwatchlogs.PutDeliverySourceOutput)

	PutDestination(*cloudwatchlogs.PutDestinationInput) (*cloudwatchlogs.PutDestinationOutput, error)
	PutDestinationWithContext(aws.Context, *cloudwatchlogs.PutDestinationInput, ...request.Option) (*cloudwatchlogs.PutDestinationOutput, error)
	PutDestinationRequest(*cloudwatchlogs.PutDestinationInput) (*request.Request, *cloudwatchlogs.PutDestinationOutput)

	PutDestinationPolicy(*cloudwatchlogs.PutDestinationPolicyInput) (*cloudwatchlogs.PutDestinationPolicyOutput, error)
	PutDestinationPolicyWithContext(aws.Context, *cloudwatchlogs.PutDestinationPolicyInput, ...request.Option) (*cloudwatchlogs.PutDestinationPolicyOutput, error)
	PutDestinationPolicyRequest(*cloudwatchlogs.PutDestinationPolicyInput) (*request.Request, *cloudwatchlogs.PutDestinationPolicyOutput)

	PutLogEvents(*cloudwatchlogs.PutLogEventsInput) (*cloudwatchlogs.PutLogEventsOutput, error)
	PutLogEventsWithContext(aws.Context, *cloudwatchlogs.PutLogEventsInput, ...request.Option) (*cloudwatchlogs.PutLogEventsOutput, error)
	PutLogEventsRequest(*cloudwatchlogs.PutLogEventsInput) (*request.Request, *cloudwatchlogs.PutLogEventsOutput)

	PutMetricFilter(*cloudwatchlogs.PutMetricFilterInput) (*cloudwatchlogs.PutMetricFilterOutput, error)
	PutMetricFilterWithContext(aws.Context, *cloudwatchlogs.PutMetricFilterInput, ...request.Option) (*cloudwatchlogs.PutMetricFilterOutput, error)
	PutMetricFilterRequest(*cloudwatchlogs.PutMetricFilterInput) (*request.Request, *cloudwatchlogs.PutMetricFilterOutput)

	PutQueryDefinition(*cloudwatchlogs.PutQueryDefinitionInput) (*cloudwatchlogs.PutQueryDefinitionOutput, error)
	PutQueryDefinitionWithContext(aws.Context, *cloudwatchlogs.PutQueryDefinitionInput, ...request.Option) (*cloudwatchlogs.PutQueryDefinitionOutput, error)
	PutQueryDefinitionRequest(*cloudwatchlogs.PutQueryDefinitionInput) (*request.Request, *cloudwatchlogs.PutQueryDefinitionOutput)

	PutResourcePolicy(*cloudwatchlogs.PutResourcePolicyInput) (*cloudwatchlogs.PutResourcePolicyOutput, error)
	PutResourcePolicyWithContext(aws.Context, *cloudwatchlogs.PutResourcePolicyInput, ...request.Option) (*cloudwatchlogs.PutResourcePolicyOutput, error)
	PutResourcePolicyRequest(*cloudwatchlogs.PutResourcePolicyInput) (*request.Request, *cloudwatchlogs.PutResourcePolicyOutput)

	PutRetentionPolicy(*cloudwatchlogs.PutRetentionPolicyInput) (*cloudwatchlogs.PutRetentionPolicyOutput, error)
	PutRetentionPolicyWithContext(aws.Context, *cloudwatchlogs.PutRetentionPolicyInput, ...request.Option) (*cloudwatchlogs.PutRetentionPolicyOutput, error)
	PutRetentionPolicyRequest(*cloudwatchlogs.PutRetentionPolicyInput) (*request.Request, *cloudwatchlogs.PutRetentionPolicyOutput)

	PutSubscriptionFilter(*cloudwatchlogs.PutSubscriptionFilterInput) (*cloudwatchlogs.PutSubscriptionFilterOutput, error)
	PutSubscriptionFilterWithContext(aws.Context, *cloudwatchlogs.PutSubscriptionFilterInput, ...request.Option) (*cloudwatchlogs.PutSubscriptionFilterOutput, error)
	PutSubscriptionFilterRequest(*cloudwatchlogs.PutSubscriptionFilterInput) (*request.Request, *cloudwatchlogs.PutSubscriptionFilterOutput)

	StartLiveTail(*cloudwatchlogs.StartLiveTailInput) (*cloudwatchlogs.StartLiveTailOutput, error)
	StartLiveTailWithContext(aws.Context, *cloudwatchlogs.StartLiveTailInput, ...request.Option) (*cloudwatchlogs.StartLiveTailOutput, error)
	StartLiveTailRequest(*cloudwatchlogs.StartLiveTailInput) (*request.Request, *cloudwatchlogs.StartLiveTailOutput)

	StartQuery(*cloudwatchlogs.StartQueryInput) (*cloudwatchlogs.StartQueryOutput, error)
	StartQueryWithContext(aws.Context, *cloudwatchlogs.StartQueryInput, ...request.Option) (*cloudwatchlogs.StartQueryOutput, error)
	StartQueryRequest(*cloudwatchlogs.StartQueryInput) (*request.Request, *cloudwatchlogs.StartQueryOutput)

	StopQuery(*cloudwatchlogs.StopQueryInput) (*cloudwatchlogs.StopQueryOutput, error)
	StopQueryWithContext(aws.Context, *cloudwatchlogs.StopQueryInput, ...request.Option) (*cloudwatchlogs.StopQueryOutput, error)
	StopQueryRequest(*cloudwatchlogs.StopQueryInput) (*request.Request, *cloudwatchlogs.StopQueryOutput)

	TagLogGroup(*cloudwatchlogs.TagLogGroupInput) (*cloudwatchlogs.TagLogGroupOutput, error)
	TagLogGroupWithContext(aws.Context, *cloudwatchlogs.TagLogGroupInput, ...request.Option) (*cloudwatchlogs.TagLogGroupOutput, error)
	TagLogGroupRequest(*cloudwatchlogs.TagLogGroupInput) (*request.Request, *cloudwatchlogs.TagLogGroupOutput)

	TagResource(*cloudwatchlogs.TagResourceInput) (*cloudwatchlogs.TagResourceOutput, error)
	TagResourceWithContext(aws.Context, *cloudwatchlogs.TagResourceInput, ...request.Option) (*cloudwatchlogs.TagResourceOutput, error)
	TagResourceRequest(*cloudwatchlogs.TagResourceInput) (*request.Request, *cloudwatchlogs.TagResourceOutput)

	TestMetricFilter(*cloudwatchlogs.TestMetricFilterInput) (*cloudwatchlogs.TestMetricFilterOutput, error)
	TestMetricFilterWithContext(aws.Context, *cloudwatchlogs.TestMetricFilterInput, ...request.Option) (*cloudwatchlogs.TestMetricFilterOutput, error)
	TestMetricFilterRequest(*cloudwatchlogs.TestMetricFilterInput) (*request.Request, *cloudwatchlogs.TestMetricFilterOutput)

	UntagLogGroup(*cloudwatchlogs.UntagLogGroupInput) (*cloudwatchlogs.UntagLogGroupOutput, error)
	UntagLogGroupWithContext(aws.Context, *cloudwatchlogs.UntagLogGroupInput, ...request.Option) (*cloudwatchlogs.UntagLogGroupOutput, error)
	UntagLogGroupRequest(*cloudwatchlogs.UntagLogGroupInput) (*request.Request, *cloudwatchlogs.UntagLogGroupOutput)

	UntagResource(*cloudwatchlogs.UntagResourceInput) (*cloudwatchlogs.UntagResourceOutput, error)
	UntagResourceWithContext(aws.Context, *cloudwatchlogs.UntagResourceInput, ...request.Option) (*cloudwatchlogs.UntagResourceOutput, error)
	UntagResourceRequest(*cloudwatchlogs.UntagResourceInput) (*request.Request, *cloudwatchlogs.UntagResourceOutput)

	UpdateAnomaly(*cloudwatchlogs.UpdateAnomalyInput) (*cloudwatchlogs.UpdateAnomalyOutput, error)
	UpdateAnomalyWithContext(aws.Context, *cloudwatchlogs.UpdateAnomalyInput, ...request.Option) (*cloudwatchlogs.UpdateAnomalyOutput, error)
	UpdateAnomalyRequest(*cloudwatchlogs.UpdateAnomalyInput) (*request.Request, *cloudwatchlogs.UpdateAnomalyOutput)

	UpdateLogAnomalyDetector(*cloudwatchlogs.UpdateLogAnomalyDetectorInput) (*cloudwatchlogs.UpdateLogAnomalyDetectorOutput, error)
	UpdateLogAnomalyDetectorWithContext(aws.Context, *cloudwatchlogs.UpdateLogAnomalyDetectorInput, ...request.Option) (*cloudwatchlogs.UpdateLogAnomalyDetectorOutput, error)
	UpdateLogAnomalyDetectorRequest(*cloudwatchlogs.UpdateLogAnomalyDetectorInput) (*request.Request, *cloudwatchlogs.UpdateLogAnomalyDetectorOutput)
}

var _ CloudWatchLogsAPI = (*cloudwatchlogs.CloudWatchLogs)(nil)
