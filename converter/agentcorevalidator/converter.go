// Package agentcorevalidator provides a confmap.Converter that validates
// the final collector configuration to ensure data is only exported to
// approved AWS endpoints. This prevents users from overriding the default
// exporter configuration to send telemetry data to unauthorized destinations.
package agentcorevalidator

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"go.opentelemetry.io/collector/confmap"
)

// allowedExporterTypes defines the exporter types (the part before "/") that
// are permitted. Users may use any alias (e.g. "otlphttp/myname" is allowed).
var allowedExporterTypes = map[string]bool{
	"otlphttp": true,
	"awsemf":   true,
}

// endpointPatterns maps each endpoint field to the regex it must satisfy.
// All patterns enforce HTTPS and an AWS domain (*.amazonaws.com[.cn]).
var endpointPatterns = map[string]*regexp.Regexp{
	// traces_endpoint must point to the AWS X-Ray service.
	"traces_endpoint": regexp.MustCompile(
		`^https://xray\.[a-z0-9][a-z0-9.\-]*\.amazonaws\.com(\.cn)?(/.*)?$`,
	),
	// logs_endpoint must point to the AWS CloudWatch Logs service.
	"logs_endpoint": regexp.MustCompile(
		`^https://logs\.[a-z0-9][a-z0-9.\-]*\.amazonaws\.com(\.cn)?(/.*)?$`,
	),
	// generic endpoint field: any AWS subdomain is accepted.
	"endpoint": regexp.MustCompile(
		`^https://[a-z0-9][a-z0-9.\-]*\.amazonaws\.com(\.cn)?(/.*)?$`,
	),
}

// otlpHTTPEndpointFields lists the fields in an otlphttp exporter config that
// contain endpoint URLs.
var otlpHTTPEndpointFields = []string{
	"endpoint",
	"traces_endpoint",
	"logs_endpoint",
}

type converter struct{}

// NewFactory creates a ConverterFactory for the AgentCore exporter validator.
func NewFactory() confmap.ConverterFactory {
	return confmap.NewConverterFactory(
		func(_ confmap.ConverterSettings) confmap.Converter {
			return &converter{}
		},
	)
}

func (c *converter) Convert(_ context.Context, conf *confmap.Conf) error {
	if err := c.validateExporters(conf); err != nil {
		return fmt.Errorf("agentcore runtime config validation failed: %w", err)
	}
	if err := c.validatePipelineExporters(conf); err != nil {
		return fmt.Errorf("agentcore runtime config validation failed: %w", err)
	}
	return nil
}

// exporterType returns the type portion of an exporter name, e.g.
// "otlphttp/xray" -> "otlphttp", "awsemf" -> "awsemf".
func exporterType(name string) string {
	if i := strings.Index(name, "/"); i >= 0 {
		return name[:i]
	}
	return name
}

// validateExporters checks that only allowed exporter types are defined and
// their endpoints point to approved AWS domains.
func (c *converter) validateExporters(conf *confmap.Conf) error {
	exportersSub, err := conf.Sub("exporters")
	if err != nil {
		// No exporters section — default config will be used.
		return nil
	}

	exporterMap := exportersSub.ToStringMap()
	for name := range exporterMap {
		typ := exporterType(name)
		if !allowedExporterTypes[typ] {
			return fmt.Errorf("exporter %q has type %q which is not allowed; permitted types: %s",
				name, typ, allowedExporterTypeNames())
		}
		if err := c.validateExporterEndpoints(conf, name, typ); err != nil {
			return err
		}
	}
	return nil
}

// validateExporterEndpoints validates endpoint fields for a single exporter
// based on its type.
func (c *converter) validateExporterEndpoints(conf *confmap.Conf, name, typ string) error {
	sub, err := conf.Sub("exporters::" + name)
	if err != nil {
		return nil
	}
	cfgMap := sub.ToStringMap()

	var fields []string
	switch typ {
	case "otlphttp":
		fields = otlpHTTPEndpointFields
	case "awsemf":
		fields = []string{"endpoint"}
	}

	for _, field := range fields {
		val, ok := cfgMap[field]
		if !ok {
			continue
		}
		endpoint, ok := val.(string)
		if !ok {
			continue
		}
		if err := validateEndpoint(field, endpoint); err != nil {
			return fmt.Errorf("exporter %q field %q: %w", name, field, err)
		}
	}
	return nil
}

// validatePipelineExporters checks that all exporters referenced in service
// pipelines are either of an allowed type or are defined connectors.
func (c *converter) validatePipelineExporters(conf *confmap.Conf) error {
	// Connectors can appear as exporters in pipelines — collect defined ones.
	definedConnectors := map[string]bool{}
	if connSub, err := conf.Sub("connectors"); err == nil {
		for name := range connSub.ToStringMap() {
			definedConnectors[name] = true
		}
	}

	serviceSub, err := conf.Sub("service")
	if err != nil {
		return nil
	}
	pipelinesSub, err := serviceSub.Sub("pipelines")
	if err != nil {
		return nil
	}

	for pipelineName, pipelineCfg := range pipelinesSub.ToStringMap() {
		pMap, ok := pipelineCfg.(map[string]any)
		if !ok {
			continue
		}
		exporterList, ok := pMap["exporters"].([]any)
		if !ok {
			continue
		}
		for _, e := range exporterList {
			name, ok := e.(string)
			if !ok {
				continue
			}
			if !allowedExporterTypes[exporterType(name)] && !definedConnectors[name] {
				return fmt.Errorf("pipeline %q references unauthorized exporter %q; permitted types: %s (and defined connectors)",
					pipelineName, name, allowedExporterTypeNames())
			}
		}
	}
	return nil
}

// validateEndpoint checks that the endpoint matches the pattern for its field.
func validateEndpoint(field, endpoint string) error {
	pattern, ok := endpointPatterns[field]
	if !ok {
		// Unknown field — fall back to the generic AWS pattern.
		pattern = endpointPatterns["endpoint"]
	}
	if !pattern.MatchString(endpoint) {
		return fmt.Errorf("endpoint %q does not match the required pattern for field %q"+
			" (must be an HTTPS AWS endpoint, e.g. https://xray.<region>.amazonaws.com/...)",
			endpoint, field)
	}
	return nil
}

func allowedExporterTypeNames() string {
	names := make([]string, 0, len(allowedExporterTypes))
	for name := range allowedExporterTypes {
		names = append(names, name)
	}
	return strings.Join(names, ", ")
}
