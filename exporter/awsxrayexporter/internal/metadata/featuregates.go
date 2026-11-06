// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package metadata // import "github.com/open-telemetry/opentelemetry-collector-contrib/exporter/awsxrayexporter/internal/metadata"

// Feature gates for the semantic convention migration. Upstream generates these
// from metadata.yaml via mdatagen; this fork registers them by hand (see
// skipTimestampValidationFeatureGate in factory.go, which stays registered there).

import (
	"go.opentelemetry.io/collector/featuregate"
)

var ExporterAwsxrayDontEmitV0DBConventionsFeatureGate = featuregate.GlobalRegistry().MustRegister(
	"exporter.awsxray.DontEmitV0DBConventions",
	featuregate.StageAlpha,
	featuregate.WithRegisterDescription("When enabled, the AWS X-Ray exporter no longer reads deprecated semconv v1.12.0 database attribute keys. Requires exporter.awsxray.EmitV1DBConventions to also be enabled."),
	featuregate.WithRegisterReferenceURL("https://github.com/open-telemetry/opentelemetry-collector-contrib/issues/22095"),
	featuregate.WithRegisterFromVersion("v0.150.0"),
)

var ExporterAwsxrayDontEmitV0HTTPNetworkConventionsFeatureGate = featuregate.GlobalRegistry().MustRegister(
	"exporter.awsxray.DontEmitV0HTTPNetworkConventions",
	featuregate.StageAlpha,
	featuregate.WithRegisterDescription("When enabled, the AWS X-Ray exporter no longer reads deprecated semconv v1.12.0 HTTP and network attribute keys. Requires exporter.awsxray.EmitV1HTTPNetworkConventions to also be enabled."),
	featuregate.WithRegisterReferenceURL("https://github.com/open-telemetry/opentelemetry-collector-contrib/issues/22095"),
	featuregate.WithRegisterFromVersion("v0.150.0"),
)

var ExporterAwsxrayDontEmitV0RPCConventionsFeatureGate = featuregate.GlobalRegistry().MustRegister(
	"exporter.awsxray.DontEmitV0RPCConventions",
	featuregate.StageAlpha,
	featuregate.WithRegisterDescription("When enabled, the AWS X-Ray exporter no longer reads deprecated rpc.service and peer.service for generic RPC spans. Requires exporter.awsxray.EmitV1RPCConventions to also be enabled. Documented AWS SDK conventions remain supported."),
	featuregate.WithRegisterReferenceURL("https://github.com/open-telemetry/opentelemetry-collector-contrib/issues/22095"),
	featuregate.WithRegisterFromVersion("v0.150.0"),
)

var ExporterAwsxrayEmitV1DBConventionsFeatureGate = featuregate.GlobalRegistry().MustRegister(
	"exporter.awsxray.EmitV1DBConventions",
	featuregate.StageAlpha,
	featuregate.WithRegisterDescription("When enabled, the AWS X-Ray exporter reads database span attributes using semconv v1.43.0 keys (db.namespace, db.system.name, db.query.text), preferring them over legacy keys."),
	featuregate.WithRegisterReferenceURL("https://github.com/open-telemetry/opentelemetry-collector-contrib/issues/22095"),
	featuregate.WithRegisterFromVersion("v0.150.0"),
)

var ExporterAwsxrayEmitV1HTTPNetworkConventionsFeatureGate = featuregate.GlobalRegistry().MustRegister(
	"exporter.awsxray.EmitV1HTTPNetworkConventions",
	featuregate.StageAlpha,
	featuregate.WithRegisterDescription("When enabled, the AWS X-Ray exporter reads HTTP and network span attributes using semconv v1.43.0 keys (e.g. http.request.method, server.address, server.port, client.address, messaging.message.body.size) instead of the deprecated v1.12.0 keys when both are present."),
	featuregate.WithRegisterReferenceURL("https://github.com/open-telemetry/opentelemetry-collector-contrib/issues/22095"),
	featuregate.WithRegisterFromVersion("v0.150.0"),
)

var ExporterAwsxrayEmitV1RPCConventionsFeatureGate = featuregate.GlobalRegistry().MustRegister(
	"exporter.awsxray.EmitV1RPCConventions",
	featuregate.StageAlpha,
	featuregate.WithRegisterDescription("When enabled, the AWS X-Ray exporter reads the semconv v1.43.0 release candidate RPC conventions, using rpc.system.name and service.peer.name and extracting service names from fully-qualified rpc.method values. Documented AWS SDK conventions remain supported."),
	featuregate.WithRegisterReferenceURL("https://github.com/open-telemetry/opentelemetry-collector-contrib/issues/22095"),
	featuregate.WithRegisterFromVersion("v0.150.0"),
)
