// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package slurm // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/awsdeviceslurmjobcorrelationprocessor/internal/slurm"

import (
	"fmt"
	"os/exec"
	"strings"

	"go.uber.org/zap"
)

// ScontrolClient queries job metadata via the scontrol CLI.
// This works on any node with slurm commands in PATH and connectivity
// to slurmctld — no slurmrestd or slurmdbd required.
type ScontrolClient struct {
	scontrolPath string
	logger       *zap.Logger
}

// NewScontrolClient creates a client that uses scontrol to fetch job metadata.
func NewScontrolClient(logger *zap.Logger) *ScontrolClient {
	path, err := exec.LookPath("scontrol")
	if err != nil {
		path = "/opt/slurm/bin/scontrol"
	}
	return &ScontrolClient{
		scontrolPath: path,
		logger:       logger,
	}
}

// GetJob fetches job metadata by calling `scontrol show job {id}`.
func (c *ScontrolClient) GetJob(jobID string) (*JobInfo, error) {
	out, err := exec.Command(c.scontrolPath, "show", "job", jobID).Output()
	if err != nil {
		return nil, fmt.Errorf("scontrol show job %s: %w", jobID, err)
	}
	return parseScontrolOutput(string(out), jobID)
}

// GetJobsOnNode fetches all running jobs on a node.
// It calls `scontrol show job` and filters by NodeList containing this node.
func (c *ScontrolClient) GetJobsOnNode(nodeName string) ([]*JobInfo, error) {
	out, err := exec.Command(c.scontrolPath, "show", "job").Output()
	if err != nil {
		return nil, fmt.Errorf("scontrol show job: %w", err)
	}
	allJobs, err := parseScontrolOutputMulti(string(out))
	if err != nil {
		return nil, err
	}
	var nodeJobs []*JobInfo
	for _, job := range allJobs {
		if jobRunsOnNode(job, nodeName) {
			nodeJobs = append(nodeJobs, job)
		}
	}
	return nodeJobs, nil
}

// parseScontrolOutput parses the key=value output from `scontrol show job`.
func parseScontrolOutput(output string, jobID string) (*JobInfo, error) {
	fields := parseScontrolFields(output)
	if len(fields) == 0 {
		return nil, fmt.Errorf("no fields parsed from scontrol output for job %s", jobID)
	}

	return &JobInfo{
		JobID:      getFieldOr(fields, "JobId", jobID),
		JobName:    getFieldOr(fields, "JobName", ""),
		User:       extractUser(getFieldOr(fields, "UserId", "")),
		Account:    getFieldOr(fields, "Account", ""),
		Partition:  getFieldOr(fields, "Partition", ""),
		GRESDetail: getFieldOr(fields, "TresPerNode", ""),
		NodeList:   getFieldOr(fields, "NodeList", ""),
	}, nil
}

func parseScontrolOutputMulti(output string) ([]*JobInfo, error) {
	// Jobs are separated by blank lines
	blocks := strings.Split(output, "\n\n")
	var jobs []*JobInfo
	for _, block := range blocks {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		fields := parseScontrolFields(block)
		state := getFieldOr(fields, "JobState", "")
		if state != "RUNNING" {
			continue
		}
		jobs = append(jobs, &JobInfo{
			JobID:      getFieldOr(fields, "JobId", ""),
			JobName:    getFieldOr(fields, "JobName", ""),
			User:       extractUser(getFieldOr(fields, "UserId", "")),
			Account:    getFieldOr(fields, "Account", ""),
			Partition:  getFieldOr(fields, "Partition", ""),
			GRESDetail: getFieldOr(fields, "TresPerNode", ""),
			NodeList:   getFieldOr(fields, "NodeList", ""),
		})
	}
	return jobs, nil
}

// parseScontrolFields parses "Key=Value" pairs from scontrol output.
// Handles multi-line output where fields are separated by spaces or newlines.
func parseScontrolFields(output string) map[string]string {
	fields := make(map[string]string)
	// Replace newlines with spaces for uniform parsing
	output = strings.ReplaceAll(output, "\n", " ")
	// Split on spaces, then find key=value pairs
	for _, token := range strings.Fields(output) {
		idx := strings.Index(token, "=")
		if idx > 0 {
			key := token[:idx]
			value := token[idx+1:]
			fields[key] = value
		}
	}
	return fields
}

// extractUser extracts the username from "user(uid)" format.
// e.g., "ec2-user(1000)" → "ec2-user"
func extractUser(userField string) string {
	if idx := strings.Index(userField, "("); idx > 0 {
		return userField[:idx]
	}
	return userField
}

func getFieldOr(fields map[string]string, key, fallback string) string {
	if v, ok := fields[key]; ok && v != "" && v != "(null)" {
		return v
	}
	return fallback
}

// jobRunsOnNode checks if a job's NodeList contains the given node name.
// NodeList can be a simple name or a Slurm hostlist expression like
// "gpu-queue-st-gpu-efa-nodes-[1-2]".
func jobRunsOnNode(job *JobInfo, nodeName string) bool {
	nodeList := job.NodeList
	if nodeList == "" {
		return false
	}
	// Simple case: exact match or comma-separated list
	if strings.Contains(nodeList, nodeName) {
		return true
	}
	// Hostlist expression: expand "prefix-[1-2]" to check membership.
	// Find the bracket expression and expand.
	bracketIdx := strings.Index(nodeList, "[")
	if bracketIdx < 0 {
		return false
	}
	prefix := nodeList[:bracketIdx]
	if !strings.HasPrefix(nodeName, prefix) {
		return false
	}
	suffix := strings.TrimPrefix(nodeName, prefix)
	// Extract the range from brackets
	closeBracket := strings.Index(nodeList[bracketIdx:], "]")
	if closeBracket < 0 {
		return false
	}
	rangeStr := nodeList[bracketIdx+1 : bracketIdx+closeBracket]
	for _, part := range strings.Split(rangeStr, ",") {
		if strings.Contains(part, "-") {
			bounds := strings.SplitN(part, "-", 2)
			if len(bounds) == 2 && suffix >= bounds[0] && suffix <= bounds[1] {
				return true
			}
		} else if part == suffix {
			return true
		}
	}
	return false
}
