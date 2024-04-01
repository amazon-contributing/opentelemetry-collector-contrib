// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

//go:build !windows
// +build !windows

package efa // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/efa"

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

func checkPermissions(info os.FileInfo) (bool, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false, errors.New("couldn't read permissions")
	}

	if stat.Uid != 0 {
		return false, fmt.Errorf("not owned by root, owned by uid %d", stat.Uid)
	}
	perms := info.Mode().Perm()
	if perms&0002 != 0 {
		return false, fmt.Errorf("writeable by anyone, permissions: %s", perms.String())
	}

	return true, nil
}
