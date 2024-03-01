// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

//go:build windows
// +build windows

package containerinsight

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHostProcessContainer(t *testing.T) {
	os.Setenv(RunInContainer, "True")
	assert.Equal(t, IsWindowsHostProcessContainer(), false)

	os.Setenv(RunAsHostProcessContainer, "True")
	assert.Equal(t, IsWindowsHostProcessContainer(), true)

	os.Unsetenv(RunInContainer)
	os.Unsetenv(RunAsHostProcessContainer)
}
