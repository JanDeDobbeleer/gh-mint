package mint

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

var githubTokenHosts = [...]string{"api.github.com", "github.com", "uploads.github.com"}

func configureSandbox(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) != 0 {
		if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
			_, err := fmt.Fprintln(stdout, `Usage: gh mint config

After setup, apply Mint's credential policy to your existing Copilot settings:
disable automatic git/gh authentication and bypass, and mask GH_TOKEN for GitHub.com.
On Windows, enable local network access so the sandbox can reach the credential proxy.
Tool paths and other settings are left unchanged. This affects all sandboxed Copilot
sessions using that home. It does not mint a token.`)
			return err
		}
		return errors.New("use 'gh mint config' without arguments")
	}
	if home := os.Getenv("COPILOT_HOME"); home != "" && !filepath.IsAbs(home) {
		return errors.New("inherited COPILOT_HOME must be absolute")
	}
	dir, err := configDirectory()
	if err != nil {
		return err
	}
	cfg, err := readConfig(dir)
	if err != nil {
		return err
	}

	if err := applySandboxSettings(ctx, cfg.Copilot, dir, os.Environ(), stderr); err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, "Configured Mint's credential policy in your Copilot settings. Tool and cache grants are unchanged.")
	return err
}

func applySandboxSettings(ctx context.Context, binary, dir string, environ []string, stderr io.Writer) error {
	if checkSandboxCredentials(ctx, binary, dir, environ) == nil {
		return nil
	}
	const hostsKey = "sandbox.credentials.envVars.GH_TOKEN.injectHosts"
	edits := [][]string{
		{"sandbox.allowBypass", "false"},
		{"sandbox.auth.git", "false"},
		{"sandbox.auth.gh", "false"},
		{"--rm", hostsKey},
	}
	for _, host := range githubTokenHosts {
		edits = append(edits, []string{hostsKey, host})
	}
	if os.PathSeparator == '\\' {
		edits = append(edits, []string{"sandbox.userPolicy.network.allowLocalNetwork", "true"})
	}
	env := childEnvironment(environ, nil)
	for _, edit := range edits {
		editCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		cmd := exec.CommandContext(editCtx, binary, append([]string{"config", "--global"}, edit...)...)
		cmd.Dir = dir
		cmd.Env = env
		cmd.Stderr = stderr
		err := cmd.Run()
		cancel()
		if err != nil {
			return fmt.Errorf("cannot apply Copilot setting %q: %w", edit[0], err)
		}
	}
	return checkSandboxCredentials(ctx, binary, dir, environ)
}
