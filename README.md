<p align="center">
  <img src="assets/mint-logo.svg" alt="Mint" width="200">
</p>

# mint

Launch GitHub Copilot CLI in its local sandbox with a short-lived GitHub App token scoped to the current repository.

```powershell
mint
```

Sandboxed `gh` commands use the App token. Copilot keeps your existing home and settings, and mint does not replace
your normal GitHub CLI login.

## Requirements

- GitHub CLI and Git.
- GitHub Copilot CLI 1.0.112 or later, with a supported local sandbox host.
- A GitHub App installed on the repositories you intend to use.
- A separate credential authorized to use Copilot.

Only GitHub.com is supported.

## Install

On Windows, install the signed MSIX package from the latest
[GitHub release](https://github.com/JanDeDobbeleer/gh-mint/releases):

```powershell
Add-AppxPackage .\mint-windows-amd64.msix
mint setup --help
```

The package installs the `mint.exe` command alias. Choose the package matching your processor architecture.
On macOS, Linux, and FreeBSD, download the matching standalone binary from the same release and place it on `PATH`.
No GitHub CLI extension is installed.

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
   outside your Git checkouts, such as `C:\secure\mint\app.pem`. Remove any leftover copy from Downloads. Never
   commit the key or share its contents.
6. Restrict that directory with your operating system's access controls. On Windows, Go's `0600` file mode does not
   replace NTFS ACLs. Setup imports the key into mint's key directory with passphrase encryption and offers to
   delete the original after verification.
7. Select Install App, choose the target account, then Only select repositories. Select the repositories where you
   will use the launcher. Each session still receives access to just the repository it starts in.

GitHub documents [App registration][app-registration], [private keys][app-keys], and
[installing your App][app-installation] in more detail.

[app-registration]: https://docs.github.com/en/apps/creating-github-apps/registering-a-github-app/registering-a-github-app
[app-keys]: https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/managing-private-keys-for-github-apps
[app-installation]: https://docs.github.com/en/apps/using-github-apps/installing-your-own-github-app

### Configure the launcher

Configure mint once:

```powershell
mint setup --app-id 123456 --key-file "C:\secure\mint\app.pem"
```

Setup applies the [credential policy](#copilot-authentication) to your normal Copilot home.
Run `mint setup` with no options to reapply it. This updates only Mint's required settings; tool and cache grants stay unchanged.
Initial setup never overwrites an existing configuration. Review and edit the existing configuration to change its values,
or use the explicit key-encryption migration below.

Setup asks for a new passphrase twice using hidden terminal prompts, writes an encrypted PKCS#8 key under
`keys/app.encrypted.pem` beside mint's configuration, and verifies that it decrypts to the original key before
saving the configuration. It then asks whether to delete the original; only typing `yes` authorizes deletion.
Declining or interrupting this confirmation leaves the original in place. Existing encrypted keys are never overwritten.

To encrypt the key referenced by an existing mint configuration:

```powershell
mint setup --encrypt-key
```

This preserves your App ID, remote, executable path, and other configuration fields, updating only `key_file`.
It does nothing if the configured key is already encrypted. On failure before configuration is saved, the original
key and configuration are retained.

Each encrypted-key launch prompts for the passphrase again. Use a long, unique passphrase; mint does not accept
passphrases in arguments or environment variables, cache them, or send them to Copilot. Decryption happens in memory,
never through a decrypted temporary file. Redirected prompt input for Copilot is not used for passphrases: mint uses
the controlling terminal instead. A terminal is required to unlock an encrypted key.

### How permissions work

**The App installation is the source of truth.** Every token request omits `permissions`, so GitHub grants the
token all permissions granted to that installation. There is no `--permissions` option or read-only default.
To change session authority, change the App's permissions in GitHub and approve installation updates when required.
Restart `mint` afterward to mint a new token with the updated grants.

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

Legacy `settings` and `sandbox` fields in mint's configuration are ignored. mint uses your normal Copilot home
(`COPILOT_HOME` if set, otherwise Copilot's default). Normal launches do not edit its settings.

Configuration stays in the existing OS user configuration directory so current installations keep using their
encrypted key without migration:

- Windows: `%APPDATA%\gh-mint\config.json`
- Linux: `$XDG_CONFIG_HOME/gh-mint/config.json`, or `~/.config/gh-mint/config.json`
- macOS: `~/Library/Application Support/gh-mint/config.json`

Setup pins the resolved executable path. If an upgrade moves the actual Copilot binary, update the configured path.

## Copilot authentication

mint uses `COPILOT_GITHUB_TOKEN` for Copilot itself, falling back to `gh auth token --hostname github.com`.
Sandboxed `gh` commands use the separate App token supplied as `GH_TOKEN`. Mint disables automatic Git authentication
and supplies a separately masked HTTPS credential in `MINT_GIT_AUTH`. Mint puts `mint-git.instructions.md` in a temporary
session directory and adds it to `COPILOT_CUSTOM_INSTRUCTIONS_DIRS`. The instructions show Copilot how to use
`git --config-env` against the scoped repository and how to open a PR after pushing. The file contains no secrets.
This guides the agent; it does not change how plain `git push` works or rewrite Git remotes.

`mint setup` applies the policy below to your existing Copilot home, or you can merge it into `settings.json` yourself.
It applies to sandboxed Copilot sessions using that home. Setup leaves tool grants unchanged and does not enable
sandboxing for ordinary Copilot launches. Mint checks the policy before minting a token.

```json
{
  "sandbox": {
    "allowBypass": false,
    "auth": { "gh": false, "git": false },
    "credentials": {
      "envVars": {
        "GH_TOKEN": {
          "injectHosts": ["github.com", "api.github.com", "uploads.github.com"]
        },
        "MINT_GIT_AUTH": {
          "injectHosts": ["github.com"]
        }
      }
    }
  }
}
```

On Windows, `mint setup` enables **Allow local network** when needed for the credential proxy. If configuring
manually, enable it in `/sandbox config`.

### Allow tools, caches, and dependency sources

Use `/sandbox config` or the `sandbox.userPolicy` section of your Copilot `settings.json`. With automatic
developer-tool access turned off, grant runtime directories read-only and build or download caches read/write.
For Go, find the actual locations with `go env GOROOT GOCACHE GOMODCACHE`. For PowerShell 7 on Windows, put the
standalone install ahead of the Store alias on `PATH` before starting mint.

For example, merge these Windows path grants into your existing settings and adjust the paths as needed:

```json
{
  "sandbox": {
    "allowDevToolAccess": false,
    "userPolicy": {
      "filesystem": {
        "readonlyPaths": ["C:\\Program Files\\Go", "C:\\Program Files\\PowerShell\\7"],
        "readwritePaths": ["$LOCALAPPDATA\\go-build", "$USERPROFILE\\go\\pkg\\mod"]
      }
    }
  }
}
```

Grant Git and GitHub CLI's install directories too if you use them. If you use `network.allowedHosts`, include
the GitHub and package download hosts you need there, not in `GH_TOKEN.injectHosts`. Use `/sandbox policy go test`
or `/sandbox policy pwsh` to check the resulting grants.

## Launch

Run inside a Git working tree:

```powershell
mint
mint --model YOUR_MODEL
mint -p "Explain this repository" -s
```

Except for the `setup` and `config` commands, arguments after `mint` are passed to Copilot unchanged, including `--help`,
`--version`, and `--`. mint rejects flags that disable sandboxing or override secret filtering. Arguments are passed
without a shell. Copilot starts at the checkout root by default, including
when you invoke mint from a subdirectory. A non-default or ambiguous remote is never silently selected.

### Lifetime and cleanup

GitHub installation tokens expire after one hour. There is no transparent refresh. Restart `mint` when needed.
Changing directories, remotes, or the prompt does not expand the already-minted token's access.

The launcher attempts to revoke the token on normal exit, child failure, and handled interruption. A hard kill or
failed revocation leaves the token valid until its expiry. Temporary Git and GitHub CLI configuration is removed on
handled exit. Copilot keeps session history in your normal home; launch through mint again when resuming to obtain
a fresh scoped token.

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
go build -trimpath -o mint.exe .
```

Tests use local HTTP servers, ephemeral test keys, and a real helper subprocess. They verify the repository and
permission inheritance, JWT signatures, encrypted-key setup and decryption, redirect refusal, argument/environment
forwarding, PEM-directory guardrails, revocation,
and exit-status propagation without live GitHub credentials.

CI checks Windows, Linux, and macOS. Every push to protected `main` automatically builds standalone macOS, Linux, and FreeBSD
binaries and architecture-specific Windows MSIX packages. Windows executables and MSIX packages are Authenticode
signed with Azure Artifact Signing using the existing release signing setup. Signatures must be valid and timestamped
before publication. Other platform binaries are not Authenticode signed. GitHub build provenance attestations cover
the final release assets.

Before merging a release workflow change, configure the `AZURE_TENANT_ID`, `AZURE_CLIENT_ID`, and `AZURE_CLIENT_SECRET`
secrets in the protected GitHub `release` environment using the existing oh-my-posh signing identity. Remove any
repository-level copies: otherwise a different workflow could bypass the environment's approval gate.
That identity needs the Artifact Signing Certificate Profile Signer role for the configured profile in the signing account at
`https://weu.codesigning.azure.net`. Missing credentials or failed signing abort the release; unsigned Windows
executables or MSIX packages are never published as a fallback. The MSIX publisher in its manifest is
`CN=Jan Joris De Dobbeleer, O=Jan Joris De Dobbeleer, L=Diest, C=BE`.

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

Workflow actions are pinned to commit SHAs.

Use [Conventional Commits](https://www.conventionalcommits.org/) for commit messages, for example
`feat: add a launcher option` or `fix(config): validate the remote`. CI checks every commit in pull requests
and pushes to `main`, not pull request titles.

## License

MIT.
