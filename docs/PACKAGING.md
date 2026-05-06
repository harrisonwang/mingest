# 包管理器发布说明

本文件说明 `brew` 分发链路的维护方式。

## 目标

- macOS / Linux：通过 Homebrew 安装

## 相关脚本

- `scripts/generate-homebrew-formula.sh`

它们都以 `SHA256SUMS.txt` 作为单一校验数据来源，避免手工复制 hash。Homebrew 的自动发布由 `harrisonwang/homebrew-tap` 统一渲染，本仓库脚本主要用于本地校验和手动兜底。

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

## 手动生成（本地）

Homebrew Formula：

```bash
scripts/generate-homebrew-formula.sh \
  --tag v0.4.2 \
  --repo harrisonwang/mingest \
  --checksums artifacts/SHA256SUMS.txt \
  --output out/mingest.rb
```

## 自动发布

Homebrew：

- `build-and-release.yml` 在 release job 成功后通知 `harrisonwang/homebrew-tap`
- tap 仓库读取本仓库 tag 下的 `.github/homebrew/formula.rb.tmpl`
- tap 仓库下载同一 release 的 `SHA256SUMS.txt`，渲染并提交 `Formula/mingest.rb`

Homebrew secrets：

- `HOMEBREW_TAP_TOKEN`
