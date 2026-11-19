# Security

Mint starts Copilot in its local sandbox and supplies a short-lived GitHub App installation token scoped to the current
repository. The token inherits the App installation's permissions. Copilot authentication uses a separate credential.

Setup stores the App key as an encrypted PKCS#8 PEM. Mint unlocks it in memory to request the token and attempts to
revoke the token when the session ends.

## Reporting

Report suspected vulnerabilities privately through the repository's GitHub security advisories. Do not include
tokens, private keys, or credential-bearing logs.
