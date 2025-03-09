package tokenprovider // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightskueuereceiver/internal/tokenprovider"

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewBearerTokenProvider(t *testing.T) {
	testCases := []struct {
		caseName string
	}{
		{
			caseName: "Success Case",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.caseName, func(t *testing.T) {
			var testProvider *BearerTokenProvider = NewBearerTokenProvider()

			assert.NotNil(t, testProvider)
			assert.Equal(t, 0, testProvider.tokenGeneration)
			assert.Empty(t, testProvider.loadedToken)

			expectedPtr := reflect.ValueOf(defaultRetrieveToken).Pointer()
			actualPtr := reflect.ValueOf(testProvider.RetrieveToken).Pointer()
			assert.Equal(t, expectedPtr, actualPtr)
		})
	}
}

func TestRetrieveToken(t *testing.T) {
	var mockToken string = "dummy-token"
	var mockError error = fmt.Errorf("dummy error")

	testCases := []struct {
		caseName           string
		initialToken       string
		mockProvider       func() (string, error)
		expectedError      error
		expectedToken      string
		expectedGeneration int
	}{
		{
			caseName:     "New Token Case",
			initialToken: "",
			mockProvider: func() (string, error) {
				return mockToken, nil
			},
			expectedError:      nil,
			expectedToken:      mockToken,
			expectedGeneration: 1,
		},
		{
			caseName:     "Same Token Case",
			initialToken: mockToken,
			mockProvider: func() (string, error) {
				return mockToken, nil
			},
			expectedError:      nil,
			expectedToken:      mockToken,
			expectedGeneration: 0,
		},
		{
			caseName:     "Error Case",
			initialToken: "",
			mockProvider: func() (string, error) {
				return "", mockError
			},
			expectedError:      mockError,
			expectedToken:      "",
			expectedGeneration: 0,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.caseName, func(t *testing.T) {
			var testProvider *BearerTokenProvider = &BearerTokenProvider{
				tokenGeneration: 0,
				loadedToken:     testCase.initialToken,
				RetrieveToken:   testCase.mockProvider,
			}

			assert.Equal(t, 0, testProvider.tokenGeneration)
			assert.Equal(t, testCase.initialToken, testProvider.loadedToken)

			yieldedToken, err := testProvider.GetToken()

			assert.Equal(t, testCase.expectedGeneration, testProvider.tokenGeneration)
			assert.Equal(t, testCase.expectedToken, yieldedToken)
			assert.Equal(t, testCase.expectedToken, testProvider.loadedToken)
			assert.Equal(t, testCase.expectedGeneration, testProvider.tokenGeneration)
			assert.Equal(t, testCase.expectedError, err)
		})
	}
}

func TestTokenGeneration(t *testing.T) {
	provider := &BearerTokenProvider{
		tokenGeneration: 42,
	}

	if gen := provider.TokenGeneration(); gen != 42 {
		t.Errorf("Expected token generation 42, got %d", gen)
	}
}

func TestRetrieveTokenFromFileSystem2(t *testing.T) {
	var tmpDir string = t.TempDir()
	var dummyToken string = "dummy-token-content"
	var dummyTokenPath string = filepath.Join(tmpDir, "test-token")

	err := os.WriteFile(dummyTokenPath, []byte(dummyToken+"\n"), 0600)
	if err != nil {
		t.Fatalf("Failed to create test token file: %v", err)
	}

	testCases := []struct {
		caseName      string
		tokenPath     string
		expectedToken string
		errorExpected bool
	}{
		{
			caseName:      "Success Case",
			tokenPath:     dummyTokenPath,
			expectedToken: dummyToken,
			errorExpected: false,
		},
		{
			caseName:      "Nonexistent File Case",
			tokenPath:     filepath.Join(tmpDir, "nonexistent-token"),
			expectedToken: "",
			errorExpected: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.caseName, func(t *testing.T) {
			token, err := retrieveTokenFromFileSystem(testCase.tokenPath)
			assert.Equal(t, testCase.expectedToken, token)
			if testCase.errorExpected {
				assert.NotNil(t, err)
			} else {
				assert.Nil(t, err)
			}
		})
	}
}
