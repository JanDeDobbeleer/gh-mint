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

- The Copilot host process runs outside its local OS command sandbox. Built-in file tools enforce file policy on a
  best-effort basis. A private key on the same host is not cryptographically inaccessible to Copilot.
- This does not isolate against other processes running as your OS user, administrators, a compromised Copilot
  binary, or a compromised GitHub CLI binary.
- User-editable sandbox settings are not managed enforcement. This extension requests a sandbox policy, but passes
  Copilot arguments unchanged. Arguments can disable or weaken sandboxing, secret filtering, and other protections.
  Settings can also change during a session. Mandatory sandboxing requires an administrator-controlled Copilot
  managed policy.
- Project configuration is checked at startup. A concurrently modified checkout or settings changed during a
  session are outside that check. The minted token remains scoped, but alternate credentials must remain absent.
- Local sandbox proxy and network controls on Windows rely on applications honoring proxy settings. They do not
  provide a complete egress boundary.
- A scoped credential does not prohibit public unauthenticated reads, or all account/installation metadata
  endpoints. It limits the repository authority GitHub grants to that token.
- Secrets already present in the checkout, unrecognized credential files outside it, copied keys, system credential
  helpers, and privileged policy hooks require separate review. The common-path denial list is not exhaustive.
- The separate Copilot credential exists in the unsandboxed host's environment. By default, secret filtering covers
  shell and MCP environments; optional extensions, plugins, and project language servers are disabled or rejected.
  Forwarded arguments can change these protections.
- The private key is stored with passphrase encryption, but encryption does not protect it from keylogging, memory
  inspection, or a compromised same-user process while unlocked. Legacy unencrypted keys remain supported and must
  be migrated explicitly with `gh mint setup --encrypt-key`. Retained source copies, backups, synced files, and
  recoverable deleted data can still expose unencrypted key material; ordinary deletion is not secure erasure.
- The child receives the scoped `GH_TOKEN` and separate `COPILOT_GITHUB_TOKEN`. The environment allowlist drops
  inherited GitHub tokens and other unapproved variables, but forwarded arguments can change Copilot's filtering.
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
