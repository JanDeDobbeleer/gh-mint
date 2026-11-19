package mint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

type session struct {
	github            *githubClient
	execute           func(*exec.Cmd) error
	stdin             io.Reader
	stdout            io.Writer
	stderr            io.Writer
	cfg               config
	repo              repository
	copilotCredential string
	args              []string
	denied            []string
	environ           []string
}

func (s session) runWithToken(ctx context.Context, token installationToken) error {
	defer func() {
		revokeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.github.revoke(revokeCtx, token.Token); err != nil {
			_, _ = fmt.Fprintln(s.stderr, "gh mint: could not revoke the installation token; it will expire automatically")
		}
	}()

	servers, err := projectConfiguration(s.repo.Root)
	if err != nil {
		return err
	}
	ghHome, err := os.MkdirTemp("", "gh-mint-gh-")
	if err != nil {
		return errors.New("cannot create an isolated GitHub CLI configuration directory")
	}
	defer cleanupDirectory(ghHome, s.stderr)
	if err := os.WriteFile(filepath.Join(ghHome, "gitconfig"), nil, 0o600); err != nil {
		return errors.New("cannot create an isolated Git configuration")
	}

	args := []string{"--sandbox", "--disable-builtin-mcps", "--secret-env-vars=COPILOT_GITHUB_TOKEN"}
	for _, path := range s.denied {
		for _, kind := range []string{"read", "write"} {
			args = append(args, "--deny-tool="+kind+"("+path+")", "--deny-tool="+kind+"("+filepath.Join(path, "**")+")")
		}
	}
	for _, name := range servers {
		args = append(args, "--disable-mcp-server="+name)
	}
	args = append(args, s.args...)
	cmd := exec.CommandContext(ctx, s.cfg.Copilot, args...)
	cmd.Dir = s.repo.Root
	cmd.Env = childEnvironment(s.environ, map[string]string{
		"GH_TOKEN":                        token.Token,
		"GH_CONFIG_DIR":                   ghHome,
		"GH_HOST":                         "github.com",
		"GH_REPO":                         s.repo.Owner + "/" + s.repo.Name,
		"GH_PROMPT_DISABLED":              "1",
		"GIT_CONFIG_GLOBAL":               filepath.Join(ghHome, "gitconfig"),
		"GIT_CONFIG_NOSYSTEM":             "1",
		"GIT_TERMINAL_PROMPT":             "0",
		"COPILOT_GITHUB_TOKEN":            s.copilotCredential,
		"COPILOT_DISABLE_LOGIN_SHELL_ENV": "1",
	})
	cmd.Stdin = s.stdin
	cmd.Stdout = s.stdout
	cmd.Stderr = s.stderr
	if _, err := fmt.Fprintf(s.stderr, "gh mint: %s/%s; repository token expires at %s; sandbox requested\n",
		s.repo.Owner, s.repo.Name, token.ExpiresAt.Local().Format(time.RFC3339)); err != nil {
		return errors.New("cannot report launch status")
	}
	return s.execute(cmd)
}

func cleanupDirectory(path string, stderr io.Writer) {
	if err := os.RemoveAll(path); err != nil {
		_, _ = fmt.Fprintln(stderr, "gh mint: could not remove an isolated session directory")
	}
}

func checkLaunchArgs(args []string) error {
	for _, arg := range args {
		if arg == "--" {
			break
		}
		flag, _, _ := strings.Cut(arg, "=")
		switch flag {
		case "--no-sandbox", "--secret-env-vars":
			return fmt.Errorf("%s would override Mint's sandbox protections", flag)
		case "--sandbox":
			if arg != flag && arg != "--sandbox=true" {
				return errors.New("sandboxing is required by Mint")
			}
		}
	}
	return nil
}

func checkSandboxCredentials(ctx context.Context, binary, root string, environ []string) error {
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, binary, "config", "sandbox")
	cmd.Dir = root
	cmd.Env = childEnvironment(environ, nil)
	output, err := cmd.Output()
	var policy struct {
		Auth struct {
			Git *bool `json:"git"`
			GH  *bool `json:"gh"`
		} `json:"auth"`
		Credentials struct {
			EnvVars map[string]struct {
				InjectHosts []string `json:"injectHosts"`
			} `json:"envVars"`
		} `json:"credentials"`
		UserPolicy struct {
			Network struct {
				AllowLocalNetwork *bool `json:"allowLocalNetwork"`
			} `json:"network"`
		} `json:"userPolicy"`
		AllowBypass *bool `json:"allowBypass"`
	}
	if err != nil || json.Unmarshal(output, &policy) != nil ||
		policy.Auth.Git == nil || *policy.Auth.Git || policy.Auth.GH == nil || *policy.Auth.GH {
		return errors.New("saved Copilot settings must explicitly disable sandbox.auth.git and sandbox.auth.gh and configure scoped GH_TOKEN masking; run 'gh mint config'")
	}
	if policy.AllowBypass == nil || *policy.AllowBypass {
		return errors.New("sandbox.allowBypass must be false in saved Copilot settings; run 'gh mint config'")
	}
	if allow := policy.UserPolicy.Network.AllowLocalNetwork; os.PathSeparator == '\\' && allow != nil && !*allow {
		return errors.New("sandbox.userPolicy.network.allowLocalNetwork must not be disabled on Windows; run 'gh mint config' to enable access to the credential proxy")
	}
	hosts := policy.Credentials.EnvVars["GH_TOKEN"].InjectHosts
	slices.Sort(hosts)
	if !slices.Equal(slices.Compact(hosts), githubTokenHosts[:]) {
		return errors.New("sandbox.credentials.envVars.GH_TOKEN.injectHosts must contain exactly github.com, api.github.com, and uploads.github.com; run 'gh mint config'")
	}
	return nil
}

