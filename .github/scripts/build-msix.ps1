param(
    [Parameter(Mandatory = $true)]
    [ValidatePattern('^v(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)$')]
    [string]$Version,

    [string]$OutputDirectory = (Join-Path $PSScriptRoot '..\..\dist')
)

$ErrorActionPreference = 'Stop'
$versionParts = $Version.Substring(1).Split('.') | ForEach-Object { [int]$_ }
if ($versionParts | Where-Object { $_ -gt 65535 }) {
    throw 'MSIX version components must not exceed 65535.'
}
$packageVersion = '{0}.{1}.{2}.0' -f $versionParts[0], $versionParts[1], $versionParts[2]
$repository = Join-Path $PSScriptRoot '..\..'
$templatePath = Join-Path $repository '.github\msix\AppxManifest.xml'
$template = Get-Content -LiteralPath $templatePath -Raw
$sdkRoot = Join-Path ${env:ProgramFiles(x86)} 'Windows Kits\10\bin'
$makeAppx = Get-ChildItem -LiteralPath $sdkRoot -Directory |
    Sort-Object Name -Descending |
    ForEach-Object { Join-Path $_.FullName 'x64\makeappx.exe' } |
    Where-Object { Test-Path -LiteralPath $_ } |
    Select-Object -First 1
if (-not $makeAppx) {
    throw 'Windows SDK makeappx.exe was not found.'
}

Add-Type -AssemblyName System.Drawing

function New-Logo {
    param([string]$Path, [int]$Size)

    $bitmap = [System.Drawing.Bitmap]::new($Size, $Size)
    $graphics = [System.Drawing.Graphics]::FromImage($bitmap)
    $font = [System.Drawing.Font]::new('Segoe UI', [single]($Size * 0.58), [System.Drawing.FontStyle]::Bold, [System.Drawing.GraphicsUnit]::Pixel)
    $format = [System.Drawing.StringFormat]::new()
    try {
        $graphics.Clear([System.Drawing.Color]::FromArgb(32, 40, 58))
        $format.Alignment = [System.Drawing.StringAlignment]::Center
        $format.LineAlignment = [System.Drawing.StringAlignment]::Center
        $graphics.DrawString('M', $font, [System.Drawing.Brushes]::White,
            [System.Drawing.RectangleF]::new(0, 0, $Size, $Size), $format)
        $bitmap.Save($Path, [System.Drawing.Imaging.ImageFormat]::Png)
    }
    finally {
        $format.Dispose()
        $font.Dispose()
        $graphics.Dispose()
        $bitmap.Dispose()
    }
}

foreach ($architecture in @(
    @{Name = '386'; Package = 'x86'},
    @{Name = 'amd64'; Package = 'x64'},
    @{Name = 'arm64'; Package = 'arm64'}
)) {
    $inputBinary = Join-Path $OutputDirectory "mint-windows-$($architecture.Name).exe"
    if (-not (Test-Path -LiteralPath $inputBinary -PathType Leaf)) {
        throw "Windows $($architecture.Name) executable was not built."
    }
    $stage = Join-Path $OutputDirectory "msix-$($architecture.Name)"
    $null = New-Item -ItemType Directory -Force -Path (Join-Path $stage 'Assets')
    try {
        Copy-Item -LiteralPath $inputBinary -Destination (Join-Path $stage 'mint.exe')
        foreach ($asset in @(
            @{Name = 'StoreLogo.png'; Size = 50},
            @{Name = 'Square44x44Logo.png'; Size = 44},
            @{Name = 'Square150x150Logo.png'; Size = 150}
        )) {
            New-Logo -Path (Join-Path $stage "Assets\$($asset.Name)") -Size $asset.Size
        }
        $manifest = $template.Replace('{{VERSION}}', $packageVersion).Replace('{{ARCH}}', $architecture.Package)
        Set-Content -LiteralPath (Join-Path $stage 'AppxManifest.xml') -Value $manifest -Encoding utf8
        $package = Join-Path $OutputDirectory "mint-windows-$($architecture.Name).msix"
        & $makeAppx pack /d $stage /p $package /o
        if ($LASTEXITCODE -ne 0) {
            throw "MSIX packaging failed for Windows $($architecture.Name)."
        }
    }
    finally {
        Remove-Item -LiteralPath $stage -Recurse -Force -ErrorAction SilentlyContinue
    }
}
