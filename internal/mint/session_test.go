package mint

import (
	"bytes"
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
	if home := os.Getenv("COPILOT_HOME"); home != "" {
		data, err := os.ReadFile(filepath.Join(home, "settings.json"))
		if err != nil {
			return 91
		}
		if slices.Equal(os.Args[1:], []string{"config", "sandbox"}) {
			var settings map[string]json.RawMessage
			if json.Unmarshal(data, &settings) != nil || len(settings["sandbox"]) == 0 {
				return 91
			}
			fmt.Println(string(settings["sandbox"]))
			return 0
		}
		if json.Unmarshal(data, &snapshot.Settings) != nil {
			return 91
		}
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
		name       string
		prompt     string
		args       []string
		exitCode   int
		startErr   bool
		customHome bool
	}{
		{name: "custom home", customHome: true, args: []string{"-p", "explain this repository", "-s", "--model=example", "--resume", "--", "a b", ""}},
		{name: "default home", args: []string{"-C", "other"}},
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
				copilotCredential: "copilot-account-secret",
				args:              args, denied: []string{keyDir},
				environ: []string{
					"GH_TOKEN=broad-user-secret", "github_token=other-secret", "GH_APP_PRIVATE_KEY=private-key",
					"GH_APP_PRIVATE_KEY_FILE=key.pem", "AWS_SECRET_ACCESS_KEY=cloud-secret",
					"NODE_OPTIONS=--require=untrusted.js", "PATH=" + os.Getenv("PATH"),
					"SYSTEMROOT=" + os.Getenv("SYSTEMROOT"), "TEMP=" + os.TempDir(),
				},
			}
			const settings = `{"sandbox":{"allowDevToolAccess":true},"model":"example"}`
			var home string
			if tt.customHome {
				home = t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(home, "settings.json"), []byte(settings), 0o600))
				s.environ = append(s.environ, "COPILOT_HOME="+home)
			}
			err = s.runWithToken(t.Context(), installationToken{Token: "repository-test-token", ExpiresAt: time.Now().Add(time.Hour)})
			assert.True(t, revoked.Load())
			assert.NotContains(t, stderr.String(), "secret")
			assert.NotContains(t, stderr.String(), "repository-test-token")
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
			tree := filepath.Join(keyDir, "**")
			assert.Equal(t, append([]string{"--sandbox", "--disable-builtin-mcps", "--secret-env-vars=COPILOT_GITHUB_TOKEN",
				"--deny-tool=read(" + keyDir + ")", "--deny-tool=read(" + tree + ")",
				"--deny-tool=write(" + keyDir + ")", "--deny-tool=write(" + tree + ")"}, args...), child.Args)
			if tt.customHome {
				assert.Equal(t, home, child.Env["COPILOT_HOME"])
				assert.Equal(t, "example", child.Settings["model"])
				assert.Equal(t, map[string]any{"allowDevToolAccess": true}, child.Settings["sandbox"])
				data, readErr := os.ReadFile(filepath.Join(home, "settings.json"))
				require.NoError(t, readErr)
				assert.Equal(t, settings, string(data))
			} else {
				assert.NotContains(t, child.Env, "COPILOT_HOME")
			}
			assert.NoDirExists(t, child.Env["GH_CONFIG_DIR"])
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

const scopedSandboxSettings = `{"sandbox":{"allowBypass":false,"auth":{"git":false,"gh":false},"credentials":{"envVars":{"GH_TOKEN":{"injectHosts":["github.com","uploads.github.com","api.github.com"]}}}}}`

