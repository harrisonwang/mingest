# 常见问题

## Mingest 是什么？

Mingest 是一个本地运行的视频批量下载 CLI：输入 URL 或 URL 列表，调用 `yt-dlp` 下载，并默认合并为 `mp4`。

## 支持哪些网站？

站点和格式支持最终取决于 `yt-dlp`。Mingest 目前内置两类登录流程：

- `youtube`
- `bilibili`

## `slim` 和 `bundled` 有什么区别？

- `slim` 只包含 Mingest 主程序，需要系统或同目录中已有 `yt-dlp`、`ffmpeg`/`ffprobe`、`node`
- `bundled` 随包附带这些工具，解压后即可使用，体积更大

Homebrew 使用 `slim`，并通过包管理器安装依赖。

## bundled 包为什么要带许可证文件？

因为 bundled 包分发了第三方二进制工具。即使这些工具只是放在同一目录里、由 Mingest 调用，也需要保留它们的版权归属、许可证说明和源码获取方式。详见 [../THIRD_PARTY_LICENSES](../THIRD_PARTY_LICENSES)。

## Mingest 怎么查找依赖？

`yt-dlp`、`ffmpeg`、`ffprobe`、`node` 按以下顺序查找：

1. 当前工作目录
2. Mingest 程序所在目录
3. 可选内嵌工具（仅限 `-tags embedtools` 构建，默认发布包不使用）
4. 系统 `PATH`

## 为什么需要 `mingest auth login <platform>`？

很多内容在网页端能看，但下载时会要求账户登录信息，例如年龄确认、会员、风险提示。`mingest auth login <platform>` 会打开浏览器窗口，让你用自己的账号完成登录，然后把该站点的 cookies 保存到本机缓存，后续下载自动使用。

你也可以用：

```bash
mingest auth status
mingest auth validate <platform>
```

## `auth login` 会打开哪个浏览器？

默认打开 Chrome。你可以显式指定：

```bash
mingest auth login youtube --from-browser firefox
```

也可以用环境变量：

```bash
BROWSER=firefox mingest auth login youtube
```

Windows 下会自动查找 Chrome / Firefox 的标准安装路径。`CHROME_PATH` / `FIREFOX_PATH` 只用于便携版或非标准安装位置。

## cookies 会上传到服务器吗？

不会。Mingest 全程本地运行，不提供在线解析/代下服务。cookies 只保存在你的电脑上。

## cookies 缓存会保存哪些站点的数据？

为减少隐私暴露，Mingest 会把 cookies 缓存过滤为与目标站点相关的域名，例如 `youtube.com` / `google.com` 或 `bilibili.com`。

## Windows 上为什么会看到 Chrome cookies 报错？

Windows + Chrome 可能因为数据库锁、DPAPI、App-Bound Encryption 等原因，导致第三方进程无法直接读取或解密浏览器 cookies。Mingest 会优先尝试常规读取，失败后自动走 CDP，由浏览器进程内导出 cookies。

## Firefox 登录需要注意什么？

如果使用日常 Firefox profile，最好在 Firefox 中登录并完成目标站点的额外确认后关闭浏览器，再运行 Mingest。这样可以避免 `cookies.sqlite` 被占用或 cookies 尚未落盘。

如果有多个 Firefox profile，可以指定：

```bash
BROWSER=firefox BROWSER_PROFILE=xxxx.default-release mingest get "<url>"
```

## 我能用 Mingest 下载付费/会员内容吗？

如果你在法律与平台规则允许范围内、且你拥有合法访问权限，Mingest 可以在使用你自己的账户登录信息的前提下完成下载。但 Mingest 不承诺任何站点长期可用，也不支持绕过 DRM 等技术保护措施。

## 什么是 DRM？Mingest 支持吗？

DRM 是内容方用于防复制的技术保护措施。Mingest 不支持绕过 DRM；遇到 DRM 保护内容，下载可能失败。
