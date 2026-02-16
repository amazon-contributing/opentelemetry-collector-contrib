// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package cloudauthextension // import "github.com/open-telemetry/opentelemetry-collector-contrib/extension/cloudauthextension"

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

// FileProvider reads an OIDC token from a file on disk.
type FileProvider struct {
	path string
}

func newFileProvider(path string) *FileProvider {
	return &FileProvider{path: path}
}

func (f *FileProvider) GetToken(_ context.Context) (string, time.Duration, error) {
	data, err := os.ReadFile(f.path)
	if err != nil {
		return "", 0, fmt.Errorf("read token file %s: %w", f.path, err)
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", 0, fmt.Errorf("token file %s is empty", f.path)
	}
	return token, 0, nil
}

func (f *FileProvider) IsAvailable(_ context.Context) bool {
	_, err := os.Stat(f.path)
	return err == nil
}

func (f *FileProvider) Name() string { return "file" }
