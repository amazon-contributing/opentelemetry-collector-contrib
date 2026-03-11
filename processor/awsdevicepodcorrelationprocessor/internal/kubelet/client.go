// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package kubelet

import (
	"context"
	"fmt"
	"net"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	podresourcesapi "k8s.io/kubelet/pkg/apis/podresources/v1"
)

const (
	defaultSocketPath    = "/var/lib/kubelet/pod-resources/kubelet.sock"
	connectionTimeout    = 10 * time.Second
	defaultRefreshInterval = 10 * time.Second
)

// ContainerInfo holds Kubernetes pod/container metadata for a device.
type ContainerInfo struct {
	PodName       string
	ContainerName string
	Namespace     string
}

// Client connects to the Kubelet Pod Resources API and maintains
// an in-memory cache of device-to-pod mappings.
type Client struct {
	conn           *grpc.ClientConn
	listerClient   podresourcesapi.PodResourcesListerClient
	resourceNames  map[string]struct{}
	deviceToPod    map[deviceKey]ContainerInfo
	ctx            context.Context
	cancel         context.CancelFunc
	socketPath     string
	refreshInterval time.Duration
}

type deviceKey struct {
	DeviceID     string
	ResourceName string
}

// ClientOption configures the Client.
type ClientOption func(*Client)

// WithSocketPath sets a custom kubelet socket path.
func WithSocketPath(path string) ClientOption {
	if path == "" {
		path = defaultSocketPath
	}
	return func(c *Client) { c.socketPath = path }
}

// NewClient creates a new Kubelet Pod Resources API client.
func NewClient(opts ...ClientOption) *Client {
	c := &Client{
		socketPath:      defaultSocketPath,
		refreshInterval: defaultRefreshInterval,
		resourceNames:   make(map[string]struct{}),
		deviceToPod:     make(map[deviceKey]ContainerInfo),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Start connects to the kubelet socket and begins periodic polling.
func (c *Client) Start() error {
	conn, err := grpc.NewClient("passthrough:"+c.socketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
			d := net.Dialer{}
			return d.DialContext(ctx, "unix", addr)
		}),
	)
	if err != nil {
		return fmt.Errorf("failed to connect to kubelet pod resources socket %s: %w", c.socketPath, err)
	}
	c.conn = conn
	c.listerClient = podresourcesapi.NewPodResourcesListerClient(conn)
	c.ctx, c.cancel = context.WithCancel(context.Background())

	go c.pollLoop()
	return nil
}

// Stop cancels the polling loop and closes the gRPC connection.
func (c *Client) Stop() {
	if c.cancel != nil {
		c.cancel()
	}
	if c.conn != nil {
		c.conn.Close()
	}
}

// AddResourceName registers a Kubernetes extended resource name to track.
func (c *Client) AddResourceName(resourceName string) {
	c.resourceNames[resourceName] = struct{}{}
}

// GetContainerInfo looks up the pod/container that owns the given device.
func (c *Client) GetContainerInfo(deviceID string, resourceName string) *ContainerInfo {
	key := deviceKey{DeviceID: deviceID, ResourceName: resourceName}
	if info, ok := c.deviceToPod[key]; ok {
		return &info
	}
	return nil
}

func (c *Client) pollLoop() {
	ticker := time.NewTicker(c.refreshInterval)
	defer ticker.Stop()

	// Initial refresh.
	c.refresh()

	for {
		select {
		case <-ticker.C:
			c.refresh()
		case <-c.ctx.Done():
			return
		}
	}
}

func (c *Client) refresh() {
	if len(c.resourceNames) == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(c.ctx, connectionTimeout)
	defer cancel()

	resp, err := c.listerClient.List(ctx, &podresourcesapi.ListPodResourcesRequest{})
	if err != nil {
		return
	}

	newMap := make(map[deviceKey]ContainerInfo)
	for _, pod := range resp.GetPodResources() {
		for _, container := range pod.GetContainers() {
			for _, device := range container.GetDevices() {
				if _, tracked := c.resourceNames[device.GetResourceName()]; !tracked {
					continue
				}
				info := ContainerInfo{
					PodName:       pod.GetName(),
					Namespace:     pod.GetNamespace(),
					ContainerName: container.GetName(),
				}
				for _, deviceID := range device.GetDeviceIds() {
					newMap[deviceKey{DeviceID: deviceID, ResourceName: device.GetResourceName()}] = info
				}
			}
		}
	}
	c.deviceToPod = newMap
}
