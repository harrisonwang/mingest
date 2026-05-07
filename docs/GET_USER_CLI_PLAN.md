# 面向 get 用户的命令边界

## 当前阶段

面向先有 URL 再做本地归档的用户，当前公开命令只保留下载、下载历史查看与登录准备：

### `mingest get <url> [--out-dir <dir>] [--name-template <tpl>] [--asset-id-only] [--json]`

- `<url>`：单条视频 URL。
- `--out-dir <dir>`：下载目录；未指定时使用当前工作目录。
- `--name-template <tpl>`：`yt-dlp` 输出模板；未指定时默认 `%(title)s.%(ext)s`。
- `--asset-id-only`：仅输出下载结果的 `asset_id`。
- `--json`：输出结构化结果。

### `mingest ls [--limit <n>] [--query <text>] [--format <table|json>] [--dedupe]`

- `--limit <n>`：最多返回条目数，默认 `20`。
- `--query <text>`：按 `asset_id`、来源 URL、标题、输出路径或平台过滤。
- `--format <table|json>`：输出格式，默认 `table`。
- `--dedupe`：按 `asset_id` 去重，仅保留最新一条记录。

### `mingest auth <platform>`

- `<platform>`：当前支持 `youtube`、`bilibili`。
- 用于准备工具专用登录信息并写入本机 cookies 缓存。

### `mingest -V|--version`

- 输出当前版本。

## 延后能力

- 批量下载。
- 更细的登录浏览器/profile 参数。
- 高级媒体处理链路。
