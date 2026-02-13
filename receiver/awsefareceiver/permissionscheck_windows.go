// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

//go:build windows
// +build windows

package awsefareceiver // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awsefareceiver"

import (
	"errors"
	"os"
)

func checkPermissions(_ os.FileInfo) error {
	return errors.New("not implemented on Windows")
}
