// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package sqlserverreceiver

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCategorizeWaitType(t *testing.T) {
	tests := []struct {
		waitType string
		expected string
	}{
		// CPU waits
		{"SOS_SCHEDULER_YIELD", "CPU"},
		{"", "CPU"},

		// Lock waits
		{"LCK_M_S", "Lock"},
		{"LCK_M_U", "Lock"},
		{"LCK_M_X", "Lock"},
		{"LCK_M_SCH_S", "Lock"},
		{"LCK_M_SCH_M", "Lock"},

		// Latch waits
		{"PAGELATCH_SH", "Latch"},
		{"PAGELATCH_UP", "Latch"},
		{"PAGELATCH_EX", "Latch"},
		{"LATCH_EX", "Latch"},
		{"AUDIT_ON_DEMAND_TARGET_LOCK", "Latch"},

		// I/O waits
		{"ASYNC_IO_COMPLETION", "IO"},
		{"IO_COMPLETION", "IO"},
		{"ASYNC_DISKPOOL_LOCK", "IO"},
		{"PAGEIOLATCH_SH", "IO"},
		{"PAGEIOLATCH_UP", "IO"},
		{"PAGEIOLATCH_EX", "IO"},

		// Log waits
		{"LOGBUFFER", "Log"},
		{"WRITELOG", "Log"},

		// Network waits
		{"ASYNC_NETWORK_IO", "Network"},

		// Memory waits
		{"CMEMTHREAD", "Memory"},
		{"BAD_PAGE_PROCESS", "Memory"},

		// Remote waits
		{"OLEDB", "Remote"},

		// Idle waits
		{"BROKER_RECEIVE_WAITFOR", "Idle"},
		{"BROKER_TASK_STOP", "Idle"},
		{"PREEMPTIVE_XE_GETTARGETSTATE", "Idle"},

		// Other/Unknown waits
		{"SLEEP_TASK", "Other"},
		{"WAITFOR", "Other"},
		{"UNKNOWN_WAIT_TYPE", "Other"},
		{"THREADPOOL", "Other"},
		{"SOS_WORK_DISPATCHER", "Other"},
		{"NET_WAITFOR_PACKET", "Other"},
		{"RESOURCE_SEMAPHORE", "Other"},
		{"SOS_RESERVEDMEMBLOCKLIST", "Other"},
	}

	for _, tt := range tests {
		t.Run(tt.waitType, func(t *testing.T) {
			result := categorizeWaitType(tt.waitType)
			assert.Equal(t, tt.expected, result, "Wait type %q should be categorized as %q", tt.waitType, tt.expected)
		})
	}
}
