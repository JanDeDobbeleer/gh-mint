package mint

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

var githubTokenHosts = [...]string{"api.github.com", "github.com", "uploads.github.com"}

func applySandboxSettings(ctx context.Context, binary, dir string, environ []string, stderr io.Writer) error {
	if checkSandboxCredentials(ctx, binary, dir, environ) == nil {
		return nil
	}
	const hostsKey = "sandbox.credentials.envVars.GH_TOKEN.injectHosts"
	const gitHostsKey = "sandbox.credentials.envVars.MINT_GIT_AUTH.injectHosts"
	edits := [][]string{
		{"sandbox.allowBypass", "false"},
		{"sandbox.auth.git", "false"},
		{"sandbox.auth.gh", "false"},
		{"--rm", hostsKey},
		{"--rm", gitHostsKey},
		{gitHostsKey, "github.com"},
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
