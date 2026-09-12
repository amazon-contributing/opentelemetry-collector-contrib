// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package efa // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/efa"

import (
	"errors"
	"net"
)

// IPv6LinkLocalToMAC converts an IPv6 link-local address to its corresponding MAC address.
// The IPv6 address must be in EUI-64 format for this conversion to work.
func IPv6LinkLocalToMAC(ipv6Addr string) (string, error) {
	// Parse the IPv6 address
	ip := net.ParseIP(ipv6Addr)
	if ip == nil || ip.To16() == nil {
		return "", errors.New("invalid IPv6 address")
	}

	// Verify it's a link-local address (fe80::/10)
	if !ip.IsLinkLocalUnicast() {
		return "", errors.New("not a link-local address")
	}

	// Extract interface identifier (last 64 bits).
	// net.IP.To16() always returns exactly 16 bytes (or nil, handled above).
	full := ip.To16()
	if len(full) < 16 {
		return "", errors.New("invalid interface identifier")
	}

	// Verify EUI-64 format (check for ff:fe in interface ID bytes 3-4)
	if full[11] != 0xff || full[12] != 0xfe {
		return "", errors.New("address does not use EUI-64 format")
	}

	// Reconstruct MAC address from EUI-64 interface identifier (bytes 8-15)
	mac := net.HardwareAddr{
		full[8] ^ 0x02, // XOR with 0b00000010 to invert Universal/Local bit
		full[9],
		full[10],
		full[13],
		full[14],
		full[15],
	}

	return mac.String(), nil
}
