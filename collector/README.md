# Artkeep Collector

独立的 Chrome／Edge 扩展，要求 Chromium 124 或更新版本。支持 Pixiv 图片、漫画、动图，以及 X 图片、视频、动画和已加载帖子的多选收藏。

## 安装与配置

从仓库根目录打包：

```powershell
./tools/package-collector.ps1
```

输出为 `artifacts/collector/` 和 `artifacts/artkeep-collector-<版本>.zip`。

1. 在 `chrome://extensions` 或 `edge://extensions` 开启开发者模式，加载 `artifacts/collector/`；ZIP 包解压后加载其中目录。
2. 打开扩展“设置”，复制扩展来源；在 Artkeep 管理页“访问令牌”创建对应令牌。
3. 在扩展设置中填写服务器地址和令牌，允许访问该服务器，测试连接并保存。
4. 保存去向可选 Artkeep、本地、本地与 Artkeep、每次询问；每次询问在任务页为整批选择。
5. 通过“导入 PPD 配置”读取已有配置，保存后生效。命名、并发、下载间隔和下载后收藏设置被读取。

独立入口默认 Pixiv 命名为 `pixiv/{user_id}-{user}/{pid}-{title}/{id}`，默认 X 命名为 `x/{user_id}-{user}/{pid}/{id}`，支持这五种命名字段。PPD 接管直接使用 PPD 预览生成的完整文件名，保留其命名规则与目录。Pixiv 每部作品保存 JSON、TXT 元数据和简介；动图保存 `.ugoira`。

网页提供单作品“收藏”按钮和“选择已加载作品”。X 帖子数据未取得时重新加载原帖。纯文字帖、直播、外部网站视频、小说和未加载列表的自动遍历留待后续。

## PPD 接管

1. 保留官方 [Powerful Pixiv Downloader](https://github.com/xuejianxianzun/PixivBatchDownloader)，当前已检查 19.6.1。
2. 在 Artkeep 设置中开启“接管 PPD 下载”，保存后重新加载 Pixiv 页面。
3. 沿用 PPD 的抓取、筛选及开始下载操作。Artkeep 接收 PPD 已保存的实际结果列表，包括多图选页；原暂停、继续和放弃按钮控制对应收藏任务。

兼容代码全部位于 Artkeep 插件。检测和接管通过 PPD 页面事件、站点 IndexedDB 及原操作按钮完成。PPD 扩展保持官方版本，由 Artkeep 自动检测。

文件保存由 Artkeep 执行，所选目标全部成功后写入 PPD 下载记录并推进其进度；PPD 面板显示 Artkeep 保存状态。开启 PPD 下载后收藏时，Artkeep 读取 PPD 的标签、翻译标签及公开范围选项，完成保存后执行收藏。小说由 PPD 原流程处理。

保持 Pixiv 页面打开可更新 PPD 进度；文件传输在 Artkeep 后台运行，页面重新加载后继续查询原收藏任务。关闭“接管 PPD 下载”时，官方 PPD 和 Artkeep 独立入口分别工作。PPD 后续版本的数据结构变化会显示错误，可关闭接管后使用独立入口。

## 任务与恢复

任务页提供暂停、继续、取消、重试和打开收藏。下载内容保存于扩展私有存储，完成后清理临时媒体；本地和上传分别记录结果。双目标重试保留已完成的一侧。

服务端上传使用 [统一 API](../backend/internal/server/openapi.json)，按偏移续传；未知写入结果先查询原任务。来源 URL 失效时重新打开来源页面后重新收藏。访问令牌撤销后，相关任务停止并显示错误。

## 开发与验证

从仓库根目录执行：

```powershell
npm --prefix collector test
./tools/package-collector.ps1
```

扩展使用浏览器原生模块，构建为复制与打包，无运行时依赖。版本和固定身份仅在 `manifest.json` 维护，`extension-id.txt` 供安装和验证使用。

浏览器验收脚本使用本机隔离 WebDriver 和隔离 Artkeep 服务，`ARTKEEP_TEST_SERVER`、`ARTKEEP_TEST_PASSWORD` 指定服务及测试密码。`tests/browser-ppd.mjs` 加载官方 PPD，检查实际下载入口转交、保存、记录和控制操作；`tests/browser-pages.mjs` 检查 X 多选与动图播放，`tests/browser-coexist.mjs` 检查关闭接管后的并存。准备方式、测试结果与实站验收范围见 [验证说明](../docs/VALIDATION.md)。
