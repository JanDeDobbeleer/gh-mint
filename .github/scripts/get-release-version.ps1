param(
    [string]$Repository = (Join-Path $PSScriptRoot '..\..')
)

$ErrorActionPreference = 'Stop'
$Repository = (Resolve-Path -LiteralPath $Repository).Path

function Invoke-Git {
    param([string[]]$Arguments)

    $output = & git --no-pager -C $Repository @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "Cannot read Git history: git $($Arguments[0]) failed."
    }
    return $output
}

$head = Invoke-Git -Arguments @('rev-parse', '--verify', 'HEAD')
if ((Invoke-Git -Arguments @('rev-parse', '--is-shallow-repository')) -ne 'false') {
    throw 'Complete Git history is required to calculate a release version; use fetch-depth: 0.'
}

$latest = Invoke-Git -Arguments @('tag', '--list', 'v*', '--merged', 'HEAD', '--sort=-version:refname') |
    Where-Object { $_ -cmatch '\Av(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\z' } |
    Select-Object -First 1
if ($null -eq $latest) {
    return 'v0.1.0'
}
if ((Invoke-Git -Arguments @('rev-parse', "${latest}^{commit}")) -eq $head) {
    return $latest
}

$parts = $latest.Substring(1).Split('.')
$major = [bigint]$parts[0]
$minor = [bigint]$parts[1]
$patch = [bigint]$parts[2]
$breaking = $false
$feature = $false
$messages = (Invoke-Git -Arguments @('log', "${latest}..HEAD", '--format=%B%x00')) -join "`n"
foreach ($message in $messages.Split([char]0)) {
    $subject = ($message.Trim() -split '\r?\n', 2)[0]
    if ($subject -cmatch '^[a-z]+(?:\([^)]+\))?!:[ \t]+\S' -or
        $message -cmatch '(?m)^BREAKING(?: CHANGE|-CHANGE):[ \t]+\S') {
        $breaking = $true
        break
    }
    if ($subject -cmatch '^feat(?:\([^)]+\))?:[ \t]+\S') {
        $feature = $true
    }
}

if ($breaking) {
    $major += [bigint]::One
    $minor = 0
    $patch = 0
} elseif ($feature) {
    $minor += [bigint]::One
    $patch = 0
} else {
    $patch += [bigint]::One
}

return 'v{0}.{1}.{2}' -f $major, $minor, $patch
