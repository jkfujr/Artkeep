# 验证说明

## 当前命令

以下命令从仓库根目录执行。扩展使用浏览器原生模块：

```powershell
npm --prefix collector test
./tools/package-collector.ps1
```

浏览器检查使用匹配版本的 Edge WebDriver（本机 `127.0.0.1:9517`）和隔离 Artkeep 服务（默认 `127.0.0.1:28080`）。服务使用独立数据目录；服务器地址、测试密码由 `ARTKEEP_TEST_SERVER`、`ARTKEEP_TEST_PASSWORD` 指定。自己的测试密码从私有环境提供。

官方 PPD 19.6.1 的上游提交为 [`2e137d0b`](https://github.com/xuejianxianzun/PixivBatchDownloader/tree/2e137d0b45f4bc897f0792994dfa7176884922ce)。当前本机 PPD checkout 与该提交一致。下列命令仅准备浏览器测试输入，交付包只包含 Artkeep：

```powershell
git -C '../../参考资料/Fork/PixivBatchDownloader' archive HEAD:dist -o "$PWD/artifacts/ppd-normal.zip"
Expand-Archive -LiteralPath './artifacts/ppd-normal.zip' -DestinationPath './artifacts/ppd-normal' -Force
$artkeepSample = Read-Host '含 5 张 JPG 和 PPD JSON 的实际样例目录'
node collector/tests/browser-ppd.mjs $artkeepSample
./collector/tests/create-fixtures.ps1
node collector/tests/browser-pages.mjs
node collector/tests/browser-coexist.mjs
```

`browser-ppd.mjs` 自行启动隔离 Edge，加载官方 PPD 与 Artkeep，真实导入样例 JSON，再操作原 PPD 按钮。样例媒体写入扩展缓存后执行实际 API 上传和入库。Pixiv 收藏接口、X 页面使用受控响应，其他站点请求被终止。脚本保存会话 ID 到 `artifacts/webdriver/session.json`，供页面检查继续使用；检查完关闭对应会话与隔离服务。

后端与前端的现有验证命令如下。本次官方 PPD 兼容改动复用此前成功结果：

```powershell
npm --prefix frontend run build
New-Item -ItemType Directory -Path './backend/web/dist' -Force | Out-Null
Get-ChildItem -LiteralPath './artifacts/frontend' | Copy-Item -Destination './backend/web/dist' -Recurse -Force
go -C backend test ./...
go -C backend build -trimpath -ldflags '-s -w' -o ../artifacts/artkeep.exe ./cmd/collection
```

## 最近一次结果

- 日期：2026-11-16。
- 提交：Artkeep `ec68f33` 加当前工作区改动；官方 PPD 19.6.1／`2e137d0b`，PPD 工作区已恢复干净。
- 环境：Windows amd64、Go 1.26.2、Node.js 24.14.1、Edge／WebDriver 147.0.3912.98、FFmpeg／FFprobe。
- Collector：5 项测试通过；安装包为 `artifacts/artkeep-collector-0.1.1.zip`。设置页已移除 PPD 扩展 ID 和连接检查。
- 设置页：1246px 与 360px 视口无横向溢出；PPD 区域仅保留接管开关，已检查渲染结果。
- 官方 PPD：实际“开始下载”按钮转交 5 张图片、每次询问、实际上传、完成记录与原进度完成事件通过。PPD 页面媒体请求为 0；文件由 Artkeep 处理。
- 官方 PPD：高级命名字段 `{bmk}` 和目录经原“预览文件名”功能生成，逐文件核对任务路径与 PPD 下载记录一致；多图只抓取前 2 张、自动开始、原暂停／继续／取消按钮通过。
- 官方 PPD：Pixiv 页面刷新后查询同一收藏任务，任务数量保持 1；取消后状态为 `cancelled`。
- 下载后收藏：保存成功后才提交收藏，翻译标签与私密选项通过受控 Pixiv 响应检查；取消任务未新增收藏请求。
- PPD 旧桥接源码及测试已撤回。撤回后 TypeScript 编译和原保存错误处理的 11 项测试通过；编译产物恢复为官方提交内容。
- Edge 页面：关闭接管时官方 PPD 设置面板与 Artkeep 独立入口并存；X 已加载帖子多选、每次询问、原图和最高码率动画选择通过；合成 `.ugoira` 在详情页与全屏查看实际播放 MP4。
- 实际样例：`151961159`，标题 `Miyabi (Pool Idea)`，作者 `29959947 / Lilith-Abdl`，R-18。复用此前三种保存去向的 1 部作品、5 张图片、3 份附属文件结果；本次通过官方 PPD 再次上传，按来源 ID 去重。收藏库保留页序和原文件名映射。
- Go／前端：复用此前全部 Go 包、TypeScript、Vite、后端编译的成功结果；覆盖 `/api/v1`、OpenAPI、HTTP 语义、PATCH、R18、令牌、幂等、分块、重启续传、重试与 PPD 元数据归组。最后补充的作品分页及人工分级省略／清除断言已定向通过。前端已有 foliate-js glob 警告保留。
- 正式 `F:\artkeep` 实例保持原样。测试数据位于 `artifacts/acceptance-data/`，临时后端嵌入资源位于 `artifacts/backend-web/`。

已验证：上述本地测试、官方 PPD 19.6.1 与 Edge 受控页面检查。未验证：Chrome、真实登录站点的抓取及下载后收藏、实站快捷抓取入口、图像与小说混合任务、关闭整个浏览器后的恢复、Docker 与云端 CI；未部署、未发布。
