package mint

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type childSnapshot struct {
	Settings map[string]any    `json:"settings"`
	Env      map[string]string `json:"env"`
	CWD      string            `json:"cwd"`
	Args     []string          `json:"args"`
}

func TestMain(m *testing.M) {
	if strings.HasPrefix(filepath.Base(os.Args[0]), "fake-copilot") {
		os.Exit(fakeCopilot())
	}
	os.Exit(m.Run())
}

func fakeCopilot() int {
	snapshot := childSnapshot{Args: os.Args[1:], Env: make(map[string]string)}
	for _, entry := range os.Environ() {
		key, value, _ := strings.Cut(entry, "=")
		snapshot.Env[key] = value
	}
	var err error
	snapshot.CWD, err = os.Getwd()
	if err != nil {
		return 90
	}
	home := os.Getenv("COPILOT_HOME")
	if home == "" {
		home, err = os.UserHomeDir()
		if err != nil {
			return 91
		}
		home = filepath.Join(home, ".copilot")
	}
	data, err := os.ReadFile(filepath.Join(home, "settings.json"))
	if err != nil || json.Unmarshal(data, &snapshot.Settings) != nil {
		return 92
	}
	if json.NewEncoder(os.Stdout).Encode(snapshot) != nil {
		return 93
	}
	if slices.Contains(os.Args[1:], "--prompt=exit-seven") {
		return 7
	}
	return 0
}

func fakeCopilotBinary(t *testing.T) string {
	t.Helper()
	self, err := os.Executable()
	require.NoError(t, err)
	data, err := os.ReadFile(self)
	require.NoError(t, err)
	name := "fake-copilot"
	if os.PathSeparator == '\\' {
		name += ".exe"
	}
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, data, 0o700))
	return path
}

