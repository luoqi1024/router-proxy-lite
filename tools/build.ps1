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
    $compatFlags=@()
    if($Target -ne 'demo'){
        $overlay=& python "$PSScriptRoot/go-compat-overlay.py" --go $Go --output "$PWD/.local/go-compat"
        if($LASTEXITCODE -ne 0){throw 'Go compatibility overlay failed'}
        $compatFlags+="-overlay=$overlay"
    }
    $name="routerlite-$Target"
    if($Target -eq 'demo'){$env:GOOS='windows';$name='routerlite-demo.exe'}
    New-Item -ItemType Directory -Force dist | Out-Null
    # UPX preserves Go's build ID; go build may otherwise reuse an already packed file.
    $buildFile="$PWD/dist/$name.build-$([guid]::NewGuid().ToString('N'))"
    & $Go build @compatFlags -buildvcs=false -trimpath '-ldflags=-s -w' -o $buildFile ./cmd/routerlite
    if($LASTEXITCODE -ne 0){throw 'Build failed'}
    Copy-Item -LiteralPath $buildFile -Destination "dist/$name" -Force
    Copy-Item -LiteralPath $buildFile -Destination "dist/$name.raw" -Force
    Remove-Item -LiteralPath $buildFile
    $raw=(Get-Item "dist/$name").Length
    if($Target -ne 'demo' -and (Test-Path $Upx)){
        & $Upx --best --lzma "dist/$name"
        if($LASTEXITCODE -ne 0){throw 'Compression failed'}
        & $Upx -t "dist/$name"
        if($LASTEXITCODE -ne 0){throw 'Compression integrity check failed'}
    }
    [pscustomobject]@{Target=$Target;RawBytes=$raw;FileBytes=(Get-Item "dist/$name").Length;SHA256=(Get-FileHash "dist/$name").Hash} | ConvertTo-Json | Set-Content "dist/$name.build.json"
} finally {Pop-Location}
