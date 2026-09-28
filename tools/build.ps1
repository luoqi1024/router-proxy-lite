param(
    [ValidateSet('demo','armv7','arm64','mipsle','mips')][string]$Target='demo',
    [string]$Go="$PSScriptRoot/../.local/tools/go/bin/go.exe",
    [string]$Upx="$PSScriptRoot/../.local/tools/upx-5.2.1-win64/upx.exe"
)
$ErrorActionPreference='Stop'
Push-Location "$PSScriptRoot/.."
try {
    $env:CGO_ENABLED='0'
    $env:GOPATH="$PWD/.local/gopath"
    $env:GOCACHE="$PWD/.local/gocache"
    $env:GOOS='linux'
    $env:GOARM='7'
    $env:GOMIPS='softfloat'
    $env:GOARCH=switch($Target){ 'demo' {'amd64'} 'armv7' {'arm'} 'arm64' {'arm64'} 'mipsle' {'mipsle'} 'mips' {'mips'} }
    $name="routerlite-$Target"
    if($Target -eq 'demo'){$env:GOOS='windows';$name='routerlite-demo.exe'}
    New-Item -ItemType Directory -Force dist | Out-Null
    & $Go build -trimpath '-ldflags=-s -w' -o "dist/$name" ./cmd/routerlite
    if($LASTEXITCODE -ne 0){throw 'Build failed'}
    $raw=(Get-Item "dist/$name").Length
    if($Target -ne 'demo' -and (Test-Path $Upx)){
        Copy-Item "dist/$name" "dist/$name.raw"
        & $Upx --best --lzma "dist/$name"
        if($LASTEXITCODE -ne 0){throw 'Compression failed'}
        & $Upx -t "dist/$name"
        if($LASTEXITCODE -ne 0){throw 'Compression integrity check failed'}
    }
    [pscustomobject]@{Target=$Target;RawBytes=$raw;FileBytes=(Get-Item "dist/$name").Length;SHA256=(Get-FileHash "dist/$name").Hash} | ConvertTo-Json | Set-Content "dist/$name.build.json"
} finally {Pop-Location}
