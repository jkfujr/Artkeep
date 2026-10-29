# Artkeep

个人作品收藏站。批量导入 Pixiv 插画、动图、小说和 X 帖子，或上传、扫描文件夹收藏本地作品。

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

## 二进制

从 [Releases](https://github.com/jkfujr/Artkeep/releases/latest) 下载 Windows amd64 或 Linux amd64 压缩包，解压后复制配置文件。`pixiv_dir` 仅作为默认导入来源，导入完成后不再依赖原目录。

```powershell
Copy-Item config.example.json config.local.json
./artkeep.exe -config config.local.json
```

Linux 使用 `./artkeep -config config.local.json`。首次管理员密码显示在启动终端。X 下载需要 [gallery-dl](https://gdl-org.github.io/docs/)，视频处理需要 FFmpeg／FFprobe。

Pixiv 在线导入在服务端设置 `pixiv_refresh_token`，或使用 `ARCHIVE_PIXIV_REFRESH_TOKEN` 环境变量。X 帖子需要登录时，将 Cookie 文件放到 `secrets/x-cookies.txt`，并在 `.env` 设置 `ARCHIVE_COOKIES_FILE=/run/collection/x-cookies.txt`；代理可用 `ARCHIVE_PROXY` 设置。其他配置见 [config.example.json](config.example.json)。凭据只放在服务端配置中。

小说详情中选择“在线阅读”，阅读位置保存在当前浏览器；书籍内容和原文件下载使用相同的 R18 授权。

## 自动发布

PR 自动构建并运行 Go 测试。合并或推送到 `main` 后，通过 CI 自动发布 Windows／Linux 二进制及同版本与 `latest` 镜像；首版为 `v0.1.0`，之后补丁号递增。也可在 Actions 中手动运行 CI/CD。
