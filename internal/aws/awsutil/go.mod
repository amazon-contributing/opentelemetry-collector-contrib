module github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/awsutil

go 1.20

require (
	github.com/amazon-contributing/opentelemetry-collector-contrib/override/aws v0.0.0-00010101000000-000000000000
	github.com/aws/aws-sdk-go v1.47.10
	github.com/open-telemetry/opentelemetry-collector-contrib/internal/metadataproviders v0.89.0
	github.com/stretchr/testify v1.8.4
	go.uber.org/zap v1.26.0
	golang.org/x/net v0.18.0
)

require (
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/jmespath/go-jmespath v0.4.0 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/stretchr/objx v0.5.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/text v0.14.0 // indirect
	gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/open-telemetry/opentelemetry-collector-contrib/internal/metadataproviders => ../../../internal/metadataproviders

replace github.com/amazon-contributing/opentelemetry-collector-contrib/override/aws => ../../../override/aws

// openshift removed all tags from their repo, use the pseudoversion from the release-3.9 branch HEAD
replace github.com/openshift/api v3.9.0+incompatible => github.com/openshift/api v0.0.0-20180801171038-322a19404e37

retract (
	v0.76.2
	v0.76.1
	v0.65.0
)