func TestSandboxCredentialCheckUsesSavedSettings(t *testing.T) {
	binary := fakeCopilotBinary(t)
	tests := []struct{ name, settings, wantErr string }{
		{"scoped settings", scopedSandboxSettings, ""},
		{"personal git", strings.Replace(scopedSandboxSettings, `"git":false`, `"git":true`, 1), "sandbox.auth.git"},
		{"default gh", strings.Replace(scopedSandboxSettings, `,"gh":false`, "", 1), "sandbox.auth.gh"},
		{"default bypass", strings.Replace(scopedSandboxSettings, `"allowBypass":false,`, "", 1), "sandbox.allowBypass"},
		{"missing token mapping", strings.Replace(scopedSandboxSettings, `"GH_TOKEN"`, `"OTHER_TOKEN"`, 1), "injectHosts"},
		{"extra host", strings.Replace(scopedSandboxSettings, `"api.github.com"`, `"api.github.com","example.com"`, 1), "injectHosts"},
		{"no sandbox settings", `{}`, "sandbox.auth.git"},
	}
	if os.PathSeparator == '\\' {
		tests = append(tests, struct{ name, settings, wantErr string }{
			"Windows credential proxy disabled",
			strings.Replace(scopedSandboxSettings, `"allowBypass":false`, `"allowBypass":false,"userPolicy":{"network":{"allowLocalNetwork":false}}`, 1),
			"sandbox.userPolicy.network.allowLocalNetwork",
		})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			check := checkSandboxSettingsFile(t, binary, tt.settings)
			if tt.wantErr == "" {
				require.NoError(t, check)
			} else {
				require.ErrorContains(t, check, tt.wantErr)
			}
		})
	}
}

func TestCopilotReadsSavedSandboxPolicy(t *testing.T) {
	binary := os.Getenv("GH_MINT_TEST_COPILOT")
	if binary == "" {
		t.Skip("set GH_MINT_TEST_COPILOT to opt into the installed CLI configuration check")
	}
	require.NoError(t, checkSandboxSettingsFile(t, binary, scopedSandboxSettings))
}

func TestApplySandboxSettingsWithCopilot(t *testing.T) {
	binary := os.Getenv("GH_MINT_TEST_COPILOT")
	if binary == "" {
		t.Skip("set GH_MINT_TEST_COPILOT to opt into the installed CLI configuration check")
	}
	home := t.TempDir()
	t.Setenv("COPILOT_HOME", home)
	path := filepath.Join(home, "settings.json")
	const initial = `{"theme":"dark","sandbox":{"allowDevToolAccess":false,"allowBypass":true,
"auth":{"git":true,"gh":true},
"credentials":{"envVars":{"GH_TOKEN":{"injectHosts":["old.example.com"]},"OTHER_TOKEN":{"injectHosts":["keep.example.com"]}}},
"userPolicy":{"filesystem":{"readonlyPaths":["~/toolchain"],"readwritePaths":["~/build-cache"]},"network":{"allowLocalNetwork":false}}}}`
	require.NoError(t, os.WriteFile(path, []byte(initial), 0o600))
	var stderr bytes.Buffer
	require.NoError(t, applySandboxSettings(t.Context(), binary, home, os.Environ(), &stderr), stderr.String())
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var settings map[string]any
	require.NoError(t, json.Unmarshal(data, &settings))
	assert.Equal(t, "dark", settings["theme"])
	sandbox := settings["sandbox"].(map[string]any)
	assert.Equal(t, false, sandbox["allowDevToolAccess"])
	userPolicy := sandbox["userPolicy"].(map[string]any)
	assert.Equal(t, map[string]any{"readonlyPaths": []any{"~/toolchain"}, "readwritePaths": []any{"~/build-cache"}}, userPolicy["filesystem"])
	assert.Equal(t, os.PathSeparator == '\\', userPolicy["network"].(map[string]any)["allowLocalNetwork"])
	envVars := sandbox["credentials"].(map[string]any)["envVars"].(map[string]any)
	assert.Equal(t, map[string]any{"injectHosts": []any{"keep.example.com"}}, envVars["OTHER_TOKEN"])
	require.NoError(t, applySandboxSettings(t.Context(), binary, home, os.Environ(), &stderr), stderr.String())
	unchanged, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, data, unchanged)
}

func checkSandboxSettingsFile(t *testing.T, binary, settings string) error {
	t.Helper()
	home := t.TempDir()
	t.Setenv("COPILOT_HOME", home)
	require.NoError(t, os.WriteFile(filepath.Join(home, "settings.json"), []byte(settings), 0o600))
	return checkSandboxCredentials(t.Context(), binary, t.TempDir(), os.Environ())
}
