package mint

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"slices"
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
	keyDirectory      string
	copilotCredential string
	args              []string
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

	args := []string{"--sandbox", "--secret-env-vars=COPILOT_GITHUB_TOKEN,GITHUB_TOKEN", "--disable-builtin-mcps"}
	for _, path := range []string{s.keyDirectory, filepath.Join(s.keyDirectory, "**")} {
		args = append(args, "--deny-tool=read("+path+")", "--deny-tool=write("+path+")")
	}
	args = append(args, s.args...)
	cmd := exec.CommandContext(ctx, s.cfg.Copilot, args...)
	cmd.Dir = s.repo.Root
	cmd.Env = append(slices.Clone(s.environ),
		"GH_TOKEN="+token.Token,
		"GH_HOST=github.com",
		"GH_REPO="+s.repo.Owner+"/"+s.repo.Name,
		"COPILOT_GITHUB_TOKEN="+s.copilotCredential,
	)
	cmd.Stdin = s.stdin
	cmd.Stdout = s.stdout
	cmd.Stderr = s.stderr
	if _, err := fmt.Fprintf(s.stderr, "gh mint: %s/%s; repository token expires at %s\n",
		s.repo.Owner, s.repo.Name, token.ExpiresAt.Local().Format(time.RFC3339)); err != nil {
		return errors.New("cannot report launch status")
	}
	return s.execute(cmd)
}
