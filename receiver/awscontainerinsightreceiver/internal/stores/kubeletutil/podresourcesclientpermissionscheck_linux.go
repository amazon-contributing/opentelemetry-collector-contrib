// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

//go:build !windows
// +build !windows

package kubeletutil

import (
	"fmt"
	"os"
	"syscall"
)

func checkPodResourcesSocketPermissions(info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("couldn't check permissions")
	}

	if stat.Uid != 0 {
		return fmt.Errorf("owned by %d, not root", stat.Uid)
	}
	perms := info.Mode().Perm()
	if perms&0002 != 0 {
		return fmt.Errorf("writeable by anyone - permissions: %s", perms)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("not a socket - mode: %s", info.Mode())
	}

	return nil
}
