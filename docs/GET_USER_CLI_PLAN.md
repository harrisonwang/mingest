# 面向 get 用户的命令边界

## 当前阶段

面向先有 URL 再做本地下载的用户，当前公开命令只保留下载、批量失败恢复、下载历史查看与登录准备：

### `mingest get <url> [--output-dir <dir>] [--name-template <tpl>] [--asset-id-only] [--json]`

- `<url>`：单条视频 URL。
- `--output-dir <dir>`：下载目录；未指定时使用当前工作目录。
- `--name-template <tpl>`：`yt-dlp` 输出模板；未指定时默认 `%(title)s.%(ext)s`。
- `--asset-id-only`：仅输出下载结果的 `asset_id`。
- `--json`：输出结构化结果。

### `mingest get --batch <file> [--continue-on-error] [--jsonl]`

- `--batch <file>`：从 URL 文件读取任务，每行一个 URL。
- `--continue-on-error`：遇到失败继续处理下一条。
- `--jsonl`：每个任务输出一行结构化结果。

### `mingest get --failed-only <result.jsonl> [--continue-on-error] [--jsonl]`

- `--failed-only <result.jsonl>`：只重跑此前 JSONL 结果里的失败任务。

### `mingest ls [--limit <n>] [--query <text>] [--failed] [--missing] [--format <table|json>] [--dedupe]`

- `--limit <n>`：最多返回条目数，默认 `50`。
- `--query <text>`：按 `task_id`、`asset_id`、来源 URL、标题、输出路径、错误码或平台过滤。
- `--failed`：只看失败记录。
- `--missing`：只看成功记录里本地文件已经缺失的条目。
- `--format <table|json>`：输出格式，默认 `table`。
- `--dedupe`：按 `task_id` 去重，仅保留最新一条记录。

### `mingest auth login <platform>`

- `<platform>`：当前支持 `youtube`、`bilibili`。
- 用于准备工具专用登录信息并写入本机 cookies 缓存。

### `mingest auth status|validate|clear|list`

- `status [platform]`：查看本地 cookies 缓存和登录信号。
- `validate <platform>`：验证本地登录信号；配合 `--url` 可做联网探测。
- `clear <platform>`：清理平台 cookies 缓存。
- `list`：列出支持平台和状态。

### `mingest -V|--version`

- 输出当前版本。

## 延后能力

- 更细的登录浏览器/profile 参数。
- 高级媒体处理链路。
