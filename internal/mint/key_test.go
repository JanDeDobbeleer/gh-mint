package mint

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testKeyPrompter struct {
	onConfirm     func(string) (bool, error)
	passwords     []string
	buffers       [][]byte
	confirmations int
	remove        bool
}

func (p *testKeyPrompter) password(string) ([]byte, error) {
	if len(p.passwords) == 0 {
		return nil, errors.New("no test passphrase available")
	}
	password := []byte(p.passwords[0])
	p.passwords = p.passwords[1:]
	p.buffers = append(p.buffers, password)
	return password, nil
}

func (p *testKeyPrompter) confirm(message string) (bool, error) {
	p.confirmations++
	if p.onConfirm != nil {
		return p.onConfirm(message)
	}
	return p.remove, nil
}

func TestEncryptedPrivateKey(t *testing.T) {
	dir := t.TempDir()
	key := testKey(t)
	password := "a long test-only passphrase"
	path, err := writeEncryptedKey(dir, key, []byte(password))
	require.NoError(t, err)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	block, _ := pem.Decode(data)
	require.NotNil(t, block)
	assert.Equal(t, "ENCRYPTED PRIVATE KEY", block.Type)
	assert.NotContains(t, string(data), password)
	for _, tt := range []struct {
		name     string
		password string
		err      string
	}{
		{name: "correct password", password: password},
		{name: "wrong password", password: "wrong password", err: "cannot decrypt"},
		{name: "empty password", err: "cannot be empty"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			buffer := []byte(tt.password)
			got, err := readKey(path, func(string) ([]byte, error) { return buffer, nil })
			assert.Equal(t, make([]byte, len(buffer)), buffer)
			if tt.err != "" {
				require.ErrorContains(t, err, tt.err)
				assert.NotContains(t, err.Error(), password)
				return
			}
			require.NoError(t, err)
			assert.True(t, key.Equal(got))
		})
	}
	_, err = writeEncryptedKey(dir, key, []byte("different password"))
	require.ErrorContains(t, err, "never overwritten")
	unchanged, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, data, unchanged)

	openssl, err := exec.LookPath("openssl")
	if err == nil {
		cmd := exec.CommandContext(t.Context(), openssl, "pkey", "-in", path, "-passin", "stdin", "-pubout")
		cmd.Stdin = strings.NewReader(password + "\n")
		output, err := cmd.Output()
		require.NoError(t, err)
		public, _ := pem.Decode(output)
		require.NotNil(t, public)
		expected, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
		require.NoError(t, err)
		assert.Equal(t, expected, public.Bytes)
	}
}

func TestEncryptedKeyRejectsEmptyPassphrase(t *testing.T) {
	dir := t.TempDir()
	_, err := writeEncryptedKey(dir, testKey(t), nil)
	require.ErrorContains(t, err, "cannot be empty")
	assert.NoDirExists(t, filepath.Join(dir, "keys"))
}

func TestKeySetupPreservesOriginalOnFailure(t *testing.T) {
	key := testKey(t)
	for _, tt := range []struct {
		name      string
		err       string
		passwords []string
		conflict  bool
	}{
		{name: "empty passphrase", passwords: []string{""}, err: "cannot be empty"},
		{name: "mismatched passphrase", passwords: []string{"first", "second"}, err: "do not match"},
		{name: "confirmation interrupted", passwords: []string{"first"}, err: "no test passphrase"},
		{name: "configuration already exists", passwords: []string{"first", "first"}, conflict: true, err: "never overwritten"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "original.pem")
			writeTestKey(t, source, key)
			data, err := os.ReadFile(source)
			require.NoError(t, err)
			dir := t.TempDir()
			if tt.conflict {
				require.NoError(t, os.WriteFile(filepath.Join(dir, "config.json"), []byte("original config"), 0o600))
			}
			cfg := config{AppID: 42, Remote: "origin", KeyFile: source, Copilot: filepath.Join(dir, "copilot")}
			prompts := &testKeyPrompter{passwords: tt.passwords, remove: true}
			err = encryptSetupKey(dir, cfg, prompts, &bytes.Buffer{}, false)
			require.ErrorContains(t, err, tt.err)
			assert.Zero(t, prompts.confirmations)
			assert.NoFileExists(t, filepath.Join(dir, "keys", "app.encrypted.pem"))
			unchanged, err := os.ReadFile(source)
			require.NoError(t, err)
			assert.Equal(t, data, unchanged)
		})
	}
}

func TestKeySetupConfirmsDeletionOnlyAfterConfiguration(t *testing.T) {
	key := testKey(t)
	for _, outcome := range []string{"keep", "delete", "confirmation error", "source replaced"} {
		t.Run(outcome, func(t *testing.T) {
			source, err := canonicalPath(t.TempDir())
			require.NoError(t, err)
			source = filepath.Join(source, "original.pem")
			writeTestKey(t, source, key)
			dir := t.TempDir()
			cfg := config{AppID: 42, Remote: "origin", KeyFile: source, Copilot: filepath.Join(dir, "copilot")}
			prompts := &testKeyPrompter{
				passwords: []string{"test passphrase", "test passphrase"},
				onConfirm: func(message string) (bool, error) {
					assert.Contains(t, message, fmt.Sprintf("%q", source))
					configured, err := readConfig(dir)
					require.NoError(t, err)
					verified, err := readKey(configured.KeyFile, func(string) ([]byte, error) { return []byte("test passphrase"), nil })
					require.NoError(t, err)
					assert.True(t, key.Equal(verified))
					switch outcome {
					case "keep":
						return false, nil
					case "confirmation error":
						return false, errors.New("interrupted")
					case "source replaced":
						require.NoError(t, os.Remove(source))
						require.NoError(t, os.WriteFile(source, []byte("a replacement file"), 0o600))
					}
					return true, nil
				},
			}
			err = encryptSetupKey(dir, cfg, prompts, &bytes.Buffer{}, false)
			switch outcome {
			case "confirmation error":
				require.ErrorContains(t, err, "original key retained")
			case "source replaced":
				require.ErrorContains(t, err, "changed during setup")
			default:
				require.NoError(t, err)
			}
			if outcome == "delete" {
				assert.NoFileExists(t, source)
			} else {
				assert.FileExists(t, source)
			}
			_, err = readConfig(dir)
			require.NoError(t, err)
		})
	}
}
