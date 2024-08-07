// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package k8sutil // import "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/k8s/k8sutil"

import (
	"fmt"
	"strings"
)

// CreatePodKey concatenates namespace and podName to get a pod key
func CreatePodKey(namespace, podName string) string {
	if namespace == "" || podName == "" {
		return ""
	}
	return fmt.Sprintf("namespace:%s,podName:%s", namespace, podName)
}

// CreateContainerKey concatenates namespace, podName and containerName to get a container key
func CreateContainerKey(namespace, podName, containerName string) string {
	if namespace == "" || podName == "" || containerName == "" {
		return ""
	}
	return fmt.Sprintf("namespace:%s,podName:%s,containerName:%s", namespace, podName, containerName)
}

// ParseInstanceIDFromProviderID parses EC2 instance id from node's provider id which has format of aws:///<subnet>/<instanceId>
func ParseInstanceIDFromProviderID(providerID string) string {
	if providerID == "" || !strings.HasPrefix(providerID, "aws://") {
		return ""
	}
	return providerID[strings.LastIndex(providerID, "/")+1:]
}