func TestSessionForwardsConfigurationAndRevokesItsToken(t *testing.T) {
	binary := fakeCopilotBinary(t)
	tests := []struct {
		name       string
		args       []string
		exitCode   int
		startErr   bool
		cancelled  bool
		customHome bool
	}{
		{name: "normal home", args: []string{"-p", "explain this repository", "-s", "--model=example", "--resume", "--", "a b", ""}},
		{name: "custom home", customHome: true},
		{name: "sandbox forwarded", args: []string{"--sandbox", "--allow-tool=shell(git:*)"}},
		{name: "permissions forwarded", args: []string{"--allow-all", "--secret-env-vars=OTHER", "-C", "other"}},
		{name: "child failure", args: []string{"--prompt=exit-seven"}, exitCode: 7},
		{name: "failed launch", startErr: true},
		{name: "cancelled launch", cancelled: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var revoked atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodDelete, r.Method)
				assert.Equal(t, "/installation/token", r.URL.Path)
				assert.Equal(t, "Bearer repository-test-token", r.Header.Get("Authorization"))
				revoked.Store(true)
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()
			root, err := canonicalPath(t.TempDir())
			require.NoError(t, err)
			userHome := t.TempDir()
			home := filepath.Join(userHome, ".copilot")
			if tt.customHome {
				home = filepath.Join(userHome, "custom copilot home")
			}
			require.NoError(t, os.MkdirAll(home, 0o700))
			settings := `{"model":"example","enabledPlugins":{"example":true},"hooks":{"sessionStart":[]},"sandbox":{"enabled":false}}`
			settingsPath := filepath.Join(home, "settings.json")
			require.NoError(t, os.WriteFile(settingsPath, []byte(settings), 0o600))
			keyDir := filepath.Join(t.TempDir(), "private keys")
			environ := []string{
				"GH_TOKEN=broad-user-secret", "GH_HOST=other.example", "GH_REPO=other/repo",
				"COPILOT_GITHUB_TOKEN=original-copilot-secret", "GITHUB_TOKEN=inherited-github-secret",
				"CUSTOM_ENV=custom-value", "GH_CONFIG_DIR=original-gh-home", "GIT_CONFIG_GLOBAL=original-gitconfig",
				"HOME=" + userHome, "USERPROFILE=" + userHome, "PATH=" + os.Getenv("PATH"),
				"SYSTEMROOT=" + os.Getenv("SYSTEMROOT"), "TEMP=" + os.TempDir(),
			}
			if tt.customHome {
				environ = append(environ, "COPILOT_HOME="+home)
			}
			if os.PathSeparator == '\\' {
				environ = append(environ, "gh_token=mixed-case-user-secret")
			}
			originalEnv := slices.Clone(environ)
			var stdout, stderr bytes.Buffer
			execute := (*exec.Cmd).Run
			if tt.startErr {
				execute = func(*exec.Cmd) error { return errors.New("process start failed") }
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tt.cancelled {
				cancel()
			}
			s := session{
				github:  &githubClient{base: server.URL, http: server.Client()},
				execute: execute, stdout: &stdout, stderr: &stderr, stdin: strings.NewReader(""),
				cfg: config{Copilot: binary}, repo: repository{Root: root, Owner: "owner", Name: "project"},
				keyDirectory: keyDir, copilotCredential: "copilot-account-secret",
				args: tt.args, environ: environ,
			}
			err = s.runWithToken(ctx, installationToken{Token: "repository-test-token", ExpiresAt: time.Now().Add(time.Hour)})
			assert.True(t, revoked.Load())
			assert.Equal(t, originalEnv, environ)
			assert.NotContains(t, stderr.String(), "secret")
			assert.NotContains(t, stderr.String(), "repository-test-token")
			unchanged, readErr := os.ReadFile(settingsPath)
			require.NoError(t, readErr)
			assert.Equal(t, settings, string(unchanged))
			if tt.startErr {
				require.ErrorContains(t, err, "process start failed")
				return
			}
			if tt.cancelled {
				require.ErrorIs(t, err, context.Canceled)
				return
			}
			if tt.exitCode != 0 {
				var exitErr *exec.ExitError
				require.ErrorAs(t, err, &exitErr)
				assert.Equal(t, tt.exitCode, exitErr.ExitCode())
			} else {
				require.NoError(t, err)
			}
			var child childSnapshot
			require.NoError(t, json.Unmarshal(stdout.Bytes(), &child))
			assert.Equal(t, root, child.CWD)
			assert.Equal(t, "repository-test-token", child.Env["GH_TOKEN"])
			assert.NotContains(t, fmt.Sprint(child.Env), "mixed-case-user-secret")
			assert.Equal(t, "github.com", child.Env["GH_HOST"])
			assert.Equal(t, "owner/project", child.Env["GH_REPO"])
			assert.Equal(t, "copilot-account-secret", child.Env["COPILOT_GITHUB_TOKEN"])
			assert.Equal(t, "inherited-github-secret", child.Env["GITHUB_TOKEN"])
			assert.Equal(t, "custom-value", child.Env["CUSTOM_ENV"])
			assert.Equal(t, "original-gh-home", child.Env["GH_CONFIG_DIR"])
			assert.Equal(t, "original-gitconfig", child.Env["GIT_CONFIG_GLOBAL"])
			if tt.customHome {
				assert.Equal(t, home, child.Env["COPILOT_HOME"])
			} else {
				assert.NotContains(t, child.Env, "COPILOT_HOME")
			}
			expectedArgs := []string{
				"--sandbox",
				"--secret-env-vars=COPILOT_GITHUB_TOKEN,GITHUB_TOKEN",
				"--disable-builtin-mcps",
				"--deny-tool=read(" + keyDir + ")",
				"--deny-tool=write(" + keyDir + ")",
				"--deny-tool=read(" + filepath.Join(keyDir, "**") + ")",
				"--deny-tool=write(" + filepath.Join(keyDir, "**") + ")",
			}
			assert.Equal(t, append(expectedArgs, tt.args...), child.Args)
			var originalSettings map[string]any
			require.NoError(t, json.Unmarshal([]byte(settings), &originalSettings))
			assert.Equal(t, originalSettings, child.Settings)
		})
	}
}
