// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

//go:build !windows
// +build !windows

package host

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/shirou/gopsutil/v4/common"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestNodeCapacity(t *testing.T) {
	lstatOption := Option(func(h any) {
		if nc, ok := h.(*nodeCapacity); ok {
			nc.osLstat = func(string) (os.FileInfo, error) {
				return nil, os.ErrNotExist
			}
		}
	})

	nc, err := newNodeCapacity(zap.NewNop(), lstatOption)
	assert.Nil(t, nc)
	assert.Error(t, err)

	// can't set environment variables
	lstatOption = Option(func(h any) {
		if nc, ok := h.(*nodeCapacity); ok {
			nc.osLstat = func(string) (os.FileInfo, error) {
				return nil, nil
			}
		}
	})

	virtualMemOption := Option(func(h any) {
		if nc, ok := h.(*nodeCapacity); ok {
			nc.virtualMemory = func(context.Context) (*mem.VirtualMemoryStat, error) {
				return nil, errors.New("error")
			}
		}
	})

	cpuInfoOption := Option(func(h any) {
		if nc, ok := h.(*nodeCapacity); ok {
			nc.cpuInfo = func(context.Context) ([]cpu.InfoStat, error) {
				return nil, errors.New("error")
			}
		}
	})

	nc, err = newNodeCapacity(zap.NewNop(), lstatOption, virtualMemOption, cpuInfoOption)
	assert.NotNil(t, nc)
	assert.NoError(t, err)
	assert.Equal(t, int64(0), nc.getMemoryCapacity())
	assert.Equal(t, int64(0), nc.getNumCores())

	// normal case where everything is working
	virtualMemOption = Option(func(h any) {
		if nc, ok := h.(*nodeCapacity); ok {
			nc.virtualMemory = func(context.Context) (*mem.VirtualMemoryStat, error) {
				return &mem.VirtualMemoryStat{
					Total: 1024,
				}, nil
			}
		}
	})

	cpuInfoOption = Option(func(h any) {
		if nc, ok := h.(*nodeCapacity); ok {
			nc.cpuInfo = func(context.Context) ([]cpu.InfoStat, error) {
				return []cpu.InfoStat{
					{},
					{},
				}, nil
			}
		}
	})

	nc, err = newNodeCapacity(zap.NewNop(), lstatOption, virtualMemOption, cpuInfoOption)
	assert.NotNil(t, nc)
	assert.NoError(t, err)
	assert.Equal(t, int64(1024), nc.getMemoryCapacity())
	assert.Equal(t, int64(2), nc.getNumCores())
}

func TestNodeCapacity_ReadsHostProcEnvVar(t *testing.T) {
	const customHostProc = "/custom/host/proc"
	t.Setenv(string(common.HostProcEnvKey), customHostProc)

	lstatOption := Option(func(h any) {
		if nc, ok := h.(*nodeCapacity); ok {
			nc.osLstat = func(name string) (os.FileInfo, error) {
				assert.Equal(t, customHostProc, name)
				return nil, nil
			}
		}
	})

	nc, err := newNodeCapacity(zap.NewNop(), lstatOption)
	assert.NoError(t, err)
	assert.NotNil(t, nc)
}
