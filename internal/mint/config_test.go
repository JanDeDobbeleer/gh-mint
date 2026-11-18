package mint

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	return key
}

func writeTestKey(t *testing.T, path string, key *rsa.PrivateKey) {
	t.Helper()
	data := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	require.NoError(t, os.WriteFile(path, data, 0o600))
}

func TestReadPrivateKey(t *testing.T) {
	key := testKey(t)
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	ecData, err := x509.MarshalPKCS8PrivateKey(ecKey)
	require.NoError(t, err)
	tests := []struct {
		name string
		err  string
		data []byte
	}{
		{name: "PKCS1", data: pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})},
		{name: "PKCS8", data: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})},
		{name: "non RSA", data: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: ecData}), err: "must be RSA"},
		{name: "invalid PEM", data: []byte("not-a-key"), err: "exactly one"},
		{name: "encrypted without prompt", data: pem.EncodeToMemory(&pem.Block{Type: "ENCRYPTED PRIVATE KEY", Bytes: []byte("encrypted")}), err: "interactive passphrase prompt"},
		{name: "trailing data", data: append(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}), []byte("another-secret")...), err: "exactly one"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "key.pem")
			require.NoError(t, os.WriteFile(path, tt.data, 0o600))
			got, err := readKey(path, nil)
			if tt.err != "" {
				require.ErrorContains(t, err, tt.err)
				assert.NotContains(t, err.Error(), "another-secret")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, key.N, got.N)
		})
	}
}

func TestConfigurationPersistence(t *testing.T) {
	tests := []struct {
		name     string
		err      string
		existing bool
		legacy   bool
	}{
		{name: "round trip"},
		{name: "never overwrite", existing: true, err: "never overwritten"},
		{name: "legacy permissions ignored", legacy: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			cfg := config{
				AppID: 42, Remote: "origin", KeyFile: filepath.Join(dir, "key.pem"),
				Copilot: filepath.Join(dir, "copilot"),
			}
			if tt.existing {
				require.NoError(t, os.WriteFile(filepath.Join(dir, "config.json"), []byte("original"), 0o600))
			}
			err := writeConfig(dir, cfg)
			if tt.err != "" {
				require.ErrorContains(t, err, tt.err)
				if tt.existing {
					data, readErr := os.ReadFile(filepath.Join(dir, "config.json"))
					require.NoError(t, readErr)
					assert.Equal(t, "original", string(data))
				}
				return
			}
			require.NoError(t, err)
			if tt.legacy {
				data, marshalErr := json.Marshal(struct {
					Permissions map[string]string `json:"permissions"`
					Settings    map[string]any    `json:"settings"`
					Sandbox     map[string]any    `json:"sandbox"`
					config
				}{
					config: cfg, Permissions: map[string]string{"contents": "read", "issues": "read", "pull_requests": "read"},
					Settings: map[string]any{"allow": []string{"sandbox"}},
					Sandbox:  map[string]any{"env": []string{"GH_TOKEN"}, "readonly_paths": []string{"relative"}},
				})
				require.NoError(t, marshalErr)
				require.NoError(t, os.WriteFile(filepath.Join(dir, "config.json"), data, 0o600))
			}
			got, err := readConfig(dir)
			require.NoError(t, err)
			assert.Equal(t, cfg, got)
			data, err := os.ReadFile(filepath.Join(dir, "config.json"))
			require.NoError(t, err)
			assert.NotContains(t, string(data), "PRIVATE KEY")
		})
	}
}
