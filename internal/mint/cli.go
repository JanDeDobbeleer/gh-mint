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

const help = `mint scopes GitHub App access to one repository and launches sandboxed Copilot.

Usage:
  mint setup --app-id ID --key-file PATH [options]
  mint setup
  mint setup --encrypt-key
  mint [COPILOT ARGUMENTS...]

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
  7. Run mint setup --app-id ID --key-file PATH.
     Start a session with mint in a checkout.

Permissions:
  The App installation is the source of truth. Tokens inherit all its granted
  permissions; there is no launcher permission override or read-only default.
  Change permissions in GitHub App settings and approve installation changes
  when required, then restart mint to mint a new token.
  Old configuration "permissions" fields are ignored and may be removed.
  Every token is restricted to the current repository, regardless of permission level.

Only github.com is supported. A GitHub App installation and separate Copilot
authentication are required. No token is printed or stored. Tokens expire after
one hour and are revoked on normal exit; there is no automatic renewal.

Setup asks for a passphrase twice, verifies the saved encrypted key, and offers
to delete the original only after configuration succeeds. Launches ask for the
passphrase in a hidden terminal prompt; no decrypted PEM is written to disk.

Each launch uses your usual Copilot home and requests the local sandbox.
Setup configures its saved policy to disable automatic Git and gh authentication
and bypass, and mask Mint's credentials. Run 'mint setup' to reapply those
settings. Launches check the policy without changing it and load temporary
instructions for authenticated Git HTTPS commands.
mint uses temporary GitHub CLI configuration and filters COPILOT_GITHUB_TOKEN
from shell and MCP environments.
The environment allowlist drops inherited GitHub tokens and other unapproved variables.
mint disables built-in and discovered project MCP servers, and rejects conflicting
project settings and active project language servers. Your own Copilot configuration
still applies. Built-in file tools are denied access to the key and mint configuration
directories; configure OS sandbox denies separately.

Copilot arguments are forwarded unchanged, except flags that disable sandboxing
or override secret filtering are rejected. Only pass arguments you trust.
The Copilot host process runs outside its local command sandbox. mint scopes the
token it issues; it is not an isolation boundary.
`

func Run(ctx context.Context, args []string, _ string, stdin io.Reader, stdout, stderr io.Writer) error {
	prompts := terminalPrompter{stdin: stdin, stderr: stderr}
	if len(args) > 0 && args[0] == "setup" {
		return setup(ctx, args[1:], stdout, stderr, prompts)
	}
	if err := checkLaunchArgs(args); err != nil {
		return err
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
	environ := os.Environ()
	if err := checkSandboxCredentials(ctx, cfg.Copilot, repo.Root, environ); err != nil {
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
		copilotCredential: credential,
		args:              args,
		denied:            denied,
		environ:           environ,
	}).runWithToken(ctx, token)
}

func setup(ctx context.Context, args []string, stdout, stderr io.Writer, prompts keyPrompter) error {
	flags := flag.NewFlagSet("mint setup", flag.ContinueOnError)
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
		return errors.New("invalid setup arguments; run 'mint setup --help'")
	}
	if home := os.Getenv("COPILOT_HOME"); home != "" && !filepath.IsAbs(home) {
		return errors.New("inherited COPILOT_HOME must be absolute")
	}
	if len(args) == 0 {
		return configureSetup(ctx, stdout, stderr)
	}
	if *encryptKey {
		otherFlags := false
		flags.Visit(func(f *flag.Flag) { otherFlags = otherFlags || f.Name != "encrypt-key" })
		if flags.NArg() != 0 || otherFlags {
			return errors.New("use 'mint setup --encrypt-key' without other setup options")
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
		if _, err := fmt.Fprintln(stdout, "Configured Copilot credential policy in your existing settings. Tool and cache grants are unchanged."); err != nil {
			return err
		}
		return encryptSetupKey(dir, cfg, prompts, stdout, true)
	}
	if flags.NArg() != 0 || *keyFile == "" {
		return errors.New("--app-id and --key-file are required; run 'mint setup --help'")
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
		return errors.New("existing configuration is never overwritten by setup; use 'mint setup --encrypt-key' to encrypt its key")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("cannot create Mint configuration directory: %w", err)
	}
	if err := applySandboxSettings(ctx, binary, dir, os.Environ(), stderr); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(stdout, "Configured Copilot credential policy in your existing settings. Tool and cache grants are unchanged."); err != nil {
		return err
	}
	return encryptSetupKey(dir, cfg, prompts, stdout, false)
}

func configureSetup(ctx context.Context, stdout, stderr io.Writer) error {
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
	_, err = fmt.Fprintln(stdout, "Configured Copilot credential policy in your existing settings. Tool and cache grants are unchanged.")
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
