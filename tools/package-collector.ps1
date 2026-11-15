$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
$artifactsRoot = Join-Path $projectRoot 'artifacts'
$collectorRoot = Join-Path $artifactsRoot 'collector'
New-Item -ItemType Directory -Path $collectorRoot -Force | Out-Null
Get-ChildItem -LiteralPath (Join-Path $projectRoot 'collector') -File |
    Where-Object { $_.Extension -in '.js','.html','.css','.json','.txt','.md' -and $_.Name -ne 'package.json' } |
    Copy-Item -Destination $collectorRoot -Force
$manifest = Get-Content -LiteralPath (Join-Path $collectorRoot 'manifest.json') -Raw | ConvertFrom-Json
$collectorZip = Join-Path $artifactsRoot ("artkeep-collector-{0}.zip" -f $manifest.version)
Compress-Archive -Path (Join-Path $collectorRoot '*') -DestinationPath $collectorZip -Force
Write-Output ("打包完成：{0}" -f $collectorZip)
