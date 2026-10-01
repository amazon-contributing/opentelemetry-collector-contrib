module github.com/open-telemetry/opentelemetry-collector-contrib/internal/tools

go 1.25.0

require go.uber.org/goleak v1.3.0

require (
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/stretchr/testify v1.11.1 // indirect
)

retract (
	v0.76.2
	v0.76.1
	v0.65.0
)
