param(
    [Parameter(Mandatory = $true)]
    [ValidatePattern('^v(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)$')]
    [string]$Version,

    [Parameter(Mandatory = $true)]
    [ValidatePattern('^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$')]
    [string]$Repository,

    [string]$OutputDirectory = (Join-Path $PSScriptRoot '..\..\dist')
)

$ErrorActionPreference = 'Stop'
$versionParts = $Version.Substring(1).Split('.') | ForEach-Object { [int]$_ }
if ($versionParts | Where-Object { $_ -gt 65535 }) {
    throw 'AppInstaller version components must not exceed 65535.'
}
$packageVersion = '{0}.{1}.{2}.0' -f $versionParts[0], $versionParts[1], $versionParts[2]
$releaseBase = "https://github.com/$Repository/releases"

foreach ($architecture in @(
    @{Name = '386'; Package = 'x86'},
    @{Name = 'amd64'; Package = 'x64'},
    @{Name = 'arm64'; Package = 'arm64'}
)) {
    $fileName = "mint-windows-$($architecture.Name)"
    $manifest = @"
<?xml version="1.0" encoding="utf-8"?>
<AppInstaller
  xmlns="http://schemas.microsoft.com/appx/appinstaller/2018"
  Uri="$releaseBase/latest/download/$fileName.appinstaller"
  Version="$packageVersion">
  <MainPackage
    Name="JanDeDobbeleer.mint"
    Publisher="CN=Jan Joris De Dobbeleer, O=Jan Joris De Dobbeleer, L=Diest, C=BE"
    Version="$packageVersion"
    Uri="$releaseBase/download/$Version/$fileName.msix"
    ProcessorArchitecture="$($architecture.Package)" />
  <UpdateSettings>
    <OnLaunch HoursBetweenUpdateChecks="0" />
  </UpdateSettings>
</AppInstaller>
"@
    $outputPath = Join-Path $OutputDirectory "$fileName.appinstaller"
    Set-Content -LiteralPath $outputPath -Value $manifest -Encoding utf8
}
