$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Drawing
$fixtureRoot = New-Item -ItemType Directory -Path './artifacts/browser-fixtures' -Force
$fixtureStream = [IO.File]::Open((Join-Path $fixtureRoot.FullName '999999991.ugoira'), [IO.FileMode]::Create)
$archive = [IO.Compression.ZipArchive]::new($fixtureStream, [IO.Compression.ZipArchiveMode]::Create)
try {
    for ($frame = 0; $frame -lt 2; $frame++) {
        $bitmap = [Drawing.Bitmap]::new(16,12)
        $graphics = [Drawing.Graphics]::FromImage($bitmap)
        $graphics.Clear($(if ($frame -eq 0) { [Drawing.Color]::Red } else { [Drawing.Color]::Blue }))
        $stream = $archive.CreateEntry(('00000{0}.png' -f $frame)).Open()
        try { $bitmap.Save($stream, [Drawing.Imaging.ImageFormat]::Png) } finally { $stream.Dispose(); $graphics.Dispose(); $bitmap.Dispose() }
    }
    $writer = [IO.StreamWriter]::new($archive.CreateEntry('animation.json').Open())
    $writer.Write('{"frames":[{"file":"000000.png","delay":200},{"file":"000001.png","delay":200}]}')
    $writer.Dispose()
} finally { $archive.Dispose(); $fixtureStream.Dispose() }
$metadata = @{ idNum='999999991'; id='999999991'; title='动图验收'; user='验收作者'; userId='999991'; type=2; pageCount=1; xRestrict=0; fullWidth=16; fullHeight=12; date='2026-11-16T00:00:00Z'; tags=@('验收'); original='https://i.pximg.net/999999991.ugoira'; fileNames=@('999999991.ugoira') }
$metadata | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath './artifacts/browser-fixtures/999999991-meta.json' -Encoding utf8
Write-Output '已生成双帧动图验收文件'
