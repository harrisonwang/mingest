# 常见问题

## Mingest 是什么？

Mingest 是一个本地运行的视频批量下载 CLI：输入 URL 或 URL 列表，调用 `yt-dlp` 下载，并默认合并为 `mp4`（嵌入封面与元数据）。

## 支持哪些网站？

站点/格式支持最终取决于 `yt-dlp`。Mingest 目前内置两类登录流程：

- `youtube`
- `bilibili`

## 为什么需要 `mingest auth login <platform>`？

很多内容在网页端能看，但下载时会要求账户登录信息（例如年龄确认、会员、风险提示）。`mingest auth login <platform>` 会打开一个工具专用的浏览器窗口，让你用自己的账号完成登录，然后把该站点的 cookies 保存到本机缓存，后续下载自动使用。你也可以用 `mingest auth status` 查看缓存状态，用 `mingest auth validate <platform>` 做本地登录信号验证。

## cookies 会上传到服务器吗？

不会。Mingest 全程本地运行，不提供在线解析/代下服务。cookies 只保存在你的电脑上（路径见 [README.md](../README.md)）。

## cookies 缓存会保存哪些站点的数据？

为减少隐私暴露，Mingest 会把 cookies 缓存过滤为与目标站点相关的域名（例如 `youtube.com`/`google.com` 或 `bilibili.com`）。

## Windows 上为什么经常看到 Chrome cookies 报错？

Windows + Chrome 可能因为数据库锁、DPAPI、App-Bound Encryption 等原因，导致第三方进程无法直接读取/解密浏览器 cookies。Mingest 会优先尝试常规读取，失败后自动走 CDP（由浏览器进程内导出明文 cookies），以降低失败率。

## 我能用 Mingest 下载付费/会员内容吗？

如果你在法律与平台规则允许范围内、且你拥有合法访问权限，Mingest 可以在「使用你自己的账户登录信息」的前提下完成下载。但 Mingest 不承诺任何站点长期可用，也不支持绕过 DRM 等技术保护措施。

## 什么是数字版权管理（DRM）？Mingest 支持吗？

数字版权管理（DRM）是内容方用于防复制的技术保护措施。Mingest 不支持绕过 DRM；遇到 DRM 保护内容，下载可能失败。

## 我需要安装哪些依赖？

Mingest 会自动寻找并调用：

- `yt-dlp`
- `ffmpeg`/`ffprobe`
- `node`
