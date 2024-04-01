// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

//go:build windows
// +build windows

package kubeletutil

import (
	"os"
)

func checkPodResourcesSocketPermissions(info os.FileInfo) error {
	return errors.New("not implemented on Windows")
}
