# Security

## Intended boundary

`gh-mint` asks GitHub to issue an App installation token for exactly one repository, inheriting all permissions
granted to that installation. It never exchanges your user token for a narrower token. The App private key signs
the request; your Copilot credential is separate.

The App installation is the permission policy. The launcher does not limit tokens to read-only access or exclude
organization/account permissions granted to the installation. Grant the App only the authority your sessions need.
Single-repository scoping does not narrow installation-wide non-repository privileges.

The launcher does not print or persist installation tokens, App JWTs, passphrases, or decrypted private key material.
Setup stores a passphrase-encrypted PKCS#8 key using PBES2, AES-256-CBC, and PBKDF2-HMAC-SHA256 with 600,000 iterations
and a random 16-byte salt. It verifies the saved key before saving configuration. Source deletion requires explicit
confirmation after configuration succeeds. Passphrases are entered through hidden terminal prompts, never through
arguments or environment variables. Mint clears its passphrase buffers and never forwards them to Copilot.
Decrypted keys remain in process memory; Go and third-party libraries may retain copies until reclaimed.

API errors do not include response bodies. The minting client refuses redirects, uses TLS verification, and has bounded responses and
timeouts. No API-host override is exposed.

The remote is untrusted input. Only supported GitHub.com URL forms are accepted, and API destinations are fixed.
Do not invoke setup from an untrusted checkout or accept an untrusted Copilot executable on `PATH`.

## Limits

- Mint is a token wrapper, not an isolation boundary. It requests Copilot's sandbox, asks Copilot to exclude
  `COPILOT_GITHUB_TOKEN` and `GITHUB_TOKEN` from shell and MCP environments, and disables built-in MCP servers.
  Copilot otherwise uses your normal home, settings, environment, hooks, plugins, extensions, and custom MCP servers.
  Configure sandbox credential masking as described in the README; mint does not enforce that policy.
- Mint adds built-in file-tool read/write deny rules for the App key directory and its descendants. Shell commands,
  hooks, MCP servers, and other processes running as your OS user can still read the PEM. Encrypted PEMs require
  their passphrase, but access to an unencrypted/decrypted key can allow minting tokens for other repositories
  installed on the App. These guardrails do not isolate the signing key.
- Use a strong, unique passphrase. Encryption protects the stored key, not against keylogging, memory inspection,
  or a compromised same-user process while it is unlocked. Legacy unencrypted keys remain supported and must be
  migrated explicitly with `gh mint setup --encrypt-key`. Retained source copies, backups, synced files, and
  recoverable deleted data can still expose the unencrypted key. Ordinary deletion is not secure erasure.
- The child receives the real scoped `GH_TOKEN` and a separate `COPILOT_GITHUB_TOKEN`. Other inherited credentials
  and normal Git credential helpers remain available. The issued token is scoped; not all operations necessarily
  use it. Copilot settings and forwarded arguments control secret filtering and any sandbox credential masking.
- File modes are not a substitute for NTFS ACLs on Windows. The App key is read from your chosen file, not moved into
  an OS keyring. Protect both the key and launcher configuration against modification.
- The launcher attempts to revoke the token on normal exit, child failure, and handled interruption. A hard kill,
  system failure, or network failure can prevent revocation. GitHub's token expiry remains the final lifetime limit.
- Changing directories, remotes, or prompts does not expand the issued token's scope. A scoped credential does not
  prohibit public unauthenticated reads or all account/installation metadata endpoints.

For a stronger signing-key boundary, use a broker running under a separate OS identity or machine. It must bind
issuance and renewal to an approved repository and permission set. A broker that signs arbitrary requests from the
agent does not solve the problem. This release does not implement such a broker.

## Reporting

Do not publish tokens, PEM files, or credential-bearing logs in issues. Report a suspected vulnerability privately
through the repository's GitHub security advisory channel when available. Before publication, contact the repository
owner privately.
