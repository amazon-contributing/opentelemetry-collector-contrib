// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

//go:build windows
// +build windows

package efa

func checkPermissions(info os.FileInfo) (bool, error) {
	return false, errors.New("not implemented on Windows")
}
