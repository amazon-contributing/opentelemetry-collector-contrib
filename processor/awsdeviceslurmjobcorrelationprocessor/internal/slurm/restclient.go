// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package slurm // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/awsdeviceslurmjobcorrelationprocessor/internal/slurm"

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// RESTClient queries slurmrestd for job metadata.
type RESTClient struct {
	endpoint   string
	httpClient *http.Client
	logger     *zap.Logger
}

// NewRESTClient creates a slurmrestd client.
func NewRESTClient(endpoint string, logger *zap.Logger) *RESTClient {
	return &RESTClient{
		endpoint: endpoint,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		logger: logger,
	}
}

// slurmJobsResponse represents the slurmrestd /slurm/v0.0.41/jobs response.
type slurmJobsResponse struct {
	Jobs []slurmJob `json:"jobs"`
}

type slurmJob struct {
	JobID      int64  `json:"job_id"`
	Name       string `json:"name"`
	UserName   string `json:"user_name"`
	Account    string `json:"account"`
	Partition  string `json:"partition"`
	JobState   string `json:"job_state"`
	GRESDetail string `json:"gres_detail"`
	Nodes      string `json:"nodes"`
}

// GetJobsOnNode queries slurmrestd for all jobs running on a specific node.
func (c *RESTClient) GetJobsOnNode(nodeName string) ([]*JobInfo, error) {
	url := fmt.Sprintf("%s/slurm/v0.0.41/jobs?node_list=%s", c.endpoint, nodeName)
	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("querying slurmrestd: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("slurmrestd returned %d: %s", resp.StatusCode, string(body))
	}

	var slurmResp slurmJobsResponse
	if err := json.NewDecoder(resp.Body).Decode(&slurmResp); err != nil {
		return nil, fmt.Errorf("decoding slurmrestd response: %w", err)
	}

	var jobs []*JobInfo
	for _, j := range slurmResp.Jobs {
		if j.JobState != "RUNNING" {
			continue
		}
		jobs = append(jobs, &JobInfo{
			JobID:      fmt.Sprintf("%d", j.JobID),
			JobName:    j.Name,
			User:       j.UserName,
			Account:    j.Account,
			Partition:  j.Partition,
			GRESDetail: j.GRESDetail,
		})
	}
	return jobs, nil
}

// GetJob queries slurmrestd for a specific job by ID.
func (c *RESTClient) GetJob(jobID string) (*JobInfo, error) {
	url := fmt.Sprintf("%s/slurm/v0.0.41/job/%s", c.endpoint, jobID)
	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("querying slurmrestd for job %s: %w", jobID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("slurmrestd returned %d for job %s: %s", resp.StatusCode, jobID, string(body))
	}

	var slurmResp slurmJobsResponse
	if err := json.NewDecoder(resp.Body).Decode(&slurmResp); err != nil {
		return nil, fmt.Errorf("decoding slurmrestd response for job %s: %w", jobID, err)
	}

	if len(slurmResp.Jobs) == 0 {
		return nil, fmt.Errorf("job %s not found", jobID)
	}

	j := slurmResp.Jobs[0]
	return &JobInfo{
		JobID:      fmt.Sprintf("%d", j.JobID),
		JobName:    j.Name,
		User:       j.UserName,
		Account:    j.Account,
		Partition:  j.Partition,
		GRESDetail: j.GRESDetail,
	}, nil
}
