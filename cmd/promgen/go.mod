module github.com/amazon-contributing/opentelemetry-collector-contrib/cmd/promgen

go 1.25.0

replace github.com/amazon-contributing/opentelemetry-collector-contrib/share/testdata/histograms => /local/home/dricross/workplace/classichistograms/opentelemetry-collector-contrib/share/testdata/histograms

require (
	github.com/amazon-contributing/opentelemetry-collector-contrib/share/testdata/histograms v0.124.1
	github.com/prometheus/client_golang v1.23.2
	google.golang.org/protobuf v1.36.9
)

require (
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.66.1 // indirect
	github.com/prometheus/procfs v0.16.1 // indirect
	go.yaml.in/yaml/v2 v2.4.2 // indirect
	golang.org/x/sys v0.35.0 // indirect
)
