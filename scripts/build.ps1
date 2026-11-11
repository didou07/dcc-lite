param(
    [switch]$Win7,
    [ValidateSet("amd64", "386")][string]$Arch = "amd64"
)
$ErrorActionPreference = "Stop"
Set-Location (Split-Path $PSScriptRoot -Parent)
$Version = (Get-Content VERSION -Raw).Trim()

$Go = "go"
$Suffix = ""
if ($Win7) {
    if (-not $env:GO_WIN7 -or -not (Test-Path $env:GO_WIN7)) {
        throw "-Win7 needs the patched Go toolchain: set `$env:GO_WIN7 to its go.exe (https://github.com/XTLS/go-win7)"
    }
    $Go = $env:GO_WIN7
    $Suffix = "-win7"
}

function Invoke-Go {
    & $Go @args
    if ($LASTEXITCODE -ne 0) { throw "go $($args -join ' ') failed (exit $LASTEXITCODE)" }
}

$Name = "DCC-Lite-$Version-windows-$Arch$Suffix"
$Dir = "dist\$Name"
try {
    if ($Win7) { $env:GOTOOLCHAIN = "local" }

    Remove-Item $Dir, "dist\$Name.zip", "dist\$Name.zip.sha256" -Recurse -Force -ErrorAction SilentlyContinue
    New-Item -ItemType Directory -Force $Dir | Out-Null
    $env:CGO_ENABLED = "0"; $env:GOOS = "windows"; $env:GOARCH = $Arch
    Invoke-Go build -trimpath -buildvcs=false -ldflags "-s -w -buildid= -X main.version=$Version" -o "$Dir\DCCLite.exe" .\src\dcclite
}
finally {
    Remove-Item Env:CGO_ENABLED, Env:GOOS, Env:GOARCH, Env:GOTOOLCHAIN -ErrorAction SilentlyContinue
}
Compress-Archive -Path "$Dir\DCCLite.exe" -DestinationPath "dist\$Name.zip" -Force
Remove-Item $Dir -Recurse -Force
$Hash = (Get-FileHash -Algorithm SHA256 "dist\$Name.zip").Hash.ToLower()
Set-Content -Encoding ASCII "dist\$Name.zip.sha256" "$Hash  $Name.zip"
Write-Host "Built dist\$Name.zip" -ForegroundColor Green
