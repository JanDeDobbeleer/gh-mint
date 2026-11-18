package mint

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const help = `Mint a single-repository GitHub App token and launch sandboxed Copilot.

Usage:
  gh mint setup --app-id ID --key-file PATH [options]
  gh mint setup --encrypt-key
  gh mint [COPILOT ARGUMENTS...]

Setup options:
  --app-id ID          GitHub App numeric ID
  --key-file PATH      RSA PEM to encrypt and import into mint's key directory
  --encrypt-key        Encrypt the key from an existing mint configuration
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

Setup asks for a passphrase twice, verifies the saved encrypted key, and offers
to delete the original only after configuration succeeds. Launches ask for the
passphrase in a hidden terminal prompt; no decrypted PEM is written to disk.

Each launch uses a temporary Copilot home and GitHub CLI configuration, requests
the local sandbox, and masks COPILOT_GITHUB_TOKEN from shell and MCP environments.
The environment allowlist drops inherited GitHub tokens and other unapproved variables.
Mint disables hooks, extensions, built-in MCP servers, and discovered project MCP servers.
Project settings that weaken this policy and active project language servers are rejected.
The key directory, mint configuration, and common credential paths are denied to
sandboxed tools on a best-effort basis.

All Copilot arguments are forwarded unchanged and can weaken these protections.
The Copilot host process runs outside its local command sandbox. Mint scopes the
token it issues; it is not an isolation boundary.
`

func Run(ctx context.Context, args []string, _ string, stdin io.Reader, stdout, stderr io.Writer) error {
	prompts := terminalPrompter{stdin: stdin, stderr: stderr}
	if len(args) > 0 && args[0] == "setup" {
		return setup(args[1:], stdout, stderr, prompts)
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
	key, err := readKey(cfg.KeyFile, prompts.password)
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

func setup(args []string, stdout, stderr io.Writer, prompts keyPrompter) error {
	flags := flag.NewFlagSet("gh mint setup", flag.ContinueOnError)
	flags.SetOutput(stderr)
	appID := flags.String("app-id", "", "GitHub App numeric ID")
	keyFile := flags.String("key-file", "", "path to an RSA PEM file")
	remote := flags.String("remote", "origin", "Git remote")
	copilot := flags.String("copilot", "", "trusted Copilot executable")
	encryptKey := flags.Bool("encrypt-key", false, "encrypt the key in an existing configuration")
	flags.Usage = func() { _, _ = fmt.Fprint(stderr, help) }
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errors.New("invalid setup arguments; run 'gh mint setup --help'")
	}
	if *encryptKey {
		otherFlags := false
		flags.Visit(func(f *flag.Flag) { otherFlags = otherFlags || f.Name != "encrypt-key" })
		if flags.NArg() != 0 || otherFlags {
			return errors.New("use 'gh mint setup --encrypt-key' without other setup options")
		}
		dir, err := configDirectory()
		if err != nil {
			return err
		}
		cfg, err := readConfig(dir)
		if err != nil {
			return err
		}
		return encryptSetupKey(dir, cfg, prompts, stdout, true)
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
	if err := cfg.validate(); err != nil {
		return err
	}
	if _, err := os.Lstat(filepath.Join(dir, "config.json")); !errors.Is(err, os.ErrNotExist) {
		return errors.New("existing configuration is never overwritten by setup; use 'gh mint setup --encrypt-key' to encrypt its key")
	}
	return encryptSetupKey(dir, cfg, prompts, stdout, false)
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
