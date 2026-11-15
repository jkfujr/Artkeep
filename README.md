# Artkeep

个人作品收藏站。批量导入 Pixiv 插画、动图、小说和 X 帖子，或上传、扫描文件夹收藏 FANBOX 和本地作品。

浏览器直接收藏 Pixiv／X 的安装、保存去向与 PPD 接管见 [Artkeep Collector](collector/README.md)。

- 按平台、作者、标签、年份、图片比例和尺寸筛选
- 多图查看、原文件下载，EPUB 在线阅读、目录、字号和本机阅读进度
- 暗色模式默认跟随设备，可调整列数、间距和图片显示方式
- 公开浏览，管理员管理收藏；R18 可单独设置密码

## Docker

下载本仓库的 [compose.yaml](compose.yaml)，在同目录创建 `.env`：

```dotenv
PIXIV_DIR=/srv/pixiv
```

```sh
mkdir -p data secrets
sudo chown 10001:10001 data
docker compose up -d
# 查看首次生成的管理员密码
docker compose logs collection
```

访问 <http://127.0.0.1:8080>，管理员直接访问 `/admin` 登录。在“导入”中选择多行 URL、本机文件夹或服务器文件夹。目录支持 Powerful Pixiv Downloader 的 `*-meta.txt`、gallery-dl JSON 和普通媒体。对外访问时通过反向代理提供 HTTPS。

镜像：`ghcr.io/jkfujr/artkeep:latest`（Linux amd64），也可固定使用版本标签。更新时执行 `docker compose pull && docker compose up -d`。

导入复制到 `data/collection/<平台>/<类型>/<作者ID>/<作品ID>/`，原文件保留；本地作品使用内容 ID，作者为 `unknown`。`data/` 保存全部收藏、索引和预览，`staging/` 保存未完成上传和任务。已有收藏可在管理页“迁移已有收藏”，失败项可单独重试。

服务器路径指容器可访问的目录，需要将来源目录只读挂载到容器；默认 Pixiv 来源挂载到 `/library/pixiv`。本机文件夹以 8 MiB 分块上传，刷新或重启后重新选择原文件夹可继续。

FANBOX 本地收藏选择“FANBOX 文件夹”格式，目录结构为 `<作者ID>-<作者名>/<YYYY-MM-DD>-<标题>/图片`，可选择收藏根目录或作者目录。默认 R18，可调整；重复提交会新增作品，暂不去重，不恢复原帖链接。

## 二进制

