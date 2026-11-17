package mint

import (
	"bytes"
	"os"
	"path/filepath"
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

func TestSetupStoresOnlyKeyReference(t *testing.T) {
	base := t.TempDir()
	for _, name := range []string{"APPDATA", "HOME", "XDG_CONFIG_HOME"} {
		t.Setenv(name, base)
	}
	keyPath := filepath.Join(base, "private.pem")
	writeTestKey(t, keyPath, testKey(t))
	binary := fakeCopilotBinary(t)
	var stdout, stderr bytes.Buffer
	err := Run(t.Context(), []string{
		"setup", "--app-id=42", "--key-file=" + keyPath, "--copilot=" + binary,
	}, "test", nil, &stdout, &stderr)
	require.NoError(t, err)
	dir, err := configDirectory()
	require.NoError(t, err)
	cfg, err := readConfig(dir)
	require.NoError(t, err)
	canonical, err := canonicalPath(keyPath)
	require.NoError(t, err)
	assert.Equal(t, canonical, cfg.KeyFile)
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	require.NoError(t, err)
	assert.NotContains(t, string(data), "PRIVATE KEY")
	assert.NotContains(t, string(data), `"permissions"`)
}
