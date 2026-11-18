# gh-mint

Launch GitHub Copilot CLI with a short-lived GitHub App token restricted to the current repository.

```powershell
gh mint
```

`gh-mint` is a compiled GitHub CLI extension with no third-party runtime dependencies. It signs an App JWT on the
host, discovers the installation for the configured remote, and requests an installation token with exactly one
repository, inheriting the App installation's granted permissions. Your normal GitHub CLI login is not replaced.

**This is a credential-scoping launcher, not a complete isolation boundary around Copilot.**
Read [Security](SECURITY.md) before trusting it with a GitHub App private key.

## Requirements

- GitHub CLI and Git.
- GitHub Copilot CLI.
- A GitHub App installed on the repositories you intend to use.
- A separate credential authorized to use Copilot.

Only GitHub.com is supported. GitHub Enterprise support is intentionally absent rather than forwarding credentials
to a host inferred from an untrusted remote.

## Install

Install the precompiled extension with GitHub CLI:

```powershell
gh extension install JanDeDobbeleer/gh-mint
gh mint setup --help
```

Go is not required to install or run the released binary.

## Configure a GitHub App

1. Open [New GitHub App](https://github.com/settings/apps/new) for a personally owned App. For organization
   ownership, use the organization's Settings > Developer settings > GitHub Apps > New GitHub App.
2. Enter a unique App name and a homepage URL, such as your GitHub profile. Leave callback and setup URLs empty.
   Leave user authorization and device flow disabled, and uncheck Webhooks > Active. This launcher uses installation
   tokens, not user OAuth authorization.
3. Grant only the App permissions your sessions need. For repository work, Contents, Issues, and Pull requests are
   common choices. Choose Read-only for inspection or Read & write for resources you intend to modify. The launcher
   inherits all permissions granted to the installation, so avoid unnecessary organization or account privileges.
4. Choose Only on this account for installations on the owning account, or Any account if you also need installations
   on other accounts or organizations. Create the App and copy its numeric **App ID**, not its Client ID.
5. In the App settings, open Credentials > Key pairs > New key. Move the downloaded RSA PEM into a dedicated directory
   outside your Git checkouts, such as `C:\secure\gh-mint\app.pem`. Remove any leftover copy from Downloads. Never
   commit the key or share its contents.
6. Restrict that directory with your operating system's access controls. On Windows, Go's `0600` file mode does not
   replace NTFS ACLs. Setup imports the key into mint's key directory with passphrase encryption and offers to
   delete the original after verification. Remove any other unencrypted copies yourself.
7. Select Install App, choose the target account, then Only select repositories. Select the repositories where you
   will use the launcher. Each session still receives access to just the repository it starts in.

GitHub documents [App registration][app-registration], [private keys][app-keys], and
[installing your App][app-installation] in more detail.

[app-registration]: https://docs.github.com/en/apps/creating-github-apps/registering-a-github-app/registering-a-github-app
[app-keys]: https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/managing-private-keys-for-github-apps
[app-installation]: https://docs.github.com/en/apps/using-github-apps/installing-your-own-github-app

### Configure the launcher

Configure the extension once:

```powershell
gh mint setup --app-id 123456 --key-file "C:\secure\gh-mint\app.pem"
```

Initial setup never overwrites an existing configuration. Review and edit the existing configuration to change its values,
or use the explicit key-encryption migration below.

Setup asks for a new passphrase twice using hidden terminal prompts, writes an encrypted PKCS#8 key under
`keys/app.encrypted.pem` beside mint's configuration, and verifies that it decrypts to the original key before
saving the configuration. It then asks whether to delete the original; only typing `yes` authorizes deletion.
Declining or interrupting this confirmation leaves the original in place. Existing encrypted keys are never overwritten.

To encrypt the key referenced by an existing mint configuration:

```powershell
gh mint setup --encrypt-key
```

This preserves your App ID, remote, executable path, and other configuration fields, updating only `key_file`.
It does nothing if the configured key is already encrypted. On failure before configuration is saved, the original
key and configuration are retained.

Each encrypted-key launch prompts for the passphrase again. Use a long, unique passphrase; mint does not accept
passphrases in arguments or environment variables, cache them, or send them to Copilot. Decryption happens in memory,
never through a decrypted temporary file. Redirected prompt input for Copilot is not used for passphrases: mint uses
the controlling terminal instead. A terminal is required to unlock an encrypted key.

Encryption uses PBES2 with AES-256-CBC and PBKDF2-HMAC-SHA256 (600,000 iterations and a random 16-byte salt).
The result is a standard encrypted PKCS#8 PEM, compatible with OpenSSL. Losing the passphrase means generating a new
GitHub App key. Deleting the source is ordinary file deletion, not guaranteed erasure from SSDs, backups, or synced copies.

### How permissions work

**The App installation is the source of truth.** Every token request omits `permissions`, so GitHub grants the
token all permissions granted to that installation. There is no `--permissions` option or read-only default.
To change session authority, change the App's permissions in GitHub and approve installation updates when required.
Restart `gh mint` afterward to mint a new token with the updated grants.

Repository selection is independent of permission levels: even a write-enabled token remains restricted to the
current repository. Operations made with this token act as the App, not your personal account.
Installation-wide non-repository privileges are inherited too; avoid granting them unless you need them.

Configurations created by older versions may contain a `permissions` field. It is ignored and can be removed;
you do not need to rerun setup.

Optional setup flags:

- `--remote upstream` selects a different remote; the default is `origin`.
- `--copilot "C:\trusted\copilot.exe"` pins a trusted executable instead of resolving it from `PATH` during setup.

Configuration contains the App ID, canonical encrypted-key and executable paths, and remote name.
It contains no token, passphrase, or private key material.

Legacy `settings` and `sandbox` fields in mint's configuration are ignored. Configure Copilot directly through
your normal Copilot settings or forwarded command-line arguments.

Configuration is stored in the OS user configuration directory:

- Windows: `%APPDATA%\gh-mint\config.json`
- Linux: `$XDG_CONFIG_HOME/gh-mint/config.json`, or `~/.config/gh-mint/config.json`
- macOS: `~/Library/Application Support/gh-mint/config.json`

Setup pins the resolved executable path. If an upgrade moves the actual Copilot binary, update the configured path.

## Copilot authentication

The installation token cannot authenticate to Copilot itself.

`gh-mint` uses `COPILOT_GITHUB_TOKEN` when set. Otherwise it captures `gh auth token --hostname github.com` in memory
for Copilot authentication. That credential must support Copilot requests. An explicit Copilot credential is needed
because the repository-scoped App token cannot authenticate to Copilot and takes precedence over saved login credentials.

Copilot inherits your environment, with these overrides:

- `COPILOT_GITHUB_TOKEN`: the separate Copilot credential.
- `GH_TOKEN`: the one-repository installation token.
- `GH_REPO` and `GH_HOST`: the pinned repository and `github.com`.

Your normal Copilot home and settings are used, including an inherited `COPILOT_HOME`. Mint does not create or modify
Copilot settings, filter other environment variables, or change Git/GitHub CLI configuration.

The launcher uses your account credential for Copilot authentication, never to mint the installation token.
Other tools, MCP servers, hooks, and Git credential helpers can use whatever credentials your normal configuration
makes available. Mint scopes the token it issues, not every possible authenticated operation in the session.

## Launch

Run inside a Git working tree:

```powershell
gh mint
gh mint --model YOUR_MODEL
gh mint -p "Explain this repository" -s
```

Except for the `setup` command, all arguments after `gh mint` are passed to Copilot unchanged, including `--help`,
`--version`, and `--`. They are passed without a shell. Copilot starts at the checkout root by default, including
when you invoke the extension from a subdirectory. A non-default or ambiguous remote is never silently selected.

Mint requests Copilot's sandbox, asks it to exclude `COPILOT_GITHUB_TOKEN` and `GITHUB_TOKEN` from shell and MCP
environments, and disables built-in MCP servers. It also adds `--deny-tool=read(...)` and `--deny-tool=write(...)`
rules for the configured App key directory and its descendants.
These rules apply to built-in file tools, not shell commands, hooks, MCP servers, or other processes.
Reading an encrypted PEM alone is insufficient to mint tokens without its passphrase, but unencrypted source copies
you retain are not protected by these rules. Unencrypted keys from older configurations still work without a prompt;
use `gh mint setup --encrypt-key` to migrate them.
Access to a decrypted key can allow minting broader tokens, so these guardrails are not sufficient for an untrusted agent.
Use a separate OS identity or signing broker if the key must be inaccessible.

Add the following to your normal Copilot `settings.json` to use the scoped token for sandboxed `gh` commands
instead of automatic account authentication:

```json
{
  "sandbox": {
    "allowBypass": false,
    "auth": { "gh": false, "git": false },
    "credentials": {
      "envVars": {
        "GH_TOKEN": {
          "injectHosts": ["github.com", "api.github.com", "uploads.github.com"]
        }
      }
    }
  }
}
```

Merge these fields with existing settings. Mint does not write or enforce this policy; project settings and other
configuration can affect the effective sandbox. Hooks, plugins, extensions, custom MCP servers, Git credential
helpers, and other accessible credentials remain separate authentication paths. Remote MCP servers are not sandboxed.
These settings and flags do not make mint an isolation boundary.

### Lifetime and cleanup

GitHub installation tokens expire after one hour. There is no transparent refresh. Restart `gh mint` when needed.
Changing directories, remotes, or the prompt does not expand the already-minted token's access.

The launcher attempts to revoke the token on normal exit, child failure, and handled interruption. A hard kill or
failed revocation leaves the token valid until its expiry. Session history and certificate state are managed by
Copilot in your normal Copilot home. Old mint-specific Copilot homes are no longer used and are not deleted automatically.

Git operations do not automatically use `GH_TOKEN`; they use your existing Git configuration and credential helpers.
GitHub CLI uses the scoped `GH_TOKEN` for GitHub.com.

### Optional PowerShell profile wrapper

Add the contents of [examples/profile.ps1](examples/profile.ps1) to your profile:

```powershell
function copilot {
    $gh = Get-Command gh -CommandType Application -ErrorAction Stop | Select-Object -First 1
    & ($gh.Source) mint @args
}
```

This works in Windows PowerShell 5.1 and PowerShell 7. A loaded function takes precedence over executables on `PATH`,
but not over aliases. A full executable path or a shell without your profile bypasses it. Nothing is installed into
your profile automatically.

## Existing tools investigated

The following projects cover parts of this workflow. Findings refer to their implementations inspected during
development, not a promise about future releases.

- [dayflower/ghatoken](https://github.com/dayflower/ghatoken): its `--restrict-scope-repo` option implements genuine
  single-repository minting. It does not configure or launch Copilot's sandbox.
- [AmadeusITGroup/gh-app-auth](https://github.com/AmadeusITGroup/gh-app-auth): implements routing and child-process
  execution, but its inspected token request uses an empty body and is installation-wide.
- [Link-/gh-token](https://github.com/Link-/gh-token): supports signing, token issuance, and revocation, but the inspected
  generation command does not narrow installation tokens to a repository.
- [coding-hermes/gh-app-auth](https://github.com/coding-hermes/gh-app-auth): Python scripts implement App registration
  and scoped issuance for a fleet, not a local Copilot launcher.
- [jupyterhub/gh-scoped-creds](https://github.com/jupyterhub/gh-scoped-creds): a JupyterHub/HPC user-authorization
  workflow, not this installation-token launcher.

No complete matching extension was found in the repository searches performed. The useful minting primitive is
GitHub's existing API, not a new token mechanism.

## Development

Use Go 1.27.4 or later. Install the pinned analysis tools:

```powershell
go install golang.org/x/tools/go/analysis/passes/modernize/cmd/modernize@v0.52.0 golang.org/x/tools/go/analysis/passes/fieldalignment/cmd/fieldalignment@v0.52.0
```

Run the checks:

```powershell
pwsh -NoProfile -File .\.github\scripts\test-release-version.ps1
modernize ./...
fieldalignment ./...
go test ./...
go vet ./...
golangci-lint run
go build -trimpath -o gh-mint.exe .
```

Tests use local HTTP servers, ephemeral test keys, and a real helper subprocess. They verify the repository and
permission inheritance, JWT signatures, encrypted-key setup and decryption, redirect refusal, argument/environment
forwarding, PEM-directory guardrails, revocation,
and exit-status propagation without live GitHub credentials.

CI checks Windows, Linux, and macOS. Every push to protected `main` automatically builds a release using the same
platform binaries and asset names as
[cli/gh-extension-precompile](https://github.com/cli/gh-extension-precompile). Windows executables are Authenticode
signed with Azure Artifact Signing using the same account and certificate profile as oh-my-posh. Signatures must
be valid and timestamped before publication. Other platforms are not Authenticode signed. GitHub build provenance
attestations cover the final release binaries, including the signed Windows files.

Before merging a release workflow change, configure the `AZURE_TENANT_ID`, `AZURE_CLIENT_ID`, and `AZURE_CLIENT_SECRET`
secrets in the protected GitHub `release` environment using the existing oh-my-posh signing identity. Remove any
repository-level copies: otherwise a different workflow could bypass the environment's approval gate.
That identity needs the Artifact Signing Certificate Profile Signer role for the `oh-my-posh` profile in the `oh-my-posh` account at
`https://weu.codesigning.azure.net`. Missing credentials or failed signing abort the release; unsigned Windows
executables are never published as a fallback.

Keep the `release` environment restricted to the `main` branch so workflows running from other branches or tags
cannot access signing credentials. Main's required pull request checks are the release gate; the release workflow
does not rerun tests. Conventional commits determine the next version: breaking changes increase the major version,
features increase the minor version, and other changes increase the patch version.
Version calculation uses the repository's PowerShell script, not a third-party action. It selects the highest
reachable stable `vMAJOR.MINOR.PATCH` tag, ignores prerelease and noncanonical tags, and reuses a tag already on the
current commit. A repository without a stable tag starts at `v0.1.0`. Complete Git history is required.

Versioning and build/sign jobs have only `contents: read`, and checkouts never retain Git credentials. A separate
publication job receives signed binaries by immutable artifact ID; it alone has repository-write, OIDC, and attestation
permissions, and it has no Azure credentials or source checkout. Release tags are created for the exact merged commit.
Release tags are created automatically; only administrators may update or delete them. Tag pushes do not trigger
release jobs. Repository administrators remain trusted to manage these controls.

Workflow actions are pinned to commit SHAs. The extension naming and release layout follow
[GitHub's extension authoring guide](https://docs.github.com/en/github-cli/github-cli/creating-github-cli-extensions).

Use [Conventional Commits](https://www.conventionalcommits.org/) for commit messages, for example
`feat: add a launcher option` or `fix(config): validate the remote`. CI checks every commit in pull requests
and pushes to `main`, not pull request titles.

## License

MIT.
