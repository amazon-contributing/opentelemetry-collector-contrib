package tokenprovider // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightskueuereceiver/internal/tokenprovider"

import (
	"fmt"
	"os"
	"strings"
)

const (
	serviceAccountTokenDefaultPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"
)

type BearerTokenProvider struct {
	tokenGeneration int
	loadedToken     string
	RetrieveToken   func() (string, error)
}

func NewBearerTokenProvider() *BearerTokenProvider {
	var provider *BearerTokenProvider = &BearerTokenProvider{
		tokenGeneration: 0,
		loadedToken:     "",
		RetrieveToken:   defaultRetrieveToken,
	}

	return provider
}

func (provider *BearerTokenProvider) GetToken() (string, error) {
	newToken, err := provider.RetrieveToken()
	if err != nil {
		return "", err
	}
	if newToken != provider.loadedToken {
		provider.tokenGeneration += 1
		provider.loadedToken = newToken
	}
	return provider.loadedToken, nil
}

func (provider *BearerTokenProvider) TokenGeneration() int {
	return provider.tokenGeneration
}

func defaultRetrieveToken() (string, error) {
	return retrieveTokenFromFileSystem(serviceAccountTokenDefaultPath)
}

func retrieveTokenFromFileSystem(tokenPath string) (string, error) {
	tokenBytes, err := os.ReadFile(tokenPath)
	if err != nil {
		return "", fmt.Errorf("failed to read bearer token: %w", err)
	}
	return strings.TrimSpace(string(tokenBytes)), nil
}
