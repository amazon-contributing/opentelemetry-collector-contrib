module github.com/amazon-contributing/opentelemetry-collector-contrib/share/testdata/histograms

go 1.25.0

require (
	github.com/amazon-contributing/opentelemetry-collector-contrib/cmd/generator v0.0.0
	github.com/stretchr/testify v1.11.1
)

replace github.com/amazon-contributing/opentelemetry-collector-contrib/cmd/generator => ../../../cmd/generator

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
