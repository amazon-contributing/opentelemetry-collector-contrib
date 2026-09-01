// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

//go:build windows
// +build windows

package extractors // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/k8swindows/extractors"

import (
	"time"

	"github.com/Microsoft/hcsshim"
)

// HCSNetworkStat Network Stat from HCS.
type HCSNetworkStat struct {
	Name                   string
	BytesReceived          uint64
	BytesSent              uint64
	DroppedPacketsIncoming uint64
	DroppedPacketsOutgoing uint64
}

// HCSStat Stats from HCS.
type HCSStat struct {
	Time time.Time
	Id   string //nolint:revive
	Name string

	CPU *hcsshim.ProcessorStats

	Network *[]HCSNetworkStat
}

// convertHCSNetworkStats Convert HCS network system stats to Raw network stats
func convertHCSNetworkStats(stat HCSStat, networkStat HCSNetworkStat) NetworkStat {
	var networkstat NetworkStat

	networkstat.Time = stat.Time

	networkstat.Name = networkStat.Name
	networkstat.TxBytes = networkStat.BytesSent
	networkstat.RxBytes = networkStat.BytesReceived
	networkstat.DroppedIncoming = networkStat.DroppedPacketsIncoming
	networkstat.DroppedOutgoing = networkStat.DroppedPacketsOutgoing

	return networkstat
}

// ConvertHCSContainerToRaw Converts HCS Container stats to RawMetric.
func ConvertHCSContainerToRaw(containerStat HCSStat) RawMetric {
	var rawMetic RawMetric

	rawMetic.Id = containerStat.Id
	rawMetic.Name = containerStat.Name
	rawMetic.Time = containerStat.Time

	if containerStat.Network != nil {
		for _, val := range *containerStat.Network {
			rawMetic.NetworkStats = append(rawMetic.NetworkStats, convertHCSNetworkStats(containerStat, val))
		}
	}

	return rawMetic
}