从 [Releases](https://github.com/jkfujr/Artkeep/releases/latest) 下载 Windows amd64 或 Linux amd64 压缩包，解压后复制配置文件。`pixiv_dir` 仅作为默认导入来源，导入完成后不再依赖原目录。

```powershell
Copy-Item config.example.json config.local.json
./artkeep.exe -config config.local.json
```

Linux 使用 `./artkeep -config config.local.json`。首次管理员密码显示在启动终端。X 下载需要 [gallery-dl](https://gdl-org.github.io/docs/)，视频处理需要 FFmpeg／FFprobe。

Pixiv 在线导入在服务端设置 `pixiv_refresh_token`，或使用 `ARCHIVE_PIXIV_REFRESH_TOKEN` 环境变量。X 帖子需要登录时，将 Cookie 文件放到 `secrets/x-cookies.txt`，并在 `.env` 设置 `ARCHIVE_COOKIES_FILE=/run/collection/x-cookies.txt`；代理可用 `ARCHIVE_PROXY` 设置。其他配置见 [config.example.json](config.example.json)。凭据只放在服务端配置中。

小说详情中选择“在线阅读”，阅读位置保存在当前浏览器；书籍内容和原文件下载使用相同的 R18 授权。

## RESTful API

正式接口统一为 `/api/v1`，网页和浏览器插件使用相同的作品、上传与任务资源。旧接口已移除，外部脚本需要同步更新。

[OpenAPI 3.1.1 契约](backend/internal/server/openapi.json) 随服务通过 `/api/v1/openapi.json` 提供，包含全部路由、字段、鉴权与响应。列表使用分页对象，错误使用 `application/problem+json`；上传创建、任务创建和任务重试要求 `Idempotency-Key`。同一请求重试沿用原键。

本地检查命令与最近结果见 [验证说明](docs/VALIDATION.md)。

管理员在管理页创建绑定扩展来源的访问令牌。插件保存令牌，令牌仅允许上传、导入自己的文件和查询相关任务；管理员可以撤销。

以下 PowerShell 示例从仓库根目录执行，`ARTKEEP_ACCESS_TOKEN` 从本机私有环境读取。上传目录由运行时输入：

```powershell
$artkeepServer = 'http://127.0.0.1:8080'
$artkeepHeaders = @{ Authorization = "Bearer $env:ARTKEEP_ACCESS_TOKEN" }
$artkeepSource = (Resolve-Path -LiteralPath (Read-Host '上传目录')).Path
$artkeepFiles = @(Get-ChildItem -LiteralPath $artkeepSource -File -Recurse)
$artkeepManifest = @($artkeepFiles | ForEach-Object {
    @{ path = [IO.Path]::GetRelativePath($artkeepSource, $_.FullName).Replace('\', '/'); size = $_.Length; modified = 0 }
})
$artkeepHeaders['Idempotency-Key'] = [guid]::NewGuid().ToString()
$artkeepUpload = Invoke-RestMethod "$artkeepServer/api/v1/uploads" -Method Post -Headers $artkeepHeaders -ContentType 'application/json' -Body (@{ name = '收藏'; files = $artkeepManifest } | ConvertTo-Json -Depth 5)
for ($i = 0; $i -lt $artkeepFiles.Count; $i++) {
    $artkeepStream = [IO.File]::OpenRead($artkeepFiles[$i].FullName)
    try {
        $artkeepBuffer = [byte[]]::new($artkeepUpload.chunkSize)
        $artkeepOffset = 0L
        while (($artkeepRead = $artkeepStream.Read($artkeepBuffer, 0, $artkeepBuffer.Length)) -gt 0) {
            $artkeepChunk = if ($artkeepRead -eq $artkeepBuffer.Length) { $artkeepBuffer } else { [byte[]]$artkeepBuffer[0..($artkeepRead - 1)] }
            Invoke-RestMethod "$artkeepServer/api/v1/uploads/$($artkeepUpload.id)/files/$i/chunks/$artkeepOffset" -Method Put -Headers $artkeepHeaders -ContentType 'application/octet-stream' -Body $artkeepChunk
            $artkeepOffset += $artkeepRead
        }
    } finally { $artkeepStream.Dispose() }
}
$artkeepHeaders['Idempotency-Key'] = [guid]::NewGuid().ToString()
$artkeepJob = Invoke-RestMethod "$artkeepServer/api/v1/jobs" -Method Post -Headers $artkeepHeaders -ContentType 'application/json' -Body (@{ kind = 'import'; source = 'upload'; uploadId = $artkeepUpload.id } | ConvertTo-Json)
Invoke-RestMethod "$artkeepServer/api/v1/jobs/$($artkeepJob.id)" -Headers $artkeepHeaders
```

断线后先查询 `GET /api/v1/uploads/{id}`，按文件 `offset` 继续。文件清单创建后固定；清单变化时创建新上传。`PATCH /api/v1/jobs/{id}` 提交 `{"status":"queued"}` 重试失败项，已完成子项保留。

`.ugoira` 原始帧与 `animation.json` 保存在收藏库，FFmpeg 生成 MP4 播放预览和封面；原文件下载保留 `.ugoira`。播放资源和原件共用 R18 授权。

## 源码构建

后端位于 `backend/`，前端位于 `frontend/`，构建产物位于 `artifacts/`。需要 Go 1.26+、Node.js 24+。以下 PowerShell 命令从仓库根目录执行：

```powershell
npm --prefix frontend ci
npm --prefix frontend run build
Copy-Item -LiteralPath ./artifacts/frontend -Destination ./backend/web/dist -Recurse
try {
    go -C backend build -trimpath -ldflags "-s -w" -o ../artifacts/artkeep.exe ./cmd/collection
    if ($LASTEXITCODE -ne 0) { throw '后端构建失败' }
} finally {
    Remove-Item -LiteralPath ./backend/web/dist -Recurse
}
```

前端静态资源随程序打包。从仓库根目录运行，沿用根目录配置和数据目录：

```powershell
./artifacts/artkeep.exe -config ./config.local.json
```

## 自动发布

PR 自动构建并运行 Go 测试。合并或推送到 `main` 后，通过 CI 自动发布 Windows／Linux 二进制及同版本与 `latest` 镜像；首版为 `v0.1.0`，之后补丁号递增。也可在 Actions 中手动运行 CI/CD。
