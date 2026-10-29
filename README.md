# 作品收藏

Go、SQLite 与 React 构建的个人作品收藏站。公开浏览，管理员导入 Pixiv 本地收藏或通过 X 帖子链接收藏图片、视频和动图。界面使用 HeroUI v3，支持侧栏筛选、浅色与暗色主题和 R18 可选密码。

## 浏览

主题默认跟随设备，可在顶部切换浅色或深色。桌面使用左侧筛选栏，手机通过“筛选”打开侧栏。

支持按平台、作者、标签、发布时间年份、图片比例和最小宽高组合筛选。多个标签同时满足，多个年份或比例满足其一。图片比例和尺寸由作品内同一张可用图片满足，匹配的第一张图片作为封面；视频和 EPUB 不参与图片条件匹配。筛选保存在 URL，返回图册时恢复浏览位置。

列数、间距、铺满宽度和图片完整显示／裁切可在侧栏调整，这些偏好与主题保存在当前浏览器。详情页始终完整显示图片。

## Windows

需要 Go 1.26+、Node.js 24+。将 [gallery-dl](https://gdl-org.github.io/docs/) 与 FFmpeg、FFprobe 放入 PATH，或在配置中填写可执行文件路径。

```powershell
cd web
npm ci
npm run build
cd ..
go build -o bin/collection.exe ./cmd/collection
Copy-Item config.example.json config.local.json
./bin/collection.exe -config config.local.json
```

在 `config.local.json` 中将 `pixiv_dir` 指向已有收藏目录。默认访问 [http://127.0.0.1:8080](http://127.0.0.1:8080)。首次启动时，管理员密码显示在启动终端；也可以在首次启动前设置 `ARCHIVE_ADMIN_PASSWORD`（10–72 字节）。密码只以哈希保存到数据库。

登录管理页面，点击“扫描目录”。支持 Powerful Pixiv Downloader 的 `*-meta.txt` 导出格式，图片与元数据应保留当前相邻关系。

开发时先完成一次前端构建，再运行 Go 服务。在另一终端执行 `cd web; npm run dev`，Vite 将 `/api` 请求转发到 Go 服务。

## Linux Docker

创建 `.env`，设置原图目录；管理员密码可在首次启动前设置，也可从首次启动日志读取自动生成的密码。

```dotenv
PIXIV_DIR=/srv/collection/pixiv
ARCHIVE_ADMIN_PASSWORD=请替换为自己的管理员密码
```

```sh
mkdir -p data secrets
sudo chown 10001:10001 data
docker compose up -d --build
```

服务绑定服务器本机 `127.0.0.1:8080`，通过现有反向代理提供 HTTPS，并转发原始 Host。上传限制不影响文件导入；视频需要代理保留 Range 请求和 206 响应。应用已对受限媒体禁用公共缓存，反向代理应遵循应用的缓存头。

原始 Pixiv 目录按只读方式挂载。应用写入 `data/collection.db`、`data/collection/`、`data/staging/` 和 `data/previews/`。迁移时保留原收藏相对目录及应用的整个 `data` 目录，配置新的 `pixiv_dir` 即可；本地标签、分级和任务记录保存在数据库。

## X 收藏

管理员粘贴单条 `https://x.com/.../status/...` 或 Twitter 帖子链接。图片只取 `orig`，不回退到小图；视频取可获取的最高码率版本。仅收藏指定帖子，不扩展回复和引用内容。

访问需要登录的帖子时，在服务端配置 Netscape 格式的 Cookie 文件：

```json
{
  "cookies_file": "F:/Private/x-cookies.txt",
  "proxy": ""
}
```

Docker 中将文件放在 `secrets/x-cookies.txt`，允许容器用户（UID 10001）读取，在 `.env` 设置 `ARCHIVE_COOKIES_FILE=/run/collection/x-cookies.txt`。有代理需求时设置 `proxy` 或 `ARCHIVE_PROXY`。配置变更后重启服务；登录文件不会返回浏览器或写入作品元数据。

重试会复用已下载文件。完整落盘但尚未完成入库的任务，会先读取本地结果继续处理。缺失敏感标记的 X 作品默认公开；管理员可补充本地标签或覆盖分级，重新扫描不会覆盖人工修改。

## 密码与访问

管理员密码与 R18 浏览密码独立。R18 默认关闭，R-18G 与其共用开关。在管理页面设置是否需要 R18 密码；修改后旧的成人浏览授权失效。管理员和成人浏览会话有效期为 24 小时。

更新管理员密码：

```powershell
$env:ARCHIVE_ADMIN_PASSWORD='请替换为新的管理员密码'
./bin/collection.exe -config config.local.json -set-admin-password
Remove-Item Env:ARCHIVE_ADMIN_PASSWORD
```

此命令同时注销已有管理员会话。Linux 可直接对相同数据目录运行 `collection -set-admin-password`，或使用 `docker compose run --rm collection -set-admin-password`，从环境变量读取新密码。

## 配置

| 配置字段 | 环境变量 | 默认值 |
|---|---|---|
| `listen` | `ARCHIVE_LISTEN` | `127.0.0.1:8080` |
| `data_dir` | `ARCHIVE_DATA_DIR` | `./data` |
| `pixiv_dir` | `ARCHIVE_PIXIV_DIR` | 未设置 |
| `gallery_dl` | `ARCHIVE_GALLERY_DL` | `gallery-dl` |
| `ffmpeg` | `ARCHIVE_FFMPEG` | `ffmpeg` |
| `ffprobe` | `ARCHIVE_FFPROBE` | `ffprobe` |
| `cookies_file` | `ARCHIVE_COOKIES_FILE` | 未设置 |
| `proxy` | `ARCHIVE_PROXY` | 未设置 |

环境变量优先于配置文件。原文件保留原位置，缩略图与预览图按需生成。EPUB 提供信息展示和文件下载。
