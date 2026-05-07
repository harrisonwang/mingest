# Mingest 设计文档

## 1. 当前边界

`mingest` 是一个本地运行的视频归档 CLI。当前公开命令只保留：

- `mingest get <url> [--out-dir <dir>] [--name-template <tpl>] [--asset-id-only] [--json]`
- `mingest ls [--limit <n>] [--query <text>] [--format <table|json>] [--dedupe]`
- `mingest auth <platform>`
- `mingest -V|--version`

核心目标是把单条视频 URL 稳定下载为本地文件，记录本机下载历史，并在需要登录的平台上提供本机 cookies 准备流程。

## 2. 目标

- 输入 URL，调用 `yt-dlp` 下载视频。
- 默认合并为 `mp4`，并嵌入封面与元数据。
- 在下载成功后生成 `asset_id`，便于脚本使用。
- 允许用户查看、过滤最近下载过的视频记录。
- 优先使用平台 cookies 缓存；缓存不可用时尝试浏览器 cookies fallback。
- Windows Chrome cookies 读取受限时，通过 `auth` 的 CDP 流程准备工具专用登录信息。

## 3. 非目标

- 不提供在线解析或代下服务。
- 不绕过 DRM 或平台技术保护措施。
- 不承诺自动剪辑、质量判断或剪辑软件交付包。
- 不提供完整素材资产管理能力；下载记录只覆盖本机成功下载结果。

## 4. 核心流程

```text
auth -> cookies cache
get  -> dependency check -> platform cookies -> yt-dlp -> local file -> asset_id
ls   -> asset records -> filter/sort/dedupe -> table/json
```

## 5. 关键模块

- CLI 入口：参数解析、用法输出、退出码。
- 依赖探测：查找 `yt-dlp`、`ffmpeg`、`ffprobe`、`node`。
- 平台识别：YouTube 与 Bilibili 的 URL、登录页、登录信号。
- cookies 管理：平台级缓存、浏览器 fallback、CDP 导出。
- 下载执行：封装 `yt-dlp`，捕获最终输出路径并渲染进度。
- 结果记录：计算并保存 `asset_id` 记录，供 `--asset-id-only` 与 `--json` 输出。
- 下载历史查询：读取本机 `assets-v1.jsonl`，支持限制数量、关键字过滤、去重和 JSON 输出。

## 6. 退出码

- `0`：成功
- `2`：用法错误
- `20`：需要登录
- `21`：cookies 读取或解密问题
- `30`：JS runtime 缺失
- `31`：`ffmpeg` 或 `ffprobe` 缺失
- `32`：`yt-dlp` 缺失
- `40`：下载失败

## 7. 取舍

- 保持命令面小而稳定，优先保证下载与登录链路可靠。
- 继续保留结构化输出，方便外部脚本组合。
- 高级媒体处理能力不再作为当前 CLI 的公开承诺。
