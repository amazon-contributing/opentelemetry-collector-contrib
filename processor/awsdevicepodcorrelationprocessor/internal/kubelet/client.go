// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package kubelet // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/awsdevicepodcorrelationprocessor/internal/kubelet"

import (
	"context"
	"fmt"
	"maps"
	"net"
	"sync"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	podresourcesapi "k8s.io/kubelet/pkg/apis/podresources/v1"

	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/awsdevicepodcorrelationprocessor/internal/podinfo"
)

const (
	DefaultSocketPath      = "/var/lib/kubelet/pod-resources/kubelet.sock"
	connectionTimeout      = 10 * time.Second
	defaultRefreshInterval = 10 * time.Second
)

// Client connects to the Kubelet Pod Resources API and maintains
// an in-memory cache of device-to-pod mappings.
//
// It serves two independent correlation paths off the same List poll:
//   - the device-plugin path, keyed by extended resource name (deviceToPod),
//   - the DRA path, which exposes the raw DynamicResources bindings reported by
//     the kubelet (draDevices) for the dra.Store to key and enrich.
//
// The DRA path is only populated when the client is created WithDRA.
type Client struct {
	conn            *grpc.ClientConn
	listerClient    podresourcesapi.PodResourcesListerClient
	resourceNames   map[string]struct{}
	mu              sync.RWMutex
	deviceToPod     map[deviceKey]podinfo.ContainerInfo
	draDevices      []podinfo.DRABinding
	draEnabled      bool
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	socketPath      string
	refreshInterval time.Duration
	logger          *zap.Logger
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
		path = DefaultSocketPath
	}
	return func(c *Client) { c.socketPath = path }
}

// WithDRA enables parsing of DynamicResources from the Pod Resources API so the
// client exposes DRA device-to-pod bindings via ListDRADevices. When this option
// is not set, the client only serves the device-plugin path and polling is a
// no-op unless at least one resource name is registered.
func WithDRA() ClientOption {
	return func(c *Client) { c.draEnabled = true }
}

// NewClient creates a new Kubelet Pod Resources API client.
func NewClient(logger *zap.Logger, opts ...ClientOption) *Client {
	c := &Client{
		socketPath:      DefaultSocketPath,
		refreshInterval: defaultRefreshInterval,
		resourceNames:   make(map[string]struct{}),
		deviceToPod:     make(map[deviceKey]podinfo.ContainerInfo),
		logger:          logger,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Start connects to the kubelet socket and begins periodic polling.
func (c *Client) Start(ctx context.Context) error {
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

	pollCtx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	c.wg.Add(1)
	go c.pollLoop(pollCtx)
	return nil
}

// Stop cancels the polling loop, waits for it to exit, and closes the gRPC connection.
func (c *Client) Stop() {
	if c.cancel != nil {
		c.cancel()
	}
	c.wg.Wait()
	if c.conn != nil {
		c.conn.Close()
	}
}

// AddResourceName registers a Kubernetes extended resource name to track.
func (c *Client) AddResourceName(resourceName string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resourceNames[resourceName] = struct{}{}
}

// GetContainerInfo looks up the pod/container that owns the given device.
func (c *Client) GetContainerInfo(deviceID, resourceName string) *podinfo.ContainerInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	key := deviceKey{DeviceID: deviceID, ResourceName: resourceName}
	if info, ok := c.deviceToPod[key]; ok {
		return &info
	}
	return nil
}

// ListDRADevices returns a snapshot of the DRA device-to-pod bindings observed
// on the last successful poll. The slice is a copy and safe for the caller to
// retain. Returns nil when the client was not created WithDRA.
func (c *Client) ListDRADevices() []podinfo.DRABinding {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.draDevices) == 0 {
		return nil
	}
	out := make([]podinfo.DRABinding, len(c.draDevices))
	copy(out, c.draDevices)
	return out
}

func (c *Client) pollLoop(ctx context.Context) {
	defer c.wg.Done()
	ticker := time.NewTicker(c.refreshInterval)
	defer ticker.Stop()

	c.refresh(ctx)

	for {
		select {
		case <-ticker.C:
			c.refresh(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (c *Client) refresh(ctx context.Context) {
	// Snapshot resourceNames under read lock to avoid racing with AddResourceName.
	c.mu.RLock()
	trackedResources := make(map[string]struct{}, len(c.resourceNames))
	maps.Copy(trackedResources, c.resourceNames)
	draEnabled := c.draEnabled
	c.mu.RUnlock()

	// Nothing to collect: no device-plugin resources tracked and DRA disabled.
	if len(trackedResources) == 0 && !draEnabled {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, connectionTimeout)
	defer cancel()

	resp, err := c.listerClient.List(ctx, &podresourcesapi.ListPodResourcesRequest{})
	if err != nil {
		c.logger.Error("Failed to list pod resources from kubelet", zap.Error(err))
		return
	}

	newMap := make(map[deviceKey]podinfo.ContainerInfo)
	var newDRA []podinfo.DRABinding
	for _, pod := range resp.GetPodResources() {
		for _, container := range pod.GetContainers() {
			info := podinfo.ContainerInfo{
				PodName:       pod.GetName(),
				Namespace:     pod.GetNamespace(),
				ContainerName: container.GetName(),
			}

			// Device-plugin path: devices keyed by extended resource name.
			for _, device := range container.GetDevices() {
				if _, tracked := trackedResources[device.GetResourceName()]; !tracked {
					continue
				}
				for _, deviceID := range device.GetDeviceIds() {
					newMap[deviceKey{DeviceID: deviceID, ResourceName: device.GetResourceName()}] = info
				}
			}

			// DRA path: the pod↔claim bindings this container holds. Device
			// detail is resolved later from the ResourceClaim, so a claim is
			// recorded even when the driver leaves ClaimResources empty.
			if draEnabled {
				newDRA = appendDRABindings(newDRA, container, info)
			}
		}
	}

	c.mu.Lock()
	c.deviceToPod = newMap
	if draEnabled {
		c.draDevices = newDRA
	}
	c.mu.Unlock()
}

// appendDRABindings records one binding per DRA claim the container holds. Only
// the claim reference (name/namespace) and owning pod are taken from the socket;
// the allocated devices are read from the ResourceClaim by the dra.Store, since
// some drivers report the claim with empty ClaimResources.
func appendDRABindings(dst []podinfo.DRABinding, container *podresourcesapi.ContainerResources, info podinfo.ContainerInfo) []podinfo.DRABinding {
	for _, dr := range container.GetDynamicResources() {
		if dr.GetClaimName() == "" {
			continue
		}
		dst = append(dst, podinfo.DRABinding{
			ClaimName:      dr.GetClaimName(),
			ClaimNamespace: dr.GetClaimNamespace(),
			Pod:            info,
		})
	}
	return dst
}
