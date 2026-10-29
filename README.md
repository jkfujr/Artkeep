# Artkeep

个人作品收藏站。导入 Pixiv 本地收藏，或粘贴 X 帖子链接收藏图片、视频和动图。

- 按平台、作者、标签、年份、图片比例和尺寸筛选
- 多图查看、原文件下载，支持 EPUB 下载
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

访问 <http://127.0.0.1:8080>，登录管理页后点击“扫描目录”。Pixiv 导入支持 Powerful Pixiv Downloader 的 `*-meta.txt` 格式。对外访问时通过反向代理提供 HTTPS。

镜像：`ghcr.io/jkfujr/artkeep:latest`（Linux amd64），也可固定使用版本标签。更新时执行 `docker compose pull && docker compose up -d`。

原收藏只读挂载；`data/` 保存索引、预览和下载内容，迁移时与原收藏一起保留。

## 二进制

从 [Releases](https://github.com/jkfujr/Artkeep/releases/latest) 下载 Windows amd64 或 Linux amd64 压缩包，解压后复制配置文件，设置 `pixiv_dir`。

```powershell
Copy-Item config.example.json config.local.json
./artkeep.exe -config config.local.json
```

Linux 使用 `./artkeep -config config.local.json`。首次管理员密码显示在启动终端。X 下载需要 [gallery-dl](https://gdl-org.github.io/docs/)，视频处理需要 FFmpeg／FFprobe。

X 帖子需要登录时，将 Cookie 文件放到 `secrets/x-cookies.txt`，并在 `.env` 设置 `ARCHIVE_COOKIES_FILE=/run/collection/x-cookies.txt`；代理可用 `ARCHIVE_PROXY` 设置。其他配置见 [config.example.json](config.example.json)。

## 自动发布

PR 自动构建并运行 Go 测试。合并或推送到 `main` 后，通过 CI 自动发布 Windows／Linux 二进制及同版本与 `latest` 镜像；首版为 `v0.1.0`，之后补丁号递增。也可在 Actions 中手动运行 CI/CD。
