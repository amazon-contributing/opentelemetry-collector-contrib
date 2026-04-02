package agentcorevalidator

import (
	"context"
	"strings"
	"testing"

	"go.opentelemetry.io/collector/confmap"
)

func newConf(m map[string]any) *confmap.Conf {
	return confmap.NewFromStringMap(m)
}

func TestNewFactory(t *testing.T) {
	f := NewFactory()
	c := f.Create(confmap.ConverterSettings{})
	if c == nil {
		t.Fatal("expected non-nil converter")
	}
}

func TestExporterType(t *testing.T) {
	cases := []struct{ name, want string }{
		{"otlphttp/xray", "otlphttp"},
		{"otlphttp/logs", "otlphttp"},
		{"otlphttp", "otlphttp"},
		{"awsemf", "awsemf"},
		{"awsemf/custom", "awsemf"},
	}
	for _, tc := range cases {
		if got := exporterType(tc.name); got != tc.want {
			t.Errorf("exporterType(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// Default config pattern — standard aliases, AWS endpoints.
func TestDefaultConfigPatternPass(t *testing.T) {
	conf := newConf(map[string]any{
		"connectors": map[string]any{
			"genaiadapterconnector": map[string]any{},
		},
		"exporters": map[string]any{
			"otlphttp/xray": map[string]any{
				"traces_endpoint": "https://xray.us-east-1.amazonaws.com/v1/traces",
			},
			"otlphttp/logs": map[string]any{
				"logs_endpoint": "https://logs.us-east-1.amazonaws.com/v1/logs",
			},
			"awsemf": map[string]any{
				"region": "us-east-1",
			},
		},
		"service": map[string]any{
			"pipelines": map[string]any{
				"traces/input": map[string]any{
					"exporters": []any{"genaiadapterconnector"},
				},
				"traces/output": map[string]any{
					"exporters": []any{"otlphttp/xray"},
				},
				"logs/genai": map[string]any{
					"exporters": []any{"otlphttp/logs"},
				},
				"metrics": map[string]any{
					"exporters": []any{"awsemf"},
				},
			},
		},
	})

	c := &converter{}
	if err := c.Convert(context.Background(), conf); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

// User uses a custom alias — should still pass if type and endpoint are valid.
func TestCustomAliasPass(t *testing.T) {
	conf := newConf(map[string]any{
		"exporters": map[string]any{
			"otlphttp/mytraces": map[string]any{
				"traces_endpoint": "https://xray.us-west-2.amazonaws.com/v1/traces",
			},
			"awsemf/mymetrics": map[string]any{
				"region": "us-west-2",
			},
		},
	})

	c := &converter{}
	if err := c.Convert(context.Background(), conf); err != nil {
		t.Fatalf("custom alias with valid type should pass, got: %v", err)
	}
}

// Unauthorized exporter type must be rejected.
func TestUnauthorizedTypeRejected(t *testing.T) {
	for _, name := range []string{"debug", "otlp/grpc", "file", "otlphttp/evil"} {
		// Only test types that are truly unauthorized (not otlphttp).
		if allowedExporterTypes[exporterType(name)] {
			continue
		}
		conf := newConf(map[string]any{
			"exporters": map[string]any{
				name: map[string]any{},
			},
		})
		c := &converter{}
		err := c.Convert(context.Background(), conf)
		if err == nil {
			t.Errorf("expected error for exporter %q, got nil", name)
		}
	}
}

// Bad endpoint on otlphttp exporter must be rejected.
func TestBadEndpointRejected(t *testing.T) {
	cases := []struct {
		name     string
		field    string
		endpoint string
	}{
		{"otlphttp/xray", "traces_endpoint", "https://evil.example.com/v1/traces"},
		{"otlphttp/custom", "endpoint", "https://attacker.io:4318"},
		{"otlphttp/logs", "logs_endpoint", "http://localhost:4318"},
	}
	for _, tc := range cases {
		conf := newConf(map[string]any{
			"exporters": map[string]any{
				tc.name: map[string]any{
					tc.field: tc.endpoint,
				},
			},
		})
		c := &converter{}
		err := c.Convert(context.Background(), conf)
		if err == nil {
			t.Errorf("expected error for exporter %q endpoint %q, got nil", tc.name, tc.endpoint)
		}
		if err != nil && !strings.Contains(err.Error(), "does not match the required pattern") {
			t.Errorf("unexpected error message: %v", err)
		}
	}
}

// Valid AWS endpoints must pass.
func TestAWSEndpointsPass(t *testing.T) {
	cases := []struct {
		field    string
		endpoint string
	}{
		{"traces_endpoint", "https://xray.us-east-1.amazonaws.com/v1/traces"},
		{"logs_endpoint", "https://logs.eu-west-1.amazonaws.com/v1/logs"},
		{"traces_endpoint", "https://xray.cn-north-1.amazonaws.com.cn/v1/traces"},
	}
	for _, tc := range cases {
		conf := newConf(map[string]any{
			"exporters": map[string]any{
				"otlphttp/test": map[string]any{
					tc.field: tc.endpoint,
				},
			},
		})
		c := &converter{}
		if err := c.Convert(context.Background(), conf); err != nil {
			t.Errorf("field %q endpoint %q should pass, got: %v", tc.field, tc.endpoint, err)
		}
	}
}

// traces_endpoint must start with https://xray.
func TestTracesEndpointMustBeXRay(t *testing.T) {
	conf := newConf(map[string]any{
		"exporters": map[string]any{
			"otlphttp/traces": map[string]any{
				"traces_endpoint": "https://logs.us-east-1.amazonaws.com/v1/traces",
			},
		},
	})
	c := &converter{}
	if err := c.Convert(context.Background(), conf); err == nil {
		t.Fatal("expected error: traces_endpoint pointing to logs service should be rejected")
	}
}

// logs_endpoint must start with https://logs.
func TestLogsEndpointMustBeLogs(t *testing.T) {
	conf := newConf(map[string]any{
		"exporters": map[string]any{
			"otlphttp/logs": map[string]any{
				"logs_endpoint": "https://xray.us-east-1.amazonaws.com/v1/logs",
			},
		},
	})
	c := &converter{}
	if err := c.Convert(context.Background(), conf); err == nil {
		t.Fatal("expected error: logs_endpoint pointing to xray service should be rejected")
	}
}

// Correct field-specific endpoints must pass.
func TestFieldSpecificEndpointsPass(t *testing.T) {
	conf := newConf(map[string]any{
		"exporters": map[string]any{
			"otlphttp/out": map[string]any{
				"traces_endpoint": "https://xray.ap-southeast-1.amazonaws.com/v1/traces",
				"logs_endpoint":   "https://logs.ap-southeast-1.amazonaws.com/v1/logs",
			},
		},
	})
	c := &converter{}
	if err := c.Convert(context.Background(), conf); err != nil {
		t.Fatalf("expected valid field-specific endpoints to pass, got: %v", err)
	}
}

// awsemf endpoint override must also be validated.
func TestAWSEMFEndpointOverrideValidated(t *testing.T) {
	conf := newConf(map[string]any{
		"exporters": map[string]any{
			"awsemf": map[string]any{
				"endpoint": "https://evil.example.com",
			},
		},
	})
	c := &converter{}
	if err := c.Convert(context.Background(), conf); err == nil {
		t.Fatal("expected error for awsemf bad endpoint override")
	}
}

// awsemf with valid endpoint override should pass.
func TestAWSEMFValidEndpointPass(t *testing.T) {
	conf := newConf(map[string]any{
		"exporters": map[string]any{
			"awsemf": map[string]any{
				"endpoint": "https://logs.us-west-2.amazonaws.com",
			},
		},
	})
	c := &converter{}
	if err := c.Convert(context.Background(), conf); err != nil {
		t.Fatalf("expected valid awsemf endpoint to pass, got: %v", err)
	}
}

// Connector used as pipeline exporter should pass.
func TestConnectorAsPipelineExporterPass(t *testing.T) {
	conf := newConf(map[string]any{
		"connectors": map[string]any{
			"genaiadapterconnector": map[string]any{},
		},
		"service": map[string]any{
			"pipelines": map[string]any{
				"traces/input": map[string]any{
					"exporters": []any{"genaiadapterconnector"},
				},
			},
		},
	})
	c := &converter{}
	if err := c.Convert(context.Background(), conf); err != nil {
		t.Fatalf("expected connector in pipeline to pass, got: %v", err)
	}
}

// Unauthorized exporter type in pipeline must be rejected.
func TestPipelineUnauthorizedTypeRejected(t *testing.T) {
	conf := newConf(map[string]any{
		"service": map[string]any{
			"pipelines": map[string]any{
				"traces": map[string]any{
					"exporters": []any{"otlphttp/xray", "debug"},
				},
			},
		},
	})
	c := &converter{}
	err := c.Convert(context.Background(), conf)
	if err == nil {
		t.Fatal("expected error for unauthorized type in pipeline")
	}
	if !strings.Contains(err.Error(), "debug") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

// Undefined connector used as pipeline exporter must be rejected.
func TestPipelineUndefinedConnectorRejected(t *testing.T) {
	conf := newConf(map[string]any{
		"service": map[string]any{
			"pipelines": map[string]any{
				"traces": map[string]any{
					"exporters": []any{"unknownconnector"},
				},
			},
		},
	})
	c := &converter{}
	err := c.Convert(context.Background(), conf)
	if err == nil {
		t.Fatal("expected error for undefined connector in pipeline")
	}
}

func TestNoExportersSectionPass(t *testing.T) {
	conf := newConf(map[string]any{
		"receivers": map[string]any{"otlp": map[string]any{}},
	})
	c := &converter{}
	if err := c.Convert(context.Background(), conf); err != nil {
		t.Fatalf("expected no error when exporters section is absent, got: %v", err)
	}
}

func TestEmptyConfigPass(t *testing.T) {
	conf := newConf(map[string]any{})
	c := &converter{}
	if err := c.Convert(context.Background(), conf); err != nil {
		t.Fatalf("expected no error for empty config, got: %v", err)
	}
}