func childEnvironment(environ []string, overrides map[string]string) []string {
	allowed := map[string]bool{}
	for name := range strings.FieldsSeq(`PATH PATHEXT SYSTEMROOT WINDIR COMSPEC SYSTEMDRIVE COPILOT_HOME
		HOME USERPROFILE HOMEDRIVE HOMEPATH APPDATA LOCALAPPDATA PROGRAMDATA ALLUSERSPROFILE
		PROGRAMFILES PROGRAMFILES(X86) PROGRAMW6432 COMMONPROGRAMFILES COMMONPROGRAMFILES(X86)
		TEMP TMP TMPDIR USER USERNAME USERDOMAIN LOGNAME SHELL PSModulePath
		LANG LANGUAGE TERM COLORTERM TERM_PROGRAM TERM_PROGRAM_VERSION NO_COLOR CLICOLOR
		WT_SESSION WT_PROFILE_ID NUMBER_OF_PROCESSORS PROCESSOR_ARCHITECTURE
		SSL_CERT_FILE SSL_CERT_DIR NODE_EXTRA_CA_CERTS`) {
		allowed[strings.ToUpper(name)] = true
	}
	env := make([]string, 0, len(environ)+len(overrides))
	for _, entry := range environ {
		name, _, found := strings.Cut(entry, "=")
		upper := strings.ToUpper(name)
		if !found || (!allowed[upper] && !strings.HasPrefix(upper, "LC_")) {
			continue
		}
		if _, overridden := overrides[upper]; overridden {
			continue
		}
		env = append(env, entry)
	}
	names := make([]string, 0, len(overrides))
	for name := range overrides {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		env = append(env, name+"="+overrides[name])
	}
	return env
}

func projectConfiguration(root string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(root, ".github", "lsp.json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("cannot inspect project language server configuration")
	}
	if err == nil {
		var document struct {
			Servers map[string]struct {
				Disabled bool `json:"disabled"`
			} `json:"lspServers"`
		}
		if json.Unmarshal(data, &document) != nil {
			return nil, errors.New("project language server configuration must contain valid JSON")
		}
		for _, server := range document.Servers {
			if !server.Disabled {
				return nil, errors.New("project language servers are not supported because their credential filtering is not guaranteed")
			}
		}
	}
	for _, name := range []string{
		filepath.Join(".github", "copilot", "settings.json"),
		filepath.Join(".github", "copilot", "settings.local.json"),
		filepath.Join(".claude", "settings.json"),
		filepath.Join(".claude", "settings.local.json"),
	} {
		data, err := os.ReadFile(filepath.Join(root, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, errors.New("cannot inspect project settings")
		}
		var values map[string]json.RawMessage
		if json.Unmarshal(data, &values) != nil {
			return nil, errors.New("project settings must contain valid JSON")
		}
		for _, key := range []string{"sandbox", "extensions", "enabledPlugins", "extraKnownMarketplaces", "disableAllHooks", "enabledMcpServers"} {
			if _, found := values[key]; found {
				return nil, fmt.Errorf("project setting %q conflicts with the isolated launcher policy", key)
			}
		}
	}

	names := make(map[string]bool)
	for _, name := range []string{".mcp.json", filepath.Join(".github", "mcp.json")} {
		data, err := os.ReadFile(filepath.Join(root, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, errors.New("cannot inspect project MCP configuration")
		}
		var document struct {
			Servers map[string]json.RawMessage `json:"mcpServers"`
		}
		if json.Unmarshal(data, &document) != nil {
			return nil, errors.New("project MCP configuration must contain valid JSON")
		}
		for server := range document.Servers {
			if server == "" || strings.ContainsAny(server, "\r\n\x00") {
				return nil, errors.New("invalid project MCP server name")
			}
			names[server] = true
		}
	}
	result := make([]string, 0, len(names))
	for name := range names {
		result = append(result, name)
	}
	slices.Sort(result)
	return result, nil
}

var copilotVersion = regexp.MustCompile(`GitHub Copilot CLI (\d+)\.(\d+)\.(\d+)`)

func checkCopilot(ctx context.Context, path string) error {
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, path, "--sandbox", "--version")
	cmd.Env = childEnvironment(os.Environ(), nil)
	output, err := cmd.Output()
	if err != nil {
		return errors.New("copilot must support --sandbox; install GitHub Copilot CLI 1.0.112 or later")
	}
	return validateCopilotVersion(string(output))
}

func validateCopilotVersion(output string) error {
	matches := copilotVersion.FindStringSubmatch(output)
	if len(matches) != 4 {
		return errors.New("cannot identify the GitHub Copilot CLI version")
	}
	major, _ := strconv.Atoi(matches[1])
	minor, _ := strconv.Atoi(matches[2])
	patch, _ := strconv.Atoi(matches[3])
	if major < 1 || (major == 1 && minor == 0 && patch < 112) {
		return errors.New("GitHub Copilot CLI 1.0.112 or later is required")
	}
	return nil
}
