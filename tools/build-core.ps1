param(
    [ValidateSet('armv7','check-windows')][string]$Target='armv7',
    [ValidateSet('full-cli','slim-cli','native-small')][string]$Profile='full-cli',
    [string]$ModuleCache=''
)
$ErrorActionPreference='Stop'
$root=(Resolve-Path "$PSScriptRoot/..").Path
$archive="$root/.local/tools/sing-box-source.tar.gz"
$sourceBase="$root/.local/core-builds/$([guid]::NewGuid().ToString('N'))"
$source="$sourceBase/sing-box-1.14.2"
$go="$root/.local/tools/go/bin/go.exe"
$upx="$root/.local/tools/upx-5.2.1-win64/upx.exe"
if(!(Test-Path $archive)){
    New-Item -ItemType Directory -Force "$root/.local/tools" | Out-Null
    Invoke-WebRequest 'https://github.com/SagerNet/sing-box/archive/refs/tags/v1.14.2.tar.gz' -OutFile $archive
}
if((Get-FileHash $archive).Hash -ne '67DD8F8C37ECAAADCFCAFAD1F0827EED4B034C963B86FD3AA5C0D7A36876845D'){throw 'Source archive hash mismatch'}
New-Item -ItemType Directory -Path $sourceBase -Force | Out-Null
& tar -xf $archive -C $sourceBase
if($LASTEXITCODE -ne 0){throw 'Source extraction failed'}
& python "$PSScriptRoot/core-profile.py" $source
if($LASTEXITCODE -ne 0){throw 'Core profile failed'}
$env:CGO_ENABLED='0';$env:GOOS='linux';$env:GOARCH='arm';$env:GOARM='7'
$env:GOPATH="$root/.local/gopath";$env:GOCACHE="$root/.local/gocache"
if($ModuleCache){$env:GOMODCACHE=$ModuleCache}
$filename='routerlite-core-armv7'
if($Target -eq 'check-windows'){$env:GOOS='windows';$env:GOARCH='amd64';$filename='routerlite-core-check.exe'}
if($Profile -ne 'full-cli'){$filename="routerlite-core-$Profile-$Target";if($Target -eq 'check-windows'){$filename+='.exe'}}
$entrypoint=if($Profile -eq 'full-cli'){'./cmd/sing-box'}else{'./cmd/routerlite-core'}
$coreVersion=if($Profile -eq 'full-cli'){'1.14.2-routerlite'}else{"1.14.2-routerlite-$Profile"}
$buildFlags=@()
if($Profile -eq 'native-small'){$buildFlags+='-gcflags=all=-l'}
Push-Location $source
try{
    New-Item -ItemType Directory -Force "$root/dist" | Out-Null
    $buildFile="$root/dist/$filename.build-$([guid]::NewGuid().ToString('N'))"
    & $go build -buildvcs=false -trimpath -tags with_utls @buildFlags "-ldflags=-s -w -X github.com/sagernet/sing-box/constant.Version=$coreVersion" -o $buildFile $entrypoint
    if($LASTEXITCODE -ne 0){throw 'Core build failed'}
    Copy-Item -LiteralPath $buildFile -Destination "$root/dist/$filename" -Force
    Copy-Item -LiteralPath $buildFile -Destination "$root/dist/$filename.raw" -Force
    Remove-Item -LiteralPath $buildFile
    if($Target -eq 'armv7' -and $Profile -ne 'native-small'){
        & $upx --best --lzma "$root/dist/$filename"
        if($LASTEXITCODE -ne 0){throw 'Compression failed'}
        & $upx -t "$root/dist/$filename"
        if($LASTEXITCODE -ne 0){throw 'Integrity check failed'}
    }
}finally{Pop-Location}
