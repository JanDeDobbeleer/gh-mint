# Security

## Intended boundary

`gh-mint` limits authenticated repository operations by asking GitHub to issue an installation token for exactly one
repository, inheriting all permissions granted to that App installation. It never exchanges your user token for a narrower token.
The App's private key signs the request; your Copilot credential is separate.

The App installation is the permission policy. The launcher does not limit tokens to read-only access or exclude
organization/account permissions granted to the installation. Grant the App only the authority your sessions need.
Single-repository scoping does not narrow installation-wide non-repository privileges.

The launcher does not print or persist installation tokens, App JWTs, or private key material. API errors do not
include response bodies. The minting client refuses redirects, uses TLS verification, and has bounded responses and
timeouts. No API-host override is exposed.

The remote is untrusted input. Only supported GitHub.com URL forms are accepted, and API destinations are fixed.
Do not invoke setup from an untrusted checkout or accept an untrusted Copilot executable on `PATH`.

## Limits

- The Copilot host process runs outside its local OS command sandbox. Built-in file tools enforce file policy on a
  best-effort basis. A private key on the same host is not cryptographically inaccessible to Copilot.
- This does not isolate against other processes running as your OS user, administrators, a compromised Copilot
  binary, or a compromised GitHub CLI binary.
- User-editable sandbox settings are not managed enforcement. This extension starts with the requested policy and
  rejects unsafe launch flags; it cannot promise that a user cannot disable or edit policy during the session.
  Mandatory sandboxing requires an administrator-controlled Copilot managed policy.
- Project configuration is checked at startup. A concurrently modified checkout or settings changed during a
  session are outside that check. The minted token remains scoped, but alternate credentials must remain absent.
- Local sandbox proxy and network controls on Windows rely on applications honoring proxy settings. They do not
  provide a complete egress boundary.
- A scoped credential does not prohibit public unauthenticated reads, or all account/installation metadata
  endpoints. It limits the repository authority GitHub grants to that token.
- Secrets already present in the checkout, unrecognized credential files outside it, copied keys, system credential
  helpers, and privileged policy hooks require separate review. The common-path denial list is not exhaustive.
- The separate Copilot credential exists in the unsandboxed host's environment. Secret filtering covers shell and
  MCP environments; optional extensions, plugins, and project language servers are disabled or rejected rather
  than assumed to have identical filtering.
- File modes are not a substitute for NTFS ACLs on Windows. The App key is read from your chosen file, not moved into
  an OS keyring. The launcher configuration contains paths but must still be protected against modification.
- Hard termination, system failure, or network failure can prevent cleanup and revocation. GitHub's token expiry
  remains the final lifetime limit.

For a stronger signing-key boundary, use a broker running under a separate OS identity or machine. It must bind
issuance and renewal to an approved repository and permission set. A broker that signs arbitrary requests from the
agent does not solve the problem. This release does not implement such a broker.

## Reporting

Do not publish tokens, PEM files, or credential-bearing logs in issues. Report a suspected vulnerability privately
through the repository's GitHub security advisory channel when available. Before publication, contact the repository
owner privately.
