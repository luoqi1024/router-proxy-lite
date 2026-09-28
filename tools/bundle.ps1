param([string]$Assets="$PSScriptRoot/../.local/assets")
$ErrorActionPreference='Stop'
$root=(Resolve-Path "$PSScriptRoot/..").Path
$bundle="$root/dist/routerlite-armv7-preview"
if(Test-Path $bundle){throw 'Bundle already exists; use a fresh build directory after reviewing the old bundle'}
foreach($file in @('ca-certificates.crt','geosite-cn.srs','geoip-cn.srs')){if(!(Test-Path "$Assets/$file")){throw "Missing asset: $file"}}
New-Item -ItemType Directory -Force "$bundle/bin","$bundle/assets","$bundle/scripts" | Out-Null
Copy-Item "$root/dist/routerlite-armv7" "$bundle/bin/routerlite"
Copy-Item "$root/dist/routerlite-core-armv7" "$bundle/bin/sing-box"
Copy-Item "$Assets/ca-certificates.crt","$Assets/geosite-cn.srs","$Assets/geoip-cn.srs" "$bundle/assets/"
Copy-Item "$root/scripts/*" "$bundle/scripts/"
Copy-Item "$root/THIRD_PARTY.md","$root/LICENSE" "$bundle/"
$manifest=Get-ChildItem $bundle -File -Recurse | Sort-Object FullName | ForEach-Object {
    $relative=$_.FullName.Substring($bundle.Length+1).Replace('\','/')
    '{0}  {1}' -f (Get-FileHash $_.FullName).Hash.ToLower(),$relative
}
[IO.File]::WriteAllText("$bundle/SHA256SUMS",(($manifest -join "`n")+"`n"),[Text.UTF8Encoding]::new($false))
$files=Get-ChildItem $bundle -File -Recurse
$bytes=($files|Measure-Object Length -Sum).Sum
[pscustomobject]@{Bytes=$bytes;MiB=[math]::Round($bytes/1MB,3);InstallReserveMiB=2;Status='UNTESTED ON HARDWARE'} | ConvertTo-Json | Set-Content "$root/dist/size-report.json"
Write-Output "Development bundle: $bundle ($bytes bytes). Not a validated public release."
