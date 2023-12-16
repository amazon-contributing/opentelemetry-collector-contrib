// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

//go:build !linux
// +build !linux

package host

// These variables are invalid for Windows
const (
	rootfs     = ""
	hostProc   = rootfs + ""
	hostMounts = hostProc + ""
)
