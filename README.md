# Mingest

![og-image](og-image.png)

**Mingest 是一个本地运行的视频批量下载 CLI**：输入 URL 或 URL 列表，调用 `yt-dlp` 下载，并默认合并为 `mp4`。下载失败时会留下结构化记录、错误码和恢复建议，方便只重跑失败任务。

> 合规提示：Mingest 仅用于下载你拥有版权或已获授权、或在法律与平台规则允许范围内可保存的内容。它不提供内容、不提供在线解析/代下服务，也不支持绕过 DRM 等技术保护措施。更多见 [docs/LEGAL.md](docs/LEGAL.md)。

## 快速开始

下载视频：

```bash
mingest get "<url>"
```

需要登录时先执行一次交互登录：

```bash
mingest auth login youtube
mingest auth login bilibili
```

支持的平台：

- `youtube`
- `bilibili`

站点和格式支持最终取决于 `yt-dlp` 本身。

## 安装

推荐优先使用包管理器安装：

```bash
brew tap harrisonwang/tap
brew install mingest
```

也可以直接下载 GitHub Release 产物：

- `*_slim`：只包含 Mingest，需要系统或同目录中已有 `yt-dlp`、`ffmpeg`/`ffprobe`、`node`
- `*_bundled`：随包附带 `yt-dlp`、`ffmpeg`/`ffprobe`、`node`，解压后可直接使用

`*_bundled` 中的第三方二进制版权和许可证见 [THIRD_PARTY_LICENSES](THIRD_PARTY_LICENSES)。

## 常用命令

下载：

```bash
mingest get "<url>"
mingest get --batch urls.txt --continue-on-error --jsonl
mingest get --failed-only result.jsonl --jsonl
```

查看下载历史：

```bash
mingest ls --limit 50
mingest ls --failed
mingest ls --missing
```

管理登录信息：

```bash
mingest auth login <platform>
mingest auth status
mingest auth validate youtube
mingest auth clear youtube
```

查看版本：

```bash
mingest -V
```

## 登录与浏览器

Mingest 会把登录 cookies 缓存在本机。默认优先使用缓存，缓存失效时再从浏览器读取。

`auth login` 默认使用 Chrome；也可以指定浏览器：

```bash
mingest auth login youtube --from-browser firefox
```

或通过环境变量指定：

```bash
BROWSER=firefox mingest auth login youtube
BROWSER=firefox mingest get "<url>"
```

可用环境变量：

- `BROWSER=chrome|firefox|chromium|edge`
- `BROWSER_PROFILE=Default|Profile 1|...`
- `CHROME_PATH=C:\\Path\\To\\chrome.exe`
- `FIREFOX_PATH=C:\\Path\\To\\firefox.exe`
- `LOG_LEVEL=debug|info|warn|error`
- `LOG_FORMAT=text|json`

Windows 下 Chrome 或 Firefox 的标准安装路径会自动查找；`CHROME_PATH` / `FIREFOX_PATH` 只用于便携版或非标准安装位置。

## 本地数据

Mingest 全程本地运行，不提供在线解析/代下服务。视频文件、URL、cookies 不会上传到 Mingest 服务器。

常见本地文件：

- Windows cookies 缓存：`%LOCALAPPDATA%\\mingest\\youtube-cookies.txt`
- macOS / Linux cookies 缓存：`os.UserConfigDir()/mingest/`
- 下载结果记录：`results-v1.jsonl`

更多见 [docs/PRIVACY.md](docs/PRIVACY.md)。

## 文档

- [docs/FAQ.md](docs/FAQ.md)：常见问题、浏览器登录、依赖查找
- [docs/LEGAL.md](docs/LEGAL.md)：使用边界、DRM、平台规则
- [docs/PRIVACY.md](docs/PRIVACY.md)：本地数据和隐私说明
- [THIRD_PARTY_LICENSES](THIRD_PARTY_LICENSES)：bundled 包第三方组件许可证

## 许可证

Mingest 采用 [GNU Affero General Public License v3.0 (AGPL-3.0)](LICENSE) 开源协议。  
Copyright (C) 2026 Harrison Wang <https://mingest.com>
