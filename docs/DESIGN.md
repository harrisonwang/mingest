# Mingest 设计文档（v1）

## 1. 文档信息
- 项目：`mingest`
- 版本范围：截至 2026-02-25 的已实现能力
- 文档目标：用一份简明设计说明当前产品边界、核心架构与关键流程

## 2. 产品定义
`mingest` 是一个面向“先拿 URL 再进入剪辑流程”的创作者工具链 CLI，定位是：

- 输入：YouTube / B 站视频 URL（及后续本地素材引用）
- 输出：可复用、可追踪、可导入剪辑软件的交付资产
- 核心价值：减少“下载、找素材、找字幕、做交接包”的重复人工操作

## 3. 目标与非目标
### 3.1 目标
- 把 `URL -> 下载 -> 索引 -> 预处理 -> 导出` 串成稳定流水线。
- 提供脚本友好的结构化输出（`--json`、`asset_id`）。
- 优先支持专业剪辑链路（Premiere / Resolve），兼容剪映基础导入。

### 3.2 非目标
- 不做在线 SaaS。
- 不做完整 DAM/MAM（全盘素材资产管理平台）。
- 不做“一键自动成片”与完整 NLE 工程自动化（如 Resolve `.drp` 级别编排）。

## 4. 目标用户与核心痛点
### 4.1 目标用户
- 使用 `get` 拉取素材的内容创作者、代剪、剪辑协作团队。
- 以桌面剪辑流程为主（Premiere / Resolve），同时覆盖部分剪映用户。

### 4.2 核心痛点
- 下载后素材散落，后续难检索与复用。
- 字幕来源不稳定，手工转写/校对耗时。
- 与剪辑师交接时格式不统一，重复制作标记与清单。

## 5. 命令边界（当前实现）
- `mingest auth <platform>`
- `mingest get <url> [--out-dir] [--name-template] [--asset-id-only] [--json]`
- `mingest ls [--limit] [--query] [--format table|json] [--dedupe]`
- `mingest prep <asset_ref> --goal <subtitle|highlights|shorts> [...] [--json]`
- `mingest export <asset_ref> --to <premiere|resolve|capcut> [--with ...] [--out-dir] [--zip] [--json]`

说明：`asset_ref` 支持 `asset_id` 与本地文件路径。

## 6. 系统架构
### 6.1 架构概览
```text
auth -> get -> assets index -> prep bundle -> export package -> NLE import
```

### 6.2 关键模块
- 依赖与执行层：`yt-dlp`、`ffmpeg/ffprobe`、`deno|node`、可选 `whisper`。
- 鉴权层：cookies 缓存优先，必要时浏览器导出；`auth` 使用 Chrome CDP 专用 profile。
- 资产层：`assets-v1.jsonl` 记录下载资产索引。
- 预处理层：生成 `prep-plan.json`、`markers.csv`、字幕与模板字幕。
- 导出层：按目标剪辑软件输出 `fcpxml/srt/csv/edl` 与可选 zip。

## 7. 数据模型与目录规范
### 7.1 资产索引
- 文件：`<appStateDir>/assets-v1.jsonl`
- 单条记录字段：`asset_id/url/platform/title/output_path/created_at`
- 作用：`ls` 查询、`prep/export` 通过 `asset_id` 反查素材

### 7.2 `asset_id` 生成
- 方案：`size + 文件头1MB + 文件尾1MB` 做哈希摘要，前缀 `ast_`
- 目的：快速稳定标识资产，支撑脚本串联

### 7.3 预处理产物（按素材目录）
- `.../.mingest/prep/<asset_id>/<timestamp>/prep-plan.json`
- `.../.mingest/prep/<asset_id>/<timestamp>/markers.csv`
- `.../.mingest/prep/<asset_id>/<timestamp>/subtitle.srt`（如有）
- `.../.mingest/prep/<asset_id>/<timestamp>/subtitle-template.srt`

### 7.4 导出产物（按素材目录）
- `.../.mingest/export/<asset_id>/<timestamp>/...`
- 可选：同名 `.zip`

## 8. 核心流程设计
### 8.1 `get` 流程
1. 校验 URL 与依赖。
2. 根据平台决定 cookies 策略并调用 `yt-dlp` 下载。
3. 解析输出路径，计算 `asset_id`，写入索引。
4. 输出人读日志或 JSON（支持仅输出 `asset_id`）。

### 8.2 `prep` 流程
1. 通过 `asset_ref` 定位素材（本地路径或索引反查）。
2. `ffprobe` 提取媒体元数据。
3. 生成候选 clips（均匀采样策略）。
4. 字幕策略按优先级执行：
   `platform_manual -> platform_auto -> whisper`
5. 对字幕做质量评分，达阈值才接受。
6. 写入 `prep-plan.json`、`markers.csv`、`subtitle-template.srt` 与可用字幕。

### 8.3 `export` 流程
1. 定位最新可用 `prep` bundle。
2. 按目标与格式导出：
   - `premiere/resolve` 默认 `fcpxml,srt`
   - `capcut` 默认 `srt,csv`
3. 可选打包 zip。
4. 输出导出清单（路径可直接用于导入）。

## 9. 对剪辑软件的实际衔接能力
### 9.1 Premiere / Resolve
- 已支持：导入 `fcpxml` 生成可编辑时间线，`srt` 单独导入字幕轨。
- 当前边界：字幕不会自动绑定到切段后的时间线轨道，需要在 NLE 内完成最后一步对齐/确认。

### 9.2 CapCut / 剪映
- 已支持：`srt + csv` 及导入说明文件。
- 当前边界：以“辅助导入与人工切片参考”为主，不是工程级自动编排。

## 10. 错误处理与可观测性
- 统一退出码：鉴权问题、依赖缺失、下载失败等可机器识别。
- 支持 `--json` 便于外部 Agent / 脚本做编排与重试。
- `get` 已提供下载进度显示，提升长任务可感知性。

## 11. 设计取舍
- 取舍 1：优先“可交付”而非“全自动创作”，先覆盖高频刚需。
- 取舍 2：先做本地文件产物与标准交换格式，降低平台与 API 依赖。
- 取舍 3：优先命令稳定与可脚本化，延后复杂 Agent 编排框架。

## 12. 已知限制
- 当前 `auth` 不支持 `--json`。
- `get` 暂未实现批量下载子命令。
- clips 生成是规则法（均匀采样），不等于“智能精彩片段”。
- 导出侧尚未实现“字幕按切段自动重映射”。

## 13. 下一步建议（P1）
- `export` 增加字幕重映射模式（基于 clips 重写时间码）。
- 提供目标 NLE 的“导入校验命令”（检查路径、时长、字幕覆盖率）。
- 引入轻量批量下载（文件输入 + 并发 + 重试），保持 CLI 语义一致。
