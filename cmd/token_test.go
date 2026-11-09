package cmd

import (
	"encoding/base64"
	"io"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/o1egl/paseto"
	"github.com/publiccodeyml/open-catalog-api/internal/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTokenCreateCreatesDecryptableTokens(t *testing.T) {
	key := common.Base64Key([]byte("test-paseto-key-dont-use-in-prod"))

	tests := []struct {
		name           string
		args           []string
		key            []byte
		expectedSub    string
		expectedExpiry time.Duration
	}{
		{
			name: "provided key with claims",
			args: []string{
				"--key", base64.StdEncoding.EncodeToString(key[:]),
				"--sub", "catalog-writer",
				"--expiry", "2h",
			},
			key:            key[:],
			expectedSub:    "catalog-writer",
			expectedExpiry: 2 * time.Hour,
		},
		{
			name: "generated key without optional claims",
			args: []string{
				"--expiry", "0",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stdout, stderr, err := runTokenCreateForTest(t, test.args...)
			require.NoError(t, err)

			key := test.key
			if key == nil {
				matches := regexp.MustCompile(`generating random PASETO secret key: ([^\n]+)`).FindStringSubmatch(stderr)
				require.Len(t, matches, 2)

				key, err = base64.StdEncoding.DecodeString(matches[1])
				require.NoError(t, err)
				assert.Len(t, key, common.SymmetricKeyLen)
			}

			var payload paseto.JSONToken
			err = paseto.NewV2().Decrypt(strings.TrimSpace(stdout), key, &payload, nil)
			require.NoError(t, err)

			assert.Equal(t, test.expectedSub, payload.Subject)
			assert.False(t, payload.IssuedAt.IsZero())
			if test.expectedExpiry == 0 {
				assert.True(t, payload.Expiration.IsZero())
			} else {
				assert.WithinDuration(t, payload.IssuedAt.Add(test.expectedExpiry), payload.Expiration, time.Second)
			}
		})
	}
}

func TestTokenCreateRejectsInvalidKeys(t *testing.T) {
	tests := []struct {
		name        string
		key         string
		expectedErr string
	}{
		{
			name:        "malformed base64",
			key:         "not base64",
			expectedErr: "can't decode key",
		},
		{
			name:        "wrong key length",
			key:         base64.StdEncoding.EncodeToString([]byte("short key")),
			expectedErr: "invalid key length: must be 32 bytes, got 9",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := runTokenCreateForTest(t, "--key", test.key)

			require.ErrorContains(t, err, test.expectedErr)
		})
	}
}

func runTokenCreateForTest(t *testing.T, args ...string) (string, string, error) {
	t.Helper()

	oldStdout, oldStderr := os.Stdout, os.Stderr
	stdoutReader, stdoutWriter, err := os.Pipe()
	require.NoError(t, err)
	stderrReader, stderrWriter, err := os.Pipe()
	require.NoError(t, err)

	os.Stdout, os.Stderr = stdoutWriter, stderrWriter
	defer func() {
		os.Stdout, os.Stderr = oldStdout, oldStderr
	}()

	command := newTokenCreateCmd()
	command.SetArgs(args)

	err = command.Execute()
	require.NoError(t, stdoutWriter.Close())
	require.NoError(t, stderrWriter.Close())

	stdout, readErr := io.ReadAll(stdoutReader)
	require.NoError(t, readErr)
	stderr, readErr := io.ReadAll(stderrReader)
	require.NoError(t, readErr)

	return string(stdout), string(stderr), err
}
