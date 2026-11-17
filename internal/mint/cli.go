package mint

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

const help = `Mint a single-repository GitHub App token and launch sandboxed Copilot.

Usage:
  gh mint setup --app-id ID --key-file PATH [options]
  gh mint [COPILOT ARGUMENTS...]

Setup options:
  --app-id ID          GitHub App numeric ID
  --key-file PATH      Unencrypted RSA PEM outside your Git working trees
  --remote NAME       Git remote to scope against (default: origin)
  --copilot PATH      Trusted Copilot executable (default: resolved from PATH)

Create and install a GitHub App:
  1. Open https://github.com/settings/apps/new (or organization developer settings).
  2. Set a unique name and homepage URL. Leave callback/setup URLs empty,
     user authorization and device flow disabled, and Webhooks > Active unchecked.
  3. Grant only the App permissions your sessions need, with write access where
     needed. The launcher inherits the installation's granted permissions.
  4. Choose installation availability: Only on this account, or Any account
     for other accounts/organizations. Create the App and copy its numeric App ID.
  5. Open Credentials > Key pairs > New key. Secure the downloaded PEM in a
     dedicated directory outside checkouts; remove leftover download copies.
  6. Select Install App > target account > Only select repositories.
  7. Run gh mint setup --app-id ID --key-file PATH, then gh mint in a checkout.

Permissions:
  The App installation is the source of truth. Tokens inherit all its granted
  permissions; there is no launcher permission override or read-only default.
  Change permissions in GitHub App settings and approve installation changes
  when required, then restart gh mint to mint a new token.
  Old configuration "permissions" fields are ignored and may be removed.
  Every token is restricted to the current repository, regardless of permission level.

Only github.com is supported. A GitHub App installation and separate Copilot
authentication are required. No token is printed or stored. Tokens expire after
one hour and are revoked on normal exit; there is no automatic renewal.

This requests Copilot's local command sandbox, not an isolated Copilot host.
Protecting against an unsandboxed host requires a separate-identity broker.
`

func Run(ctx context.Context, args []string, _ string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) > 0 && args[0] == "setup" {
		return setup(args[1:], stdout, stderr)
	}
	dir, err := configDirectory()
	if err != nil {
		return err
	}
	cfg, err := readConfig(dir)
	if err != nil {
		return err
	}
	repo, err := discoverRepository(ctx, cfg.Remote)
	if err != nil {
		return err
	}
	if _, err := projectConfiguration(repo.Root); err != nil {
		return err
	}
	denied, err := protectPaths(repo, dir, cfg)
	if err != nil {
		return err
	}
	if within(repo.Root, cfg.Copilot) {
		return errors.New("copilot executable must be outside the working tree")
	}
	if err := checkCopilot(ctx, cfg.Copilot); err != nil {
		return err
	}
	credential, err := copilotCredential(ctx)
	if err != nil {
		return err
	}
	key, err := readKey(cfg.KeyFile)
	if err != nil {
		return err
	}
	client := newGitHubClient()
	token, err := client.mint(ctx, cfg, repo, key)
	if err != nil {
		return err
	}

	return (session{
		github:            client,
		execute:           (*exec.Cmd).Run,
		stdin:             stdin,
		stdout:            stdout,
		stderr:            stderr,
		cfg:               cfg,
		repo:              repo,
		configDir:         dir,
		copilotCredential: credential,
		args:              args,
		denied:            denied,
		environ:           os.Environ(),
	}).runWithToken(ctx, token)
}

func setup(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("gh mint setup", flag.ContinueOnError)
	flags.SetOutput(stderr)
	appID := flags.String("app-id", "", "GitHub App numeric ID")
	keyFile := flags.String("key-file", "", "path to an RSA PEM file")
	remote := flags.String("remote", "origin", "Git remote")
	copilot := flags.String("copilot", "", "trusted Copilot executable")
	flags.Usage = func() { _, _ = fmt.Fprint(stderr, help) }
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errors.New("invalid setup arguments; run 'gh mint setup --help'")
	}
	if flags.NArg() != 0 || *keyFile == "" {
		return errors.New("--app-id and --key-file are required; run 'gh mint setup --help'")
	}
	id, err := parseAppID(*appID)
	if err != nil {
		return err
	}
	key, err := canonicalPath(*keyFile)
	if err != nil {
		return err
	}
	if _, err := readKey(key); err != nil {
		return err
	}
	if *copilot == "" {
		*copilot, err = exec.LookPath("copilot")
		if err != nil {
			return errors.New("copilot is required on PATH, or supply --copilot with a trusted executable path")
		}
	}
	binary, err := canonicalPath(*copilot)
	if err != nil {
		return err
	}
	dir, err := configDirectory()
	if err != nil {
		return err
	}
	cfg := config{AppID: id, KeyFile: key, Copilot: binary, Remote: *remote}
	if err := writeConfig(dir, cfg); err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, "Configured gh-mint. Protect the private key directory with your OS permissions, then run 'gh mint' inside a Git repository.")
	return err
}

func copilotCredential(ctx context.Context) (string, error) {
	if token := os.Getenv("COPILOT_GITHUB_TOKEN"); token != "" {
		if strings.ContainsAny(token, " \t\r\n\x00") {
			return "", errors.New("invalid COPILOT_GITHUB_TOKEN")
		}
		return token, nil
	}
	gh, err := exec.LookPath("gh")
	if err != nil {
		return "", errors.New("GitHub CLI is required")
	}
	authCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	command := exec.CommandContext(authCtx, gh, "auth", "token", "--hostname", "github.com")
	output, err := command.Output()
	if err != nil {
		return "", errors.New("cannot obtain Copilot authentication; set COPILOT_GITHUB_TOKEN or authenticate with 'gh auth login'")
	}
	token := strings.TrimSpace(string(output))
	if token == "" || strings.ContainsAny(token, " \t\r\n\x00") {
		return "", errors.New("GitHub CLI did not return a valid Copilot authentication credential")
	}
	return token, nil
}
