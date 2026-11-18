package mint

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommandEntryPoints(t *testing.T) {
	tests := []struct {
		name string
		want string
		err  string
		args []string
	}{
		{name: "setup help", args: []string{"setup", "--help"}},
		{name: "missing setup key", args: []string{"setup", "--app-id", "123"}, err: "are required"},
		{name: "removed permission override", args: []string{"setup", "--permissions=contents=read"}, err: "invalid setup arguments"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := Run(t.Context(), tt.args, "test-version", nil, &stdout, &stderr)
			if tt.err != "" {
				require.ErrorContains(t, err, tt.err)
				return
			}
			require.NoError(t, err)
			assert.Contains(t, stdout.String(), tt.want)
		})
	}
}

func TestCopilotArgumentsReachLaunch(t *testing.T) {
	base := t.TempDir()
	for _, name := range []string{"APPDATA", "HOME", "XDG_CONFIG_HOME"} {
		t.Setenv(name, base)
	}
	for _, args := range [][]string{
		{"--help"}, {"-h"}, {"help"}, {"--version"}, {"copilot"},
		{"--", "--no-sandbox"}, {"--model"}, {"-p", "", "-s"},
	} {
		t.Run(args[0], func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := Run(t.Context(), args, "test-version", nil, &stdout, &stderr)
			require.ErrorContains(t, err, "cannot read configuration")
			assert.Empty(t, stdout.String())
		})
	}
}

func TestSetupEncryptsKeyAndStoresOnlyReference(t *testing.T) {
	base := t.TempDir()
	for _, name := range []string{"APPDATA", "HOME", "XDG_CONFIG_HOME"} {
		t.Setenv(name, base)
	}
	keyPath := filepath.Join(base, "private.pem")
	writeTestKey(t, keyPath, testKey(t))
	binary := fakeCopilotBinary(t)
	var stdout, stderr bytes.Buffer
	prompts := &testKeyPrompter{passwords: []string{"test passphrase", "test passphrase"}}
	err := setup([]string{
		"--app-id=42", "--key-file=" + keyPath, "--copilot=" + binary,
	}, &stdout, &stderr, prompts)
	require.NoError(t, err)
	dir, err := configDirectory()
	require.NoError(t, err)
	cfg, err := readConfig(dir)
	require.NoError(t, err)
	canonical, err := canonicalPath(filepath.Join(dir, "keys", "app.encrypted.pem"))
	require.NoError(t, err)
	assert.Equal(t, canonical, cfg.KeyFile)
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	require.NoError(t, err)
	assert.NotContains(t, string(data), "PRIVATE KEY")
	assert.NotContains(t, string(data), `"permissions"`)
	assert.NotContains(t, string(data), "test passphrase")
	assert.NotContains(t, stdout.String()+stderr.String(), "test passphrase")
	assert.FileExists(t, keyPath)
	_, err = readKey(cfg.KeyFile, func(string) ([]byte, error) { return []byte("test passphrase"), nil })
	require.NoError(t, err)
	for _, buffer := range prompts.buffers {
		assert.Equal(t, make([]byte, len(buffer)), buffer)
	}
}

func TestSetupEncryptsExistingConfiguration(t *testing.T) {
	base := t.TempDir()
	for _, name := range []string{"APPDATA", "HOME", "XDG_CONFIG_HOME"} {
		t.Setenv(name, base)
	}
	dir, err := configDirectory()
	require.NoError(t, err)
	path, err := canonicalPath(t.TempDir())
	require.NoError(t, err)
	path = filepath.Join(path, "original.pem")
	writeTestKey(t, path, testKey(t))
	cfg := config{AppID: 42, Remote: "upstream", KeyFile: path, Copilot: filepath.Join(base, "copilot")}
	require.NoError(t, writeConfig(dir, cfg))
	var stdout, stderr bytes.Buffer
	prompts := &testKeyPrompter{passwords: []string{"test passphrase", "test passphrase"}, remove: true}
	require.NoError(t, setup([]string{"--encrypt-key"}, &stdout, &stderr, prompts))
	updated, err := readConfig(dir)
	require.NoError(t, err)
	assert.Equal(t, cfg.AppID, updated.AppID)
	assert.Equal(t, cfg.Remote, updated.Remote)
	assert.Equal(t, cfg.Copilot, updated.Copilot)
	assert.NotEqual(t, path, updated.KeyFile)
	assert.NoFileExists(t, path)
	assert.Equal(t, 1, prompts.confirmations)
	require.NoError(t, setup([]string{"--encrypt-key"}, &stdout, &stderr, &testKeyPrompter{}))
	assert.Contains(t, stdout.String(), "already encrypted")
}

func TestSetupDoesNotOverwriteExistingConfiguration(t *testing.T) {
	base := t.TempDir()
	for _, name := range []string{"APPDATA", "HOME", "XDG_CONFIG_HOME"} {
		t.Setenv(name, base)
	}
	keyPath := filepath.Join(base, "private.pem")
	writeTestKey(t, keyPath, testKey(t))
	binary := fakeCopilotBinary(t)
	dir, err := configDirectory()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	configPath := filepath.Join(dir, "config.json")
	require.NoError(t, os.WriteFile(configPath, []byte("existing"), 0o600))
	err = setup([]string{"--app-id=42", "--key-file=" + keyPath, "--copilot=" + binary}, &bytes.Buffer{}, &bytes.Buffer{}, &testKeyPrompter{})
	require.ErrorContains(t, err, "never overwritten")
	data, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.Equal(t, "existing", string(data))
}

func TestSetupRejectsMixedMigrationOptions(t *testing.T) {
	err := setup([]string{"--encrypt-key", "--remote=other"}, &bytes.Buffer{}, &bytes.Buffer{}, &testKeyPrompter{})
	require.ErrorContains(t, err, "without other setup options")
}

func TestTerminalPromptsDoNotConsumePipedInput(t *testing.T) {
	input := strings.NewReader("prompt intended for Copilot")
	prompt := terminalPrompter{stdin: input, stderr: &bytes.Buffer{}}
	_, closeTerminal, err := prompt.terminal()
	if err == nil {
		closeTerminal()
	}
	assert.Equal(t, len("prompt intended for Copilot"), input.Len())
}
