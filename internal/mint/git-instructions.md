# Git authentication in this Mint session

Use the repository in `GH_REPO` for authenticated Git operations. Keep the sandbox
enabled. Do not use a personal credential, SSH, or a credential helper. Do not
print credentials, add them to Git config files, or enable HTTP tracing.

For a push, use this pattern in PowerShell:

```powershell
if (-not $env:MINT_GIT_AUTH) { throw 'Mint Git credential is missing' }
$url = "https://github.com/${env:GH_REPO}.git"
$env:MINT_GIT_HEADER = "Authorization: Basic $env:MINT_GIT_AUTH"
try {
    git "--config-env=http.$url.extraHeader=MINT_GIT_HEADER" push $url "HEAD:refs/heads/$branch"
} finally {
    $env:MINT_GIT_HEADER = $null
}
```

In a POSIX shell:

```sh
(
    : "${MINT_GIT_AUTH:?Mint Git credential is missing}"
    url="https://github.com/${GH_REPO}.git"
    export MINT_GIT_HEADER="Authorization: Basic $MINT_GIT_AUTH"
    git "--config-env=http.$url.extraHeader=MINT_GIT_HEADER" push "$url" "HEAD:refs/heads/$branch"
)
```

Set `branch` to the intended remote branch before running the example. Use the same
header and explicit HTTPS URL for authenticated fetches or ref lookups. Leave
ordinary local Git commands unchanged. If a push fails, report it without falling
back to another credential. After a successful push, use `gh pr create --head`
with that branch so `gh` does not attempt another push.
