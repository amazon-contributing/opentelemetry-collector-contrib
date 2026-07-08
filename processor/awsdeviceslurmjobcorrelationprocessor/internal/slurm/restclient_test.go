// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package slurm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestRESTClient_GetJobsOnNode(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/slurm/v0.0.41/jobs", r.URL.Path)
		assert.Equal(t, "gpu-node-1", r.URL.Query().Get("node_list"))

		resp := slurmJobsResponse{
			Jobs: []slurmJob{
				{JobID: 100, Name: "training", UserName: "alice", Account: "ml", Partition: "gpu", JobState: "RUNNING", GRESDetail: "gpu:t4:1(IDX:0)"},
				{JobID: 101, Name: "queued", UserName: "bob", Account: "ml", Partition: "gpu", JobState: "PENDING"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	client := NewRESTClient(server.URL, zaptest.NewLogger(t))
	jobs, err := client.GetJobsOnNode("gpu-node-1")
	require.NoError(t, err)

	// Only RUNNING jobs should be returned
	require.Len(t, jobs, 1)
	assert.Equal(t, "100", jobs[0].JobID)
	assert.Equal(t, "training", jobs[0].JobName)
	assert.Equal(t, "alice", jobs[0].User)
	assert.Equal(t, "ml", jobs[0].Account)
	assert.Equal(t, "gpu", jobs[0].Partition)
	assert.Equal(t, "gpu:t4:1(IDX:0)", jobs[0].GRESDetail)
}

func TestRESTClient_GetJob(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/slurm/v0.0.41/job/42", r.URL.Path)

		resp := slurmJobsResponse{
			Jobs: []slurmJob{
				{JobID: 42, Name: "inference", UserName: "carol", Account: "prod", Partition: "gpu-queue", JobState: "RUNNING"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	client := NewRESTClient(server.URL, zaptest.NewLogger(t))
	job, err := client.GetJob("42")
	require.NoError(t, err)
	assert.Equal(t, "42", job.JobID)
	assert.Equal(t, "inference", job.JobName)
	assert.Equal(t, "carol", job.User)
}

func TestRESTClient_GetJob_NotFound(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := slurmJobsResponse{Jobs: []slurmJob{}}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	client := NewRESTClient(server.URL, zaptest.NewLogger(t))
	_, err := client.GetJob("999")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestRESTClient_ServerError(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	client := NewRESTClient(server.URL, zaptest.NewLogger(t))
	_, err := client.GetJobsOnNode("node-1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}
