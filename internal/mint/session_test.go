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
	if slices.Contains(os.Args[1:], "--version") {
		fmt.Println("GitHub Copilot CLI 1.0.112.")
		return 0
	}
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
	data, err := os.ReadFile(filepath.Join(os.Getenv("COPILOT_HOME"), "settings.json"))
	if err != nil || json.Unmarshal(data, &snapshot.Settings) != nil {
		return 91
	}
	if json.NewEncoder(os.Stdout).Encode(snapshot) != nil {
		return 92
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

func TestSessionLaunchesAnIsolatedChildAndRevokesItsToken(t *testing.T) {
	binary := fakeCopilotBinary(t)
	tests := []struct {
		name     string
		prompt   string
		args     []string
		exitCode int
		startErr bool
	}{
		{name: "normal exit", args: []string{"-p", "explain this repository", "-s", "--model=example", "--resume", "--", "a b", ""}},
		{name: "unrestricted arguments", args: []string{"--no-sandbox", "--secret-env-vars=OTHER", "-C", "other"}},
		{name: "child failure", prompt: "exit-seven", exitCode: 7},
		{name: "failed launch", startErr: true},
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
			configDir := t.TempDir()
			root, err := canonicalPath(t.TempDir())
			require.NoError(t, err)
			keyDir := filepath.Join(t.TempDir(), "private")
			var stdout, stderr bytes.Buffer
			execute := (*exec.Cmd).Run
			if tt.startErr {
				execute = func(*exec.Cmd) error { return errors.New("process start failed") }
			}
			args := tt.args
			if tt.prompt != "" {
				args = []string{"--prompt=" + tt.prompt}
			}
			s := session{
				github:  &githubClient{base: server.URL, http: server.Client()},
				execute: execute, stdout: &stdout, stderr: &stderr, stdin: strings.NewReader(""),
				cfg: config{Copilot: binary}, repo: repository{Root: root, Owner: "owner", Name: "project"},
				configDir: configDir, copilotCredential: "copilot-account-secret",
				args: args, denied: []string{keyDir, configDir},
				environ: []string{
					"GH_TOKEN=broad-user-secret", "github_token=other-secret", "GH_APP_PRIVATE_KEY=private-key",
					"GH_APP_PRIVATE_KEY_FILE=key.pem", "AWS_SECRET_ACCESS_KEY=cloud-secret",
					"NODE_OPTIONS=--require=untrusted.js", "COPILOT_HOME=original-home", "PATH=" + os.Getenv("PATH"),
					"SYSTEMROOT=" + os.Getenv("SYSTEMROOT"), "TEMP=" + os.TempDir(),
				},
			}
			err = s.runWithToken(t.Context(), installationToken{Token: "repository-test-token", ExpiresAt: time.Now().Add(time.Hour)})
			assert.True(t, revoked.Load())
			assert.NotContains(t, stderr.String(), "secret")
			assert.NotContains(t, stderr.String(), "repository-test-token")
			entries, readErr := os.ReadDir(configDir)
			require.NoError(t, readErr)
			assert.Empty(t, entries)
			if tt.startErr {
				require.ErrorContains(t, err, "process start failed")
				return
			}
			if tt.exitCode != 0 {
				var exitErr *exec.ExitError
				require.ErrorAs(t, err, &exitErr)
				assert.Equal(t, tt.exitCode, exitErr.ExitCode())
			}
			if tt.exitCode == 0 {
				require.NoError(t, err)
			}
			var child childSnapshot
			require.NoError(t, json.Unmarshal(stdout.Bytes(), &child))
			assert.Equal(t, root, child.CWD)
			assert.Equal(t, "repository-test-token", child.Env["GH_TOKEN"])
			assert.Equal(t, "owner/project", child.Env["GH_REPO"])
			assert.Equal(t, "copilot-account-secret", child.Env["COPILOT_GITHUB_TOKEN"])
			for _, name := range []string{"github_token", "GH_APP_PRIVATE_KEY", "GH_APP_PRIVATE_KEY_FILE", "AWS_SECRET_ACCESS_KEY", "NODE_OPTIONS"} {
				assert.NotContains(t, child.Env, name)
			}
			assert.Contains(t, child.Args, "--sandbox")
			assert.Contains(t, child.Args, "--disable-builtin-mcps")
			assert.Contains(t, child.Args, "--secret-env-vars=COPILOT_GITHUB_TOKEN")
			assert.Equal(t, append([]string{"--sandbox", "--disable-builtin-mcps", "--secret-env-vars=COPILOT_GITHUB_TOKEN"}, args...), child.Args)
			sandbox := child.Settings["sandbox"].(map[string]any)
			assert.Equal(t, true, sandbox["enabled"])
			assert.Equal(t, false, sandbox["allowBypass"])
			assert.Equal(t, false, sandbox["allowDevToolAccess"])
			auth := sandbox["auth"].(map[string]any)
			assert.Equal(t, false, auth["gh"])
			assert.Equal(t, false, auth["git"])
			policy := sandbox["userPolicy"].(map[string]any)
			filesystem := policy["filesystem"].(map[string]any)
			assert.Contains(t, filesystem["deniedPaths"], keyDir)
			assert.Contains(t, filesystem["deniedPaths"], configDir)
			credentials := sandbox["credentials"].(map[string]any)["envVars"].(map[string]any)
			injectHosts := credentials["GH_TOKEN"].(map[string]any)["injectHosts"]
			assert.ElementsMatch(t, []string{"github.com", "api.github.com", "uploads.github.com"}, injectHosts)
			assert.NotContains(t, fmt.Sprint(child.Settings), "copilot-account-secret")
			assert.NotContains(t, fmt.Sprint(child.Settings), "repository-test-token")
			assert.NoDirExists(t, child.Env["GH_CONFIG_DIR"])
			assert.NoDirExists(t, child.Env["COPILOT_HOME"])
		})
	}
}

