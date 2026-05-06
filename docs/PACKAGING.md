# 包管理器发布说明

本文件说明 `brew` 分发链路的维护方式。

## 目标

- macOS / Linux：通过 Homebrew 安装

## Homebrew 模板

- `.github/homebrew/formula.rb.tmpl`

Homebrew 采用 slim 包，并让 brew 管理运行依赖：

- `yt-dlp`
- `ffmpeg`（包含 `ffprobe`）
- `deno`

## 相关工作流

- `.github/workflows/build-and-release.yml`（发版后通知 tap 仓库）

触发方式：

- Homebrew：tag 发版后发送 `repository_dispatch` 到 `harrisonwang/homebrew-tap`

## 自动发布

Homebrew：

- `build-and-release.yml` 在 release job 成功后通知 `harrisonwang/homebrew-tap`
- tap 仓库读取本仓库 tag 下的 `.github/homebrew/formula.rb.tmpl`
- tap 仓库下载同一 release 的 `SHA256SUMS.txt`，渲染并提交 `Formula/mingest.rb`

Homebrew secrets：

- `HOMEBREW_TAP_TOKEN`
