param(
    [Parameter(Mandatory = $true)]
    [ValidatePattern('^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$')]
    [string]$Version,

    [string]$OutputDirectory = (Join-Path $PSScriptRoot '..\..\dist')
)

$ErrorActionPreference = 'Stop'
$platforms = @(
    'darwin-amd64', 'darwin-arm64',
    'freebsd-386', 'freebsd-amd64', 'freebsd-arm64',
    'linux-386', 'linux-amd64', 'linux-arm', 'linux-arm64',
    'windows-386', 'windows-amd64', 'windows-arm64'
)
$originalEnvironment = @{}
foreach ($name in @('GOOS', 'GOARCH', 'CGO_ENABLED')) {
    $originalEnvironment[$name] = [Environment]::GetEnvironmentVariable($name, 'Process')
}

$null = New-Item -ItemType Directory -Force -Path $OutputDirectory
$OutputDirectory = (Resolve-Path -LiteralPath $OutputDirectory).Path
Push-Location (Join-Path $PSScriptRoot '..\..')
try {
    $supported = @(& go tool dist list)
    if ($LASTEXITCODE -ne 0) {
        throw 'Cannot determine supported Go platforms.'
    }
    $env:CGO_ENABLED = '0'
    foreach ($platform in $platforms) {
        $targetOS, $targetArch = $platform.Split('-')
        if ($supported -notcontains "$targetOS/$targetArch") {
            Write-Warning "Skipping unsupported Go platform $platform."
            continue
        }
        $env:GOOS = $targetOS
        $env:GOARCH = $targetArch
        $extension = ''
        if ($targetOS -eq 'windows') {
            $extension = '.exe'
        }
        $output = Join-Path $OutputDirectory "mint-$platform$extension"
        & go build -trimpath "-ldflags=-s -w -X=main.version=$Version" -o $output .
        if ($LASTEXITCODE -ne 0) {
            throw "Build failed for $platform."
        }
    }
}
finally {
    Pop-Location
    foreach ($name in $originalEnvironment.Keys) {
        if ($null -eq $originalEnvironment[$name]) {
            Remove-Item -LiteralPath "Env:$name" -ErrorAction SilentlyContinue
        } else {
            [Environment]::SetEnvironmentVariable($name, $originalEnvironment[$name], 'Process')
        }
    }
}