func TestProjectConfigurationCannotEnableAdditionalCredentialRoutes(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		content string
		err     string
		servers []string
	}{
		{name: "empty project"},
		{name: "disable remote MCP", file: ".mcp.json", content: `{"mcpServers":{"github":{"type":"http","headers":{"Authorization":"secret"}}}}`,
			servers: []string{"github"}},
		{name: "weaken sandbox", file: filepath.Join(".github", "copilot", "settings.json"),
			content: `{"sandbox":{"enabled":false}}`, err: "conflicts"},
		{name: "enable hooks", file: filepath.Join(".claude", "settings.json"), content: `{"disableAllHooks":false}`, err: "conflicts"},
		{name: "enable plugins", file: filepath.Join(".github", "copilot", "settings.json"),
			content: `{"enabledPlugins":{"extra":true}}`, err: "conflicts"},
		{name: "active language server", file: filepath.Join(".github", "lsp.json"),
			content: `{"lspServers":{"custom":{"command":"untrusted"}}}`, err: "not supported"},
		{name: "disabled language server", file: filepath.Join(".github", "lsp.json"),
			content: `{"lspServers":{"custom":{"disabled":true}}}`},
		{name: "invalid config", file: ".mcp.json", content: "not JSON", err: "valid JSON"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			if tt.file != "" {
				path := filepath.Join(root, tt.file)
				require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
				require.NoError(t, os.WriteFile(path, []byte(tt.content), 0o600))
			}
			servers, err := projectConfiguration(root)
			if tt.err != "" {
				require.ErrorContains(t, err, tt.err)
				return
			}
			require.NoError(t, err)
			assert.ElementsMatch(t, tt.servers, servers)
		})
	}
}

func TestSupportedCopilotVersions(t *testing.T) {
	tests := []struct {
		name    string
		version string
		valid   bool
	}{
		{name: "minimum", version: "GitHub Copilot CLI 1.0.112.", valid: true},
		{name: "future", version: "GitHub Copilot CLI 2.0.0.", valid: true},
		{name: "older", version: "GitHub Copilot CLI 1.0.111."},
		{name: "unrelated executable", version: "something else 1.0.112"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCopilotVersion(tt.version)
			if tt.valid {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
		})
	}
}

func TestCopilotReadsGeneratedSandboxPolicy(t *testing.T) {
	binary := os.Getenv("GH_MINT_TEST_COPILOT")
	if binary == "" {
		t.Skip("set GH_MINT_TEST_COPILOT to opt into the installed CLI configuration check")
	}
	home := t.TempDir()
	data, err := json.Marshal(sandboxSettings([]string{filepath.Join(home, "private-key")}))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(home, "settings.json"), data, 0o600))
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "config", "sandbox")
	cmd.Env = childEnvironment(os.Environ(), map[string]string{"COPILOT_HOME": home})
	output, err := cmd.Output()
	require.NoError(t, err)
	var sandbox map[string]any
	require.NoError(t, json.Unmarshal(output, &sandbox))
	assert.Equal(t, true, sandbox["enabled"])
	assert.Equal(t, false, sandbox["allowBypass"])
	auth := sandbox["auth"].(map[string]any)
	assert.Equal(t, false, auth["gh"])
	assert.Equal(t, false, auth["git"])
	envVars := sandbox["credentials"].(map[string]any)["envVars"].(map[string]any)
	assert.Contains(t, envVars, "GH_TOKEN")
}
