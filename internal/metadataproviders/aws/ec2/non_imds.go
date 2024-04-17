// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package ec2 // import "github.com/open-telemetry/opentelemetry-collector-contrib/internal/metadataproviders/aws/ec2"

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/client"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
)

const (
	envHostname               = "HOST_NAME"
	filterKeyInstanceID       = "instance-id"
	filterKeyPrivateIPAddress = "private-ip-address"
	prefixInstanceID          = "i-"
	prefixPrivateIPAddress    = "ip-"
	suffixDefault             = ".ec2.internal"
	suffixRegional            = ".compute.internal"
)

var (
	errUnsupportedHostname = errors.New("unable to parse non-fixed format hostname")
	errUnsupportedFilter   = errors.New("unable to determine EC2 filter")
	errReservationCount    = errors.New("invalid number of reservations found")
	errInstanceCount       = errors.New("invalid number of instances found")
)

type ec2ClientProvider func(client.ConfigProvider, ...*aws.Config) ec2iface.EC2API

type describeInstancesMetadataProvider struct {
	configProvider client.ConfigProvider
	newEC2Client   ec2ClientProvider
	osHostname     func() (string, error)
}

var _ Provider = (*describeInstancesMetadataProvider)(nil)

func newDescribeInstancesMetadataProvider(configProvider client.ConfigProvider) *describeInstancesMetadataProvider {
	return &describeInstancesMetadataProvider{
		configProvider: configProvider,
		newEC2Client: func(provider client.ConfigProvider, configs ...*aws.Config) ec2iface.EC2API {
			return ec2.New(provider, configs...)
		},
		osHostname: os.Hostname,
	}
}

func (p *describeInstancesMetadataProvider) ID() string {
	return "DescribeInstances"
}

func (p *describeInstancesMetadataProvider) Get(ctx context.Context) (*Metadata, error) {
	filter, region, err := p.getEC2FilterAndRegion(ctx)
	if err != nil {
		return nil, err
	}
	input := &ec2.DescribeInstancesInput{Filters: []*ec2.Filter{filter}}
	cfg := &aws.Config{
		CredentialsChainVerboseErrors: aws.Bool(true),
	}
	if region != "" {
		cfg = cfg.WithRegion(region)
	}
	svc := p.newEC2Client(p.configProvider, cfg)
	output, err := svc.DescribeInstances(input)
	if err != nil {
		return nil, err
	}
	reservationCount := len(output.Reservations)
	if reservationCount == 0 || reservationCount > 1 {
		return nil, fmt.Errorf("%w: %v", errReservationCount, reservationCount)
	}
	metadata, err := fromReservation(*output.Reservations[0])
	if err != nil {
		return nil, err
	}
	metadata.Region = region
	return metadata, nil
}

func (p *describeInstancesMetadataProvider) Hostname(context.Context) (string, error) {
	hostname := os.Getenv(envHostname)
	if hostname == "" {
		return p.osHostname()
	}
	return hostname, nil
}

func (p *describeInstancesMetadataProvider) getEC2FilterAndRegion(ctx context.Context) (*ec2.Filter, string, error) {
	hostname, err := p.Hostname(ctx)
	if err != nil {
		return nil, "", err
	}
	prefix, region, err := splitHostname(hostname)
	if region == "" {
		return nil, "", err
	}
	filter, err := filterFromHostnamePrefix(prefix)
	if err != nil {
		return nil, "", err
	}
	return filter, region, nil
}

func fromReservation(reservation ec2.Reservation) (*Metadata, error) {
	instanceCount := len(reservation.Instances)
	if instanceCount == 0 || instanceCount > 1 {
		return nil, fmt.Errorf("%w: %v", errInstanceCount, instanceCount)
	}
	instance := reservation.Instances[0]
	metadata := &Metadata{
		AccountID:    aws.StringValue(reservation.OwnerId),
		ImageID:      aws.StringValue(instance.ImageId),
		InstanceID:   aws.StringValue(instance.InstanceId),
		InstanceType: aws.StringValue(instance.InstanceType),
		PrivateIP:    aws.StringValue(instance.PrivateIpAddress),
	}
	if instance.Placement != nil {
		metadata.AvailabilityZone = aws.StringValue(instance.Placement.AvailabilityZone)
	}
	return metadata, nil
}

func filterFromHostnamePrefix(prefix string) (*ec2.Filter, error) {
	// i-0123456789abcdef
	if strings.HasPrefix(prefix, prefixInstanceID) {
		return &ec2.Filter{
			Name:   aws.String(filterKeyInstanceID),
			Values: aws.StringSlice([]string{prefix}),
		}, nil
	}
	// ip-10-24-34-0 -> 10.24.34.0
	if ipAddress, ok := strings.CutPrefix(prefix, prefixPrivateIPAddress); ok {
		return &ec2.Filter{
			Name:   aws.String(filterKeyPrivateIPAddress),
			Values: aws.StringSlice([]string{strings.ReplaceAll(ipAddress, "-", ".")}),
		}, nil
	}
	return nil, fmt.Errorf("%w from hostname prefix: %s", errUnsupportedFilter, prefix)
}

// splitHostname extracts the prefix and region based on https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/ec2-instance-naming.html
func splitHostname(hostname string) (prefix string, region string, err error) {
	before, ok := strings.CutSuffix(hostname, suffixRegional)
	if ok {
		parts := strings.Split(before, ".")
		if len(parts) == 2 {
			return parts[0], parts[1], nil
		}
	}
	before, ok = strings.CutSuffix(hostname, suffixDefault)
	if ok {
		return before, "us-east-1", nil
	}
	return hostname, "", fmt.Errorf("%w: %s", errUnsupportedHostname, hostname)
}
