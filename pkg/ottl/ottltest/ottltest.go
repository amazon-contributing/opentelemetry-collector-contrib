// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package ottltest // import "github.com/open-telemetry/opentelemetry-collector-contrib/pkg/ottl/ottltest"

//go:fix inline
func Strp(s string) *string {
	return new(s)
}

//go:fix inline
func Floatp(f float64) *float64 {
	return new(f)
}

//go:fix inline
func Intp(i int64) *int64 {
	return new(i)
}

//go:fix inline
func Boolp(b bool) *bool {
	return new(b)
}
