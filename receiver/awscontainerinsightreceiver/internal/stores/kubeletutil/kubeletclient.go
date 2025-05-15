// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package kubeletutil // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/stores/kubeletutil"

import (
	"encoding/json"
	"fmt"
	"log"
	"os"

	"go.opentelemetry.io/collector/config/configtls"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	stats "k8s.io/kubelet/pkg/apis/stats/v1alpha1"
	"k8s.io/utils/net"

	"github.com/open-telemetry/opentelemetry-collector-contrib/internal/k8sconfig"
	"github.com/open-telemetry/opentelemetry-collector-contrib/internal/kubelet"
)

var kubeletNewClientProvider = kubelet.NewClientProvider

const kubeletCAPath = "/etc/kubernetes/kubelet-ca.crt"

type KubeletClient struct {
	KubeIP     string
	Port       string
	restClient kubelet.Client
}

func isFileExist(filePath string) bool {
	// assumes file does not exist on ANY kind of error
	_, err := os.Stat(filePath)
	return err == nil
}

func NewKubeletClient(kubeIP string, port string, clientConfig *kubelet.ClientConfig, logger *zap.Logger) (*KubeletClient, error) {
	kubeClient := &KubeletClient{
		Port:   port,
		KubeIP: kubeIP,
	}
	log.Println("NewKubelet client parameters", kubeIP)

	b, err := json.Marshal(clientConfig)
	if err != nil {
		logger.Debug("Error marshaling client config", zap.Error(err))
		log.Println("Error marshaling clientConfigCall:", err)
	} else {
		logger.Debug("Client config", zap.String("config", string(b)))
		log.Println("calling clientConfigCall", string(b))
	}

	endpoint := kubeIP
	if net.IsIPv6String(kubeIP) {
		logger.Debug("IPv6 address detected, adding brackets")
		endpoint = "[" + endpoint + "]"
	}
	endpoint = endpoint + ":" + port
	logger.Debug("Created endpoint", zap.String("endpoint", endpoint))
	// use service account for authentication by default
	if clientConfig == nil {
		clientConfig = &kubelet.ClientConfig{
			APIConfig: k8sconfig.APIConfig{
				AuthType: k8sconfig.AuthTypeServiceAccount,
			},
		}
	}

	clientProvider, err := kubeletNewClientProvider(endpoint, clientConfig, logger)
	if err != nil {
		return nil, err
	}
	client, err := clientProvider.BuildClient()
	if err != nil {
		return nil, err
	}
	kubeClient.restClient = client
	return kubeClient, nil
}

func (k *KubeletClient) ListPods() ([]corev1.Pod, error) {
	var result []corev1.Pod
	b, err := k.restClient.Get("/pods")
	if err != nil {
		return result, fmt.Errorf("call to /pods endpoint failed: %w", err)
	}

	pods := corev1.PodList{}
	err = json.Unmarshal(b, &pods)
	if err != nil {
		return result, fmt.Errorf("parsing response failed: %w", err)
	}

	return pods.Items, nil
}

// Summary hits kubelet summary API using service account authentication.
// Summary API returns metrics at container, pod and node level for CPU, memory, networking and file system resources.
func (k *KubeletClient) Summary(logger *zap.Logger) (*stats.Summary, error) {
	logger.Debug("Calling kubelet /stats/summary API")

	b, err := k.restClient.Get("/stats/summary")
	if err != nil {
		return nil, fmt.Errorf("call to kubelet /stats/summary API failed %w", err)
	}
	var out stats.Summary
	err = json.Unmarshal(b, &out)
	if err != nil {
		return nil, fmt.Errorf("kubelet summary unmarshalling failed %w", err)
	}

	logger.Debug("/stats/summary API response unmarshalled successfully")
	return &out, nil
}

func ClientConfig(kubeConfigPath string, isSystemd bool) *kubelet.ClientConfig {
	log.Println("kubeConfigPath")
	if kubeConfigPath != "" {
		// use kube-config for authentication
		return &kubelet.ClientConfig{
			APIConfig: k8sconfig.APIConfig{
				AuthType:       k8sconfig.AuthTypeKubeConfig,
				KubeConfigPath: kubeConfigPath,
			},
		}
	}
	log.Println("after kubeConfigPath")

	if isFileExist(kubeletCAPath) {
		log.Println("in isFileExist")

		// check if kubeletca is available
		return &kubelet.ClientConfig{
			APIConfig: k8sconfig.APIConfig{
				AuthType: k8sconfig.AuthTypeServiceAccount,
			},
			Config: configtls.Config{CAFile: kubeletCAPath},
		}
	}

	if !isSystemd {
		log.Println("in !systemd")

		// use service account if kubelet ca or kubeconfig don't exist
		return &kubelet.ClientConfig{
			APIConfig: k8sconfig.APIConfig{
				AuthType: k8sconfig.AuthTypeServiceAccount,
			},
		}
	}
	log.Println("return cfg")
	// insecure TLS if not provided
	return &kubelet.ClientConfig{
		APIConfig: k8sconfig.APIConfig{
			AuthType: k8sconfig.AuthTypeTLS,
		},
		InsecureSkipVerify: true,
	}
}
