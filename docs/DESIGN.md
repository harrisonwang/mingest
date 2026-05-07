# Mingest 设计文档

## 1. 当前边界

`mingest` 是一个本地运行的视频批量下载 CLI。当前公开命令只保留：

- `mingest get <url> [--output-dir <dir>] [--name-template <tpl>] [--asset-id-only] [--json]`
- `mingest get --batch <file> [--continue-on-error] [--jsonl]`
- `mingest get --failed-only <result.jsonl> [--continue-on-error] [--jsonl]`
- `mingest ls [--limit <n>] [--query <text>] [--failed] [--missing] [--format <table|json>]`
- `mingest auth login|status|validate|clear|list`
- `mingest -V|--version`

核心目标是把视频 URL 或 URL 列表下载为本地文件，并把成功/失败都记录为可脚本处理的 JSONL。遇到登录状态/平台风控问题时，CLI 需要给出错误码、恢复建议和可执行的下一步命令。

## 2. 目标

- 输入 URL，调用 `yt-dlp` 下载视频。
- 支持 URL 文件批量下载、失败继续、只重跑失败任务。
- 默认合并为 `mp4`，并嵌入封面与元数据。
- 在每次任务后生成 `task_id` 结果记录；下载成功后额外生成 `asset_id`。
- 允许用户查看、过滤最近成功/失败/文件缺失的视频记录。
- 优先使用平台 cookies 缓存；缓存不可用时尝试浏览器 cookies fallback。
- Windows Chrome cookies 读取受限时，通过 `auth` 的 CDP 流程准备工具专用登录信息。

## 3. 非目标

- 不提供在线解析或代下服务。
- 不绕过 DRM 或平台技术保护措施。
- 不承诺自动剪辑、质量判断或剪辑软件交付包。
- 不提供完整素材资产管理能力；下载记录只服务失败恢复、查询和自动化接入。

## 4. 核心流程

```text
auth login/status/validate/clear -> cookies cache
get  -> dependency check -> platform cookies -> yt-dlp -> result record -> local file/asset_id
batch/failed-only -> run get per URL -> JSONL result stream
ls   -> result records -> filter/sort/dedupe -> table/json
```

## 5. 关键模块

- CLI 入口：参数解析、用法输出、退出码。
- 依赖探测：查找 `yt-dlp`、`ffmpeg`、`ffprobe`、`node`。
- 平台识别：YouTube 与 Bilibili 的 URL、登录页、登录信号。
- cookies 管理：平台级缓存、浏览器 fallback、CDP 导出。
- 下载执行：封装 `yt-dlp`，捕获最终输出路径并渲染进度。
- 结果记录：保存 `results-v1.jsonl`，成功/失败都保留 `task_id`、`error_code`、`exit_code`、`recovery_hint`、`recommended_command`。
- 下载历史查询：读取本机结果记录，支持成功/失败/缺失文件、平台、关键字、去重和 JSON 输出。

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
