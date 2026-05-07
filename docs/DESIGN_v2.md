# Mingest 设计文档 v2 状态

本文件保留为历史路线记录。当前实现已经回到更小的公开边界：本地下载、登录信息准备、结构化下载结果输出。

公开命令：

- `mingest get <url> [--out-dir <dir>] [--name-template <tpl>] [--asset-id-only] [--json]`
- `mingest ls [--limit <n>] [--query <text>] [--format <table|json>] [--dedupe]`
- `mingest auth <platform>`
- `mingest -V|--version`

当前支持面是本地下载、登录信息准备、下载历史查看和结构化下载结果输出。高级媒体处理链路不再作为 CLI 支持面；后续如重新评估相关能力，应先更新本文档与 README，再恢复代码入口。
