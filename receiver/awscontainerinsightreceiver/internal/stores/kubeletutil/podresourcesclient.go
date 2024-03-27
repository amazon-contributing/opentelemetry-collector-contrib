// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package kubeletutil // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/stores/kubeletutil"

import (
	"context"
	"fmt"
	"net"
	"os"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	podresourcesapi "k8s.io/kubelet/pkg/apis/podresources/v1"
)

const (
	socketPath        = "/var/lib/kubelet/pod-resources/kubelet.sock"
	connectionTimeout = 10 * time.Second
)

type PodResourcesClient struct {
	delegateClient podresourcesapi.PodResourcesListerClient
	conn           *grpc.ClientConn
}

func NewPodResourcesClient() (*PodResourcesClient, error) {
	podResourcesClient := &PodResourcesClient{}

	conn, err := podResourcesClient.connectToServer(socketPath)
	podResourcesClient.conn = conn
	if err != nil {
		return nil, fmt.Errorf("failed to connect to server: %w", err)
	}

	podResourcesClient.delegateClient = podresourcesapi.NewPodResourcesListerClient(conn)

	return podResourcesClient, nil
}

func (p *PodResourcesClient) connectToServer(socket string) (*grpc.ClientConn, error) {
	err := validateSocket(socket)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), connectionTimeout)
	defer cancel()

	conn, err := grpc.DialContext(ctx,
		socket,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
		grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
			d := net.Dialer{}
			return d.DialContext(ctx, "unix", addr)
		}),
	)

	if err != nil {
		return nil, fmt.Errorf("failure connecting to '%s': %w", socket, err)
	}

	return conn, nil
}

func validateSocket(socket string) error {
	info, err := os.Stat(socket)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("socket path does not exist: %s", socket)
		}
		return fmt.Errorf("failed to check socket path: %w", err)
	}

	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}

	if stat.Uid != 0 {
		return fmt.Errorf("socket path %s is owned by %d, not root", socket, stat.Uid)
	}
	perms := info.Mode().Perm()
	if perms&0002 != 0 {
		return fmt.Errorf("socket path %s is writeable by anyone - permissions: %s", socket, perms)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("socket path %s is not a socket - mode: %s", socket, info.Mode())
	}

	return nil
}

func (p *PodResourcesClient) ListPods() (*podresourcesapi.ListPodResourcesResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), connectionTimeout)
	defer cancel()

	resp, err := p.delegateClient.List(ctx, &podresourcesapi.ListPodResourcesRequest{})
	if err != nil {
		return nil, fmt.Errorf("failure getting pod resources: %w", err)
	}

	return resp, nil
}

func (p *PodResourcesClient) Shutdown() {
	err := p.conn.Close()
	if err != nil {
		return
	}
}
