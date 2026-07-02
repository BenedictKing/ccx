param(
    [Parameter(Mandatory = $true)][string]$RootDir,
    [Parameter(Mandatory = $true)][string]$Arch,
    [Parameter(Mandatory = $true)][string]$Version,
    [Parameter(Mandatory = $true)][string]$PackageName,
    [Parameter(Mandatory = $true)][string]$Publisher,
    [Parameter(Mandatory = $true)][string]$PublisherDisplayName,
    [Parameter(Mandatory = $true)][string]$DisplayName,
    [Parameter(Mandatory = $true)][string]$Description,
    [string]$OutPath = ""
)

$ErrorActionPreference = "Stop"

function Convert-MsixVersion([string]$Value) {
    $clean = $Value.TrimStart("v")
    $parts = @($clean.Split(".") | ForEach-Object { if ($_ -match "^\d+$") { $_ } else { "0" } })
    while ($parts.Count -lt 3) { $parts += "0" }
    return "$($parts[0]).$($parts[1]).$($parts[2]).0"
}

function Convert-MsixArch([string]$Value) {
    switch ($Value) {
        "amd64" { return "x64" }
        "386" { return "x86" }
        "arm64" { return "arm64" }
        default { return $Value }
    }
}

function Escape-Xml([string]$Value) {
    return [System.Security.SecurityElement]::Escape($Value)
}

function Find-MakeAppx {
    $cmd = Get-Command makeappx.exe -ErrorAction SilentlyContinue
    if ($cmd) { return $cmd.Source }
    $kitsRoot = Join-Path ${env:ProgramFiles(x86)} "Windows Kits\10\bin"
    $match = Get-ChildItem -Path $kitsRoot -Filter makeappx.exe -Recurse -ErrorAction SilentlyContinue |
        Where-Object { $_.FullName -match "\\x64\\makeappx\.exe$" } |
        Sort-Object FullName -Descending |
        Select-Object -First 1
    if ($match) { return $match.FullName }
    throw "makeappx.exe not found. Install the Windows SDK on this runner."
}

function New-Logo([System.Drawing.Image]$Source, [string]$Path, [int]$Width, [int]$Height) {
    $bitmap = New-Object System.Drawing.Bitmap($Width, $Height)
    $graphics = [System.Drawing.Graphics]::FromImage($bitmap)
    $graphics.Clear([System.Drawing.Color]::Transparent)
    $graphics.InterpolationMode = [System.Drawing.Drawing2D.InterpolationMode]::HighQualityBicubic
    $scale = [Math]::Min($Width / $Source.Width, $Height / $Source.Height)
    $drawWidth = [int]($Source.Width * $scale)
    $drawHeight = [int]($Source.Height * $scale)
    $x = [int](($Width - $drawWidth) / 2)
    $y = [int](($Height - $drawHeight) / 2)
    $graphics.DrawImage($Source, $x, $y, $drawWidth, $drawHeight)
    $bitmap.Save($Path, [System.Drawing.Imaging.ImageFormat]::Png)
    $graphics.Dispose()
    $bitmap.Dispose()
}

$binDir = Join-Path $RootDir "bin"
$packageDir = Join-Path $binDir "msix-$Arch"
$assetsDir = Join-Path $packageDir "Assets"
$desktopExe = Join-Path $binDir "ccx-desktop.exe"
$backendExe = Join-Path $binDir "ccx-go.exe"
$templatePath = Join-Path $RootDir "build/windows/msix/app_manifest.xml"
$sourceIcon = Join-Path $RootDir "build/appicon-windows.png"
$staticAssetsDir = Join-Path $RootDir "build/windows"
if ([string]::IsNullOrWhiteSpace($OutPath)) {
    $OutPath = Join-Path $binDir "ccx-desktop-$Arch.msix"
}

if (!(Test-Path $desktopExe)) { throw "Desktop executable not found: $desktopExe" }
if (!(Test-Path $backendExe)) { throw "Bundled backend executable not found: $backendExe" }
if (!(Test-Path $templatePath)) { throw "MSIX manifest template not found: $templatePath" }
if (!(Test-Path $sourceIcon)) { throw "MSIX icon source not found: $sourceIcon" }

Remove-Item $packageDir -Recurse -Force -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Path $packageDir, $assetsDir | Out-Null
Copy-Item $desktopExe (Join-Path $packageDir "ccx-desktop.exe")
Copy-Item $backendExe (Join-Path $packageDir "ccx-go.exe")

# Copy static tile assets if available, otherwise generate from source icon
$staticTileAssets = @(
    "StoreLogo.png",
    "Square30x30Logo.png",
    "Square44x44Logo.png",
    "Square71x71Logo.png",
    "Square89x89Logo.png",
    "Square107x107Logo.png",
    "Square142x142Logo.png",
    "Square150x150Logo.png",
    "Square284x284Logo.png",
    "Square310x310Logo.png",
    "Wide310x150Logo.png",
    "SplashScreen.png"
)

$useStaticAssets = $true
foreach ($assetName in $staticTileAssets) {
    $srcPath = Join-Path $staticAssetsDir $assetName
    if (!(Test-Path $srcPath)) {
        $useStaticAssets = $false
        break
    }
}

if ($useStaticAssets) {
    foreach ($assetName in $staticTileAssets) {
        $srcPath = Join-Path $staticAssetsDir $assetName
        Copy-Item $srcPath (Join-Path $assetsDir $assetName)
    }
} else {
    Add-Type -AssemblyName System.Drawing
    $icon = [System.Drawing.Image]::FromFile($sourceIcon)
    try {
        New-Logo $icon (Join-Path $assetsDir "StoreLogo.png") 50 50
        New-Logo $icon (Join-Path $assetsDir "Square44x44Logo.png") 44 44
        New-Logo $icon (Join-Path $assetsDir "Square150x150Logo.png") 150 150
        New-Logo $icon (Join-Path $assetsDir "Wide310x150Logo.png") 310 150
        New-Logo $icon (Join-Path $assetsDir "SplashScreen.png") 620 300
    } finally {
        $icon.Dispose()
    }
}

$optionalAssetPatterns = @(
    "Square44x44Logo.targetsize-*.png"
)

foreach ($pattern in $optionalAssetPatterns) {
    Get-ChildItem -Path $staticAssetsDir -Filter $pattern -File -ErrorAction SilentlyContinue |
        ForEach-Object { Copy-Item $_.FullName (Join-Path $assetsDir $_.Name) }
}

$manifest = Get-Content $templatePath -Raw
$values = @{
    "{{PACKAGE_NAME}}" = Escape-Xml $PackageName
    "{{PUBLISHER}}" = Escape-Xml $Publisher
    "{{VERSION}}" = Escape-Xml (Convert-MsixVersion $Version)
    "{{ARCHITECTURE}}" = Escape-Xml (Convert-MsixArch $Arch)
    "{{DISPLAY_NAME}}" = Escape-Xml $DisplayName
    "{{PUBLISHER_DISPLAY_NAME}}" = Escape-Xml $PublisherDisplayName
    "{{DESCRIPTION}}" = Escape-Xml $Description
}
foreach ($key in $values.Keys) {
    $manifest = $manifest.Replace($key, $values[$key])
}
Set-Content -Path (Join-Path $packageDir "AppxManifest.xml") -Value $manifest -Encoding utf8

$makeAppx = Find-MakeAppx
Remove-Item $OutPath -Force -ErrorAction SilentlyContinue
& $makeAppx pack /d $packageDir /p $OutPath /o
if ($LASTEXITCODE -ne 0) { throw "makeappx.exe failed with exit code $LASTEXITCODE" }
Write-Output $OutPath
