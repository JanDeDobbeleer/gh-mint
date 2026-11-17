$ErrorActionPreference = 'Stop'
$versionScript = Join-Path $PSScriptRoot 'get-release-version.ps1'
$root = Join-Path ([System.IO.Path]::GetTempPath()) "gh-mint-version-$([guid]::NewGuid().ToString('N'))"
$null = New-Item -ItemType Directory -Path $root
$gitEnvironment = @{}
$passed = 0

function Invoke-TestGit {
    param([string]$Repository, [string[]]$Arguments)

    $output = & git --no-pager -C $Repository -c core.hooksPath= -c commit.gpgSign=false `
        -c tag.gpgSign=false -c user.name=gh-mint-tests -c user.email=tests@example.invalid @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "Git fixture setup failed: $($Arguments[0])."
    }
    return $output
}

function Assert-Version {
    param([string]$Repository, [string]$Expected, [string]$Name)

    $tagsBefore = (Invoke-TestGit $Repository @('tag', '--list')) -join "`n"
    $headBefore = Invoke-TestGit $Repository @('rev-parse', 'HEAD')
    $actual = @(& $versionScript -Repository $Repository)
    if ($actual.Count -ne 1 -or $actual[0] -cne $Expected) {
        throw "${Name}: expected $Expected, got $($actual -join ', ')."
    }
    if ((Invoke-TestGit $Repository @('rev-parse', 'HEAD')) -ne $headBefore -or
        ((Invoke-TestGit $Repository @('tag', '--list')) -join "`n") -cne $tagsBefore) {
        throw "${Name}: version calculation changed Git history or tags."
    }
    Write-Output "PASS $Name"
    $script:passed++
}

try {
    foreach ($entry in @(Get-ChildItem Env: | Where-Object { $_.Name -like 'GIT_*' })) {
        $gitEnvironment[$entry.Name] = $entry.Value
        Remove-Item -LiteralPath "Env:$($entry.Name)"
    }
    $cases = @(
        @{Name = 'first release'; Expected = 'v0.1.0'}
        @{Name = 'patch'; Base = 'v1.2.3'; Commits = @('fix: handle errors'); Expected = 'v1.2.4'}
        @{Name = 'feature'; Base = 'v1.2.3'; Commits = @('feat: add an option'); Expected = 'v1.3.0'}
        @{Name = 'scoped feature'; Base = 'v1.2.3'; Commits = @('feat(cli): add an option'); Expected = 'v1.3.0'}
        @{Name = 'scoped breaking header'; Base = 'v0.2.3'; Commits = @('fix(cli)!: replace an option'); Expected = 'v1.0.0'}
        @{Name = 'breaking footer'; Base = 'v1.2.3'; Commits = @("refactor: change config`n`nBREAKING CHANGE: old configs are unsupported"); Expected = 'v2.0.0'}
        @{Name = 'hyphenated breaking footer'; Base = 'v1.2.3'; Commits = @("fix: change config`n`nBREAKING-CHANGE: old configs are unsupported"); Expected = 'v2.0.0'}
        @{Name = 'highest bump wins'; Base = 'v1.2.3'; Commits = @('refactor!: replace config', 'feat: add an option', 'fix: handle errors'); Expected = 'v2.0.0'}
        @{Name = 'body examples are not subjects'; Base = 'v1.2.3'; Commits = @("docs: explain commits`n`nfeat: this is an example, not a feature"); Expected = 'v1.2.4'}
        @{Name = 'stable numeric tag selection'; Base = 'v2.10.0'; Tags = @('v2.9.9', 'v99.0.0-rc.1', 'v99.0.0+build', 'v01.0.0', 'V99.0.0', 'unrelated'); Commits = @('ci: update workflow'); Expected = 'v2.10.1'}
        @{Name = 'unmerged tag ignored'; Base = 'v1.2.3'; UnmergedTag = $true; Commits = @('fix: handle errors'); Expected = 'v1.2.4'}
        @{Name = 'lightweight tagged rerun'; Base = 'v1.2.3'; Expected = 'v1.2.3'}
        @{Name = 'annotated tagged rerun'; Base = 'v1.2.3'; Annotated = $true; Expected = 'v1.2.3'}
    )
    $index = 0
    foreach ($case in $cases) {
        $repository = Join-Path $root "case-$index"
        $index++
        $null = New-Item -ItemType Directory -Path $repository
        $null = Invoke-TestGit $repository @('init', '--quiet', '--initial-branch=main')
        $null = Invoke-TestGit $repository @('commit', '--quiet', '--allow-empty', '-m', 'chore: initial')
        if ($case.Base) {
            $arguments = @('tag', $case.Base)
            if ($case.Annotated) {
                $arguments += @('--annotate', '--message', $case.Base)
            }
            $null = Invoke-TestGit $repository $arguments
        }
        foreach ($tag in $case.Tags) {
            $null = Invoke-TestGit $repository @('tag', $tag)
        }
        if ($case.UnmergedTag) {
            $null = Invoke-TestGit $repository @('checkout', '--quiet', '-b', 'unmerged')
            $null = Invoke-TestGit $repository @('commit', '--quiet', '--allow-empty', '-m', 'feat: unmerged')
            $null = Invoke-TestGit $repository @('tag', 'v99.0.0')
            $null = Invoke-TestGit $repository @('checkout', '--quiet', 'main')
        }
        foreach ($message in $case.Commits) {
            $null = Invoke-TestGit $repository @('commit', '--quiet', '--allow-empty', '--cleanup=verbatim', '-m', $message)
        }
        Assert-Version $repository $case.Expected $case.Name
    }

    $shallow = Join-Path $root 'shallow'
    $null = Invoke-TestGit $root @('clone', '--quiet', '--depth=1', '--no-local', $repository, $shallow)
    $rejected = $false
    try {
        $null = & $versionScript -Repository $shallow
    } catch {
        if ($_.Exception.Message -notlike '*Complete Git history is required*') {
            throw
        }
        $rejected = $true
    }
    if (-not $rejected) {
        throw 'Shallow history must be rejected.'
    }
    $passed++
    Write-Output 'PASS shallow history rejected'
    Write-Output "$passed release-version tests passed."
}
finally {
    foreach ($name in $gitEnvironment.Keys) {
        [Environment]::SetEnvironmentVariable($name, $gitEnvironment[$name], 'Process')
    }
    Remove-Item -LiteralPath $root -Recurse -Force
}
