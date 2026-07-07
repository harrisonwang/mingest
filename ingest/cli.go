// mingest - Media Ingestion CLI tool
// Copyright (C) 2026  Harrison Wang <https://mingest.com>
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published
// by the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package ingest

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"mingest/ingest/embedtools"
	"mingest/ingest/platform/console"
)

const (
	exitOK             = 0
	exitUsage          = 2
	exitAuthRequired   = 20
	exitCookieProblem  = 21
	exitRuntimeMissing = 30
	exitFFmpegMissing  = 31
	exitYtDlpMissing   = 32
	exitDownloadFailed = 40
)

const (
	defaultYtDlpOutputTemplate = "%(title)s.%(ext)s"
	ytDlpPathMarker            = "__MINGEST_PATH__"
	ytDlpProgressMarker        = "__MINGEST_PROGRESS__"
)

const (
	errorAuthRequired   = "AUTH_REQUIRED"
	errorBotCheck       = "BOT_CHECK"
	errorCookieExpired  = "COOKIE_EXPIRED"
	errorCookieProblem  = "COOKIE_PROBLEM"
	errorRateLimited    = "RATE_LIMITED"
	errorFormatBlocked  = "FORMAT_BLOCKED"
	errorNetwork        = "NETWORK_ERROR"
	errorToolMissing    = "TOOL_MISSING"
	errorDownloadFailed = "DOWNLOAD_FAILED"
	errorUsage          = "USAGE_ERROR"
)

type tool struct {
	Name string
	Path string
}

type deps struct {
	YtDlp       tool
	FFmpeg      tool
	FFprobe     tool
	JSRuntime   tool
	JSRuntimeID string
}

type authKind string

const (
	authKindBrowser authKind = "browser"
)

type authSource struct {
	Kind  authKind
	Value string
}

type getOptions struct {
	TargetURL       string
	BatchFile       string
	FailedOnlyFile  string
	OutDir          string
	NameTemplate    string
	AssetIDOnly     bool
	JSON            bool
	JSONL           bool
	ContinueOnError bool
}

type lsOptions struct {
	Limit    int
	Query    string
	Format   string
	Dedupe   bool
	Failed   bool
	OK       bool
	Missing  bool
	Platform string
}

type authOptions struct {
	Action      string
	Platform    string
	FromBrowser string
	ValidateURL string
	JSON        bool
}

type resultRecord struct {
	SchemaVersion      int    `json:"schema_version"`
	TaskID             string `json:"task_id"`
	AssetID            string `json:"asset_id,omitempty"`
	SourceURL          string `json:"source_url"`
	Platform           string `json:"platform,omitempty"`
	Title              string `json:"title,omitempty"`
	FilePath           string `json:"file_path,omitempty"`
	OK                 bool   `json:"ok"`
	ErrorCode          string `json:"error_code,omitempty"`
	ExitCode           int    `json:"exit_code"`
	RecoveryHint       string `json:"recovery_hint,omitempty"`
	RecommendedCommand string `json:"recommended_command,omitempty"`
	CreatedAt          string `json:"created_at"`
	DownloadedAt       string `json:"downloaded_at,omitempty"`
	OutputDir          string `json:"out_dir,omitempty"`
	NameTemplate       string `json:"name_template,omitempty"`
}

type failureClassification struct {
	ExitCode           int
	ErrorCode          string
	RecoveryHint       string
	RecommendedCommand string
}

type authStatus struct {
	Platform      string `json:"platform"`
	CookiePath    string `json:"cookie_path,omitempty"`
	Cached        bool   `json:"cached"`
	Authenticated bool   `json:"authenticated"`
	UpdatedAt     string `json:"updated_at,omitempty"`
	Error         string `json:"error,omitempty"`
}

type ytDlpConfig struct {
	OutputTemplate   string
	CaptureMovedPath bool
	Quiet            bool
	ProgressOnly     bool
	ProbeOnly        bool
}

type streamOptions struct {
	HidePathMarker bool
	Progress       *progressRenderer
}

type progressRenderer struct {
	mu          sync.Mutex
	out         io.Writer
	tty         bool
	lastPercent int
	hadRender   bool
}

type lsJSONResult struct {
	Total int            `json:"total"`
	Count int            `json:"count"`
	Limit int            `json:"limit"`
	Items []resultRecord `json:"items"`
}

func Main(args []string) int {
	configureLogger()
	console.EnsureUTF8()
	defer embedtools.Cleanup()

	if len(args) == 1 {
		usage()
		return exitUsage
	}

	if len(args) == 2 && isHelpArg(args[1]) {
		usage()
		return exitOK
	}

	if len(args) == 2 && isVersionArg(args[1]) {
		printVersion()
		return exitOK
	}

	switch strings.ToLower(strings.TrimSpace(args[1])) {
	case "get":
		opts, err := parseGetOptions(args[2:])
		if err != nil {
			logError("cli.invalid_arguments", "command", "get", "error", err)
			usage()
			return exitUsage
		}
		return runGet(opts)
	case "ls":
		opts, err := parseLsOptions(args[2:])
		if err != nil {
			logError("cli.invalid_arguments", "command", "ls", "error", err)
			usage()
			return exitUsage
		}
		return runLs(opts)
	case "auth":
		opts, err := parseAuthOptions(args[2:])
		if err != nil {
			logError("cli.invalid_arguments", "command", "auth", "error", err)
			usage()
			return exitUsage
		}
		return runAuthCommand(opts)
	case "update":
		return runUpdate(args[2:])
	default:
		usage()
		return exitUsage
	}
}

func usage() {
	fmt.Println("用法:")
	fmt.Println("  mingest get <url> [--output-dir <dir>] [--name-template <tpl>] [--asset-id-only] [--json]")
	fmt.Println("  mingest get --batch <file> [--continue-on-error] [--jsonl]")
	fmt.Println("  mingest get --failed-only <result.jsonl> [--continue-on-error] [--jsonl]")
	fmt.Println("  mingest ls [--limit <n>] [--query <text>] [--failed] [--missing] [--format <table|json>]")
	fmt.Println("  mingest auth login <platform> [--from-browser <chrome|firefox>]")
	fmt.Println("  mingest auth status [platform] [--json]")
	fmt.Println("  mingest auth validate <platform> [--url <url>] [--json]")
	fmt.Println("  mingest auth clear <platform>")
	fmt.Println("  mingest auth list [--json]")
	fmt.Println("  mingest update [yt-dlp]")
	fmt.Println("  mingest -V|--version")
	fmt.Println()
	fmt.Println("get 参数:")
	fmt.Println("  --output-dir, -o <dir>     设置下载目录（默认当前工作目录）")
	fmt.Println("  --name-template <tpl>     设置输出模板（默认 %(title)s.%(ext)s）")
	fmt.Println("  --batch, -b <file>        从文件读取 URL（每行一个）")
	fmt.Println("  --failed-only <jsonl>     只重跑 JSONL 结果文件中的失败任务")
	fmt.Println("  --continue-on-error       批量模式遇到失败时继续下一条")
	fmt.Println("  --asset-id-only           仅输出 asset_id（便于脚本串联）")
	fmt.Println("  --json                    输出 JSON 结果")
	fmt.Println("  --jsonl                   批量模式每行输出一个 JSON 结果")
	fmt.Println()
	fmt.Println("ls 参数:")
	fmt.Println("  --limit, -n <n>           最多返回 n 条（默认 50）")
	fmt.Println("  --query <text>            关键字过滤（匹配 asset_id/url/title/path/platform）")
	fmt.Println("  --platform <platform>     按平台过滤")
	fmt.Println("  --failed                  仅显示失败记录")
	fmt.Println("  --ok                      仅显示成功记录")
	fmt.Println("  --missing                 仅显示记录存在但本地文件缺失的成功记录")
	fmt.Println("  --format <table|json>     输出格式（默认 table）")
	fmt.Println("  --dedupe                  按 asset_id 去重（仅保留最新一条）")
	fmt.Println()
	fmt.Println("auth 参数:")
	fmt.Println("  login <platform>          交互式登录并写入平台 cookies 缓存")
	fmt.Println("  status [platform]         查看 cookies 缓存与本地登录信号")
	fmt.Println("  validate <platform>       验证本地登录信号；配合 --url 可做联网探测")
	fmt.Println("  clear <platform>          删除平台 cookies 缓存")
	fmt.Println("  list                      列出支持平台与状态")
	fmt.Println("  --from-browser <browser>  auth login 的浏览器来源（当前支持 chrome/firefox）")
	fmt.Println("  --url <url>               auth validate 的联网探测 URL")
	fmt.Println("  --json                    auth 输出 JSON")
	fmt.Println()
	fmt.Println("平台:")
	fmt.Println("  - youtube")
	fmt.Println("  - bilibili")
	fmt.Println()
	fmt.Println("update 参数:")
	fmt.Println("  update [yt-dlp]           下载并更新 yt-dlp 到最新版（存放在应用状态目录，优先于内置版本）")
	fmt.Println()
	fmt.Println("行为:")
	fmt.Println("  - 自动检测并调用 yt-dlp / ffmpeg / ffprobe / node")
	fmt.Println("  - 优先使用已自更新的 yt-dlp；下载因 yt-dlp 陈旧而失败时自动更新并重试一次")
	fmt.Println("  - 自动维护 cookies 缓存（优先使用；必要时从浏览器读取 cookies 刷新账户登录信息）")
	fmt.Println("  - 若 Windows 下 Chrome cookies 读取/解密失败，可用 `mingest auth login <platform>` 准备账户登录信息")
	fmt.Println()
	fmt.Println("可选环境变量:")
	fmt.Println("  - BROWSER=chrome|firefox|chromium|edge")
	fmt.Println("  - BROWSER_PROFILE=Default|Profile 1|...")
	fmt.Println("  - CHROME_PATH=C:\\\\Path\\\\To\\\\chrome.exe")
	fmt.Println("  - FIREFOX_PATH=C:\\\\Path\\\\To\\\\firefox.exe")
	fmt.Println("  - LOG_LEVEL=debug|info|warn|error（默认 info）")
	fmt.Println("  - LOG_FORMAT=text|json（默认 text）")
	fmt.Println("  - MINGEST_NO_AUTO_UPDATE=1（关闭下载失败时自动更新 yt-dlp）")
	fmt.Println()
	fmt.Println("退出码:")
	fmt.Println("  - 20: 需要登录（AUTH_REQUIRED）")
	fmt.Println("  - 21: cookies 读取/解密问题（COOKIE_PROBLEM）")
	fmt.Println("  - 30: JS runtime 缺失（RUNTIME_MISSING）")
	fmt.Println("  - 31: ffmpeg 缺失（FFMPEG_MISSING）")
	fmt.Println("  - 32: yt-dlp 缺失（YTDLP_MISSING）")
	fmt.Println("  - 40: 下载失败（DOWNLOAD_FAILED）")
}

func isHelpArg(v string) bool {
	switch v {
	case "-h", "--help", "help":
		return true
	default:
		return false
	}
}

func isVersionArg(v string) bool {
	switch v {
	case "-V", "--version", "version":
		return true
	default:
		return false
	}
}

var version = "dev"

func printVersion() {
	fmt.Printf("mingest %s\n", strings.TrimSpace(version))
}

func parseGetOptions(args []string) (getOptions, error) {
	opts := getOptions{}
	var outDirProvided bool
	var nameTemplateProvided bool

	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		switch {
		case arg == "--asset-id-only":
			opts.AssetIDOnly = true
		case arg == "--json":
			opts.JSON = true
		case arg == "--jsonl":
			opts.JSONL = true
		case arg == "--continue-on-error":
			opts.ContinueOnError = true
		case arg == "--output-dir" || arg == "-o":
			if i+1 >= len(args) {
				return getOptions{}, fmt.Errorf("`%s` 缺少参数", arg)
			}
			i++
			opts.OutDir = strings.TrimSpace(args[i])
			outDirProvided = true
		case strings.HasPrefix(arg, "--output-dir="):
			opts.OutDir = strings.TrimSpace(strings.TrimPrefix(arg, "--output-dir="))
			outDirProvided = true
		case arg == "--batch" || arg == "-b":
			if i+1 >= len(args) {
				return getOptions{}, fmt.Errorf("`%s` 缺少参数", arg)
			}
			i++
			opts.BatchFile = strings.TrimSpace(args[i])
		case strings.HasPrefix(arg, "--batch="):
			opts.BatchFile = strings.TrimSpace(strings.TrimPrefix(arg, "--batch="))
		case arg == "--failed-only":
			if i+1 >= len(args) {
				return getOptions{}, fmt.Errorf("`--failed-only` 缺少参数")
			}
			i++
			opts.FailedOnlyFile = strings.TrimSpace(args[i])
		case strings.HasPrefix(arg, "--failed-only="):
			opts.FailedOnlyFile = strings.TrimSpace(strings.TrimPrefix(arg, "--failed-only="))
		case arg == "--name-template":
			if i+1 >= len(args) {
				return getOptions{}, fmt.Errorf("`--name-template` 缺少参数")
			}
			i++
			opts.NameTemplate = strings.TrimSpace(args[i])
			nameTemplateProvided = true
		case strings.HasPrefix(arg, "--name-template="):
			opts.NameTemplate = strings.TrimSpace(strings.TrimPrefix(arg, "--name-template="))
			nameTemplateProvided = true
		case strings.HasPrefix(arg, "-"):
			return getOptions{}, fmt.Errorf("不支持的参数: %s", arg)
		default:
			if opts.TargetURL != "" {
				return getOptions{}, fmt.Errorf("`mingest get` 仅支持一个 URL")
			}
			opts.TargetURL = arg
		}
	}

	inputs := 0
	if strings.TrimSpace(opts.TargetURL) != "" {
		inputs++
	}
	if strings.TrimSpace(opts.BatchFile) != "" {
		inputs++
	}
	if strings.TrimSpace(opts.FailedOnlyFile) != "" {
		inputs++
	}
	if inputs == 0 {
		return getOptions{}, fmt.Errorf("缺少输入。用法: mingest get <url> 或 mingest get --batch <file>")
	}
	if inputs > 1 {
		return getOptions{}, fmt.Errorf("`mingest get` 只能选择 URL、--batch 或 --failed-only 其中一种输入")
	}
	if opts.AssetIDOnly && opts.JSON {
		return getOptions{}, fmt.Errorf("`--asset-id-only` 与 `--json` 不能同时使用")
	}
	if opts.JSON && opts.JSONL {
		return getOptions{}, fmt.Errorf("`--json` 与 `--jsonl` 不能同时使用")
	}
	if opts.AssetIDOnly && (opts.BatchFile != "" || opts.FailedOnlyFile != "") {
		return getOptions{}, fmt.Errorf("`--asset-id-only` 仅支持单条 URL")
	}
	if opts.JSON && (opts.BatchFile != "" || opts.FailedOnlyFile != "") {
		return getOptions{}, fmt.Errorf("批量模式请使用 `--jsonl`，不要使用 `--json`")
	}
	if outDirProvided && strings.TrimSpace(opts.OutDir) == "" {
		return getOptions{}, fmt.Errorf("`--output-dir` 不能为空")
	}
	if strings.TrimSpace(opts.BatchFile) == "" && strings.TrimSpace(opts.FailedOnlyFile) == "" && opts.ContinueOnError {
		return getOptions{}, fmt.Errorf("`--continue-on-error` 仅支持批量输入")
	}
	if nameTemplateProvided && strings.TrimSpace(opts.NameTemplate) == "" {
		return getOptions{}, fmt.Errorf("`--name-template` 不能为空")
	}
	return opts, nil
}

func parseLsOptions(args []string) (lsOptions, error) {
	opts := lsOptions{
		Limit:  50,
		Format: "table",
	}

	var limitProvided bool
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		switch {
		case arg == "--dedupe":
			opts.Dedupe = true
		case arg == "--failed":
			opts.Failed = true
		case arg == "--ok":
			opts.OK = true
		case arg == "--missing":
			opts.Missing = true
		case arg == "--limit" || arg == "-n":
			if i+1 >= len(args) {
				return lsOptions{}, fmt.Errorf("`%s` 缺少参数", arg)
			}
			i++
			v := strings.TrimSpace(args[i])
			n, err := strconv.Atoi(v)
			if err != nil {
				return lsOptions{}, fmt.Errorf("`--limit` 必须是整数: %s", v)
			}
			opts.Limit = n
			limitProvided = true
		case strings.HasPrefix(arg, "--limit="):
			v := strings.TrimSpace(strings.TrimPrefix(arg, "--limit="))
			n, err := strconv.Atoi(v)
			if err != nil {
				return lsOptions{}, fmt.Errorf("`--limit` 必须是整数: %s", v)
			}
			opts.Limit = n
			limitProvided = true
		case arg == "--query" || arg == "-q":
			if i+1 >= len(args) {
				return lsOptions{}, fmt.Errorf("`%s` 缺少参数", arg)
			}
			i++
			opts.Query = strings.TrimSpace(args[i])
		case strings.HasPrefix(arg, "--query="):
			opts.Query = strings.TrimSpace(strings.TrimPrefix(arg, "--query="))
		case arg == "--platform" || arg == "-p":
			if i+1 >= len(args) {
				return lsOptions{}, fmt.Errorf("`%s` 缺少参数", arg)
			}
			i++
			opts.Platform = strings.ToLower(strings.TrimSpace(args[i]))
		case strings.HasPrefix(arg, "--platform="):
			opts.Platform = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(arg, "--platform=")))
		case arg == "--format" || arg == "-f":
			if i+1 >= len(args) {
				return lsOptions{}, fmt.Errorf("`%s` 缺少参数", arg)
			}
			i++
			opts.Format = strings.ToLower(strings.TrimSpace(args[i]))
		case strings.HasPrefix(arg, "--format="):
			opts.Format = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(arg, "--format=")))
		case strings.HasPrefix(arg, "-"):
			return lsOptions{}, fmt.Errorf("不支持的参数: %s", arg)
		default:
			return lsOptions{}, fmt.Errorf("不支持的位置参数: %s", arg)
		}
	}

	if opts.Format != "table" && opts.Format != "json" {
		return lsOptions{}, fmt.Errorf("`--format` 仅支持 table 或 json")
	}
	if opts.Failed && opts.OK {
		return lsOptions{}, fmt.Errorf("`--failed` 与 `--ok` 不能同时使用")
	}
	if limitProvided && opts.Limit <= 0 {
		return lsOptions{}, fmt.Errorf("`--limit` 必须大于 0")
	}
	return opts, nil
}

func parseAuthOptions(args []string) (authOptions, error) {
	if len(args) == 0 {
		return authOptions{}, fmt.Errorf("缺少 auth 子命令")
	}

	opts := authOptions{
		Action: strings.ToLower(strings.TrimSpace(args[0])),
	}
	if opts.Action == "" {
		return authOptions{}, fmt.Errorf("缺少 auth 子命令")
	}
	if opts.Action == "list" {
		opts.Action = "status"
	}

	for i := 1; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		switch {
		case arg == "--json":
			opts.JSON = true
		case arg == "--from-browser":
			if i+1 >= len(args) {
				return authOptions{}, fmt.Errorf("`--from-browser` 缺少参数")
			}
			i++
			opts.FromBrowser = strings.ToLower(strings.TrimSpace(args[i]))
		case strings.HasPrefix(arg, "--from-browser="):
			opts.FromBrowser = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(arg, "--from-browser=")))
		case arg == "--url":
			if i+1 >= len(args) {
				return authOptions{}, fmt.Errorf("`--url` 缺少参数")
			}
			i++
			opts.ValidateURL = strings.TrimSpace(args[i])
		case strings.HasPrefix(arg, "--url="):
			opts.ValidateURL = strings.TrimSpace(strings.TrimPrefix(arg, "--url="))
		case strings.HasPrefix(arg, "-"):
			return authOptions{}, fmt.Errorf("不支持的参数: %s", arg)
		default:
			if opts.Platform != "" {
				return authOptions{}, fmt.Errorf("auth %s 仅支持一个平台参数", opts.Action)
			}
			opts.Platform = strings.ToLower(arg)
		}
	}

	switch opts.Action {
	case "login":
		if opts.Platform == "" {
			return authOptions{}, fmt.Errorf("`mingest auth login` 缺少平台")
		}
		if opts.FromBrowser != "" && !isSupportedAuthLoginBrowser(opts.FromBrowser) {
			return authOptions{}, fmt.Errorf("`auth login` 当前仅支持 `--from-browser chrome|firefox`")
		}
		if opts.ValidateURL != "" {
			return authOptions{}, fmt.Errorf("`--url` 仅支持 auth validate")
		}
	case "status":
		if opts.FromBrowser != "" || opts.ValidateURL != "" {
			return authOptions{}, fmt.Errorf("auth status 不支持 --from-browser 或 --url")
		}
	case "validate":
		if opts.Platform == "" {
			return authOptions{}, fmt.Errorf("`mingest auth validate` 缺少平台")
		}
		if opts.FromBrowser != "" {
			return authOptions{}, fmt.Errorf("auth validate 不支持 --from-browser")
		}
	case "clear":
		if opts.Platform == "" {
			return authOptions{}, fmt.Errorf("`mingest auth clear` 缺少平台")
		}
		if opts.FromBrowser != "" || opts.ValidateURL != "" {
			return authOptions{}, fmt.Errorf("auth clear 不支持 --from-browser 或 --url")
		}
	default:
		return authOptions{}, fmt.Errorf("不支持的 auth 子命令: %s", opts.Action)
	}

	if opts.Platform != "" {
		if _, ok := platformByID(opts.Platform); !ok {
			return authOptions{}, fmt.Errorf("不支持的平台: %s", opts.Platform)
		}
	}
	return opts, nil
}

func runAuthCommand(opts authOptions) int {
	switch opts.Action {
	case "login":
		p, _ := platformByID(opts.Platform)
		return runAuthLogin(p, opts.FromBrowser)
	case "status":
		return runAuthStatus(opts)
	case "validate":
		return runAuthValidate(opts)
	case "clear":
		return runAuthClear(opts)
	default:
		logError("auth.unsupported_action", "action", opts.Action)
		return exitUsage
	}
}

func runAuthLogin(p videoPlatform, explicitBrowser string) int {
	browser := strings.ToLower(strings.TrimSpace(explicitBrowser))
	if browser == "" {
		browser = selectedBrowserEnv()
	}
	if browser == "" {
		browser = "chrome"
	}
	if !isSupportedAuthLoginBrowser(browser) {
		logError("auth.browser_unsupported", "browser", browser, "supported", "chrome,firefox")
		return exitUsage
	}

	switch browser {
	case "firefox":
		return runFirefoxAuth(p)
	default:
		return runChromeAuth(p)
	}
}

func runAuthStatus(opts authOptions) int {
	statuses := authStatuses(opts.Platform)
	if opts.JSON {
		data, err := json.Marshal(struct {
			Platforms []authStatus `json:"platforms"`
		}{Platforms: statuses})
		if err != nil {
			logError("json.marshal_failed", "context", "auth_status", "error", err)
			return exitDownloadFailed
		}
		fmt.Println(string(data))
		return exitOK
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "PLATFORM\tCACHED\tAUTH\tUPDATED_AT\tCOOKIE_PATH")
	for _, st := range statuses {
		_, _ = fmt.Fprintf(w, "%s\t%t\t%t\t%s\t%s\n",
			st.Platform,
			st.Cached,
			st.Authenticated,
			st.UpdatedAt,
			st.CookiePath,
		)
	}
	_ = w.Flush()
	return exitOK
}

func runAuthClear(opts authOptions) int {
	p, _ := platformByID(opts.Platform)
	path, err := cookiesCacheFilePath(p)
	if err != nil {
		logError("auth.cookie_cache_path_resolve_failed", "platform", p.ID, "error", err)
		return exitCookieProblem
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		logError("auth.cookie_cache_remove_failed", "platform", p.ID, "path", path, "error", err)
		return exitCookieProblem
	}
	logInfo("auth.cleared", "platform", p.ID, "path", path)
	return exitOK
}

func runAuthValidate(opts authOptions) int {
	p, _ := platformByID(opts.Platform)
	st := inspectAuthStatus(p)
	if opts.ValidateURL == "" {
		if opts.JSON {
			data, err := json.Marshal(st)
			if err != nil {
				logError("json.marshal_failed", "context", "auth_validate", "error", err)
				return exitDownloadFailed
			}
			fmt.Println(string(data))
		} else {
			fmt.Printf("platform=%s cached=%t authenticated=%t path=%s\n", st.Platform, st.Cached, st.Authenticated, st.CookiePath)
		}
		if st.Authenticated {
			return exitOK
		}
		return exitAuthRequired
	}

	if !st.Authenticated {
		if opts.JSON {
			data, _ := json.Marshal(st)
			fmt.Println(string(data))
		}
		logError("auth.validate_no_local_session", "platform", p.ID, "recommended_command", authLoginCommand(p))
		return exitAuthRequired
	}

	u, err := validateURL(opts.ValidateURL)
	if err != nil {
		logError("auth.validate_url_invalid", "url", opts.ValidateURL, "error", err)
		return exitUsage
	}
	if vp, ok := platformForURL(u); ok && vp.ID != p.ID {
		logError("auth.validate_platform_mismatch", "platform", p.ID, "url_platform", vp.ID)
		return exitUsage
	}

	found, err := detectDeps()
	if err != nil {
		var depErr dependencyError
		if errors.As(err, &depErr) {
			logError("deps.validation_failed", "exit_code", depErr.ExitCode, "detail", depErr.Message)
			return depErr.ExitCode
		}
		logError("deps.detect_failed", "error", err)
		return exitDownloadFailed
	}

	cfg := ytDlpConfig{Quiet: true, ProbeOnly: true}
	failure, _ := runYtDlp(found, buildYtDlpArgsWithCookiesFile(opts.ValidateURL, found, st.CookiePath, cfg), p, cfg)
	if opts.JSON {
		data, err := json.Marshal(struct {
			AuthStatus authStatus `json:"auth_status"`
			OK         bool       `json:"ok"`
			ExitCode   int        `json:"exit_code"`
			ErrorCode  string     `json:"error_code,omitempty"`
			Hint       string     `json:"recovery_hint,omitempty"`
			Command    string     `json:"recommended_command,omitempty"`
		}{
			AuthStatus: st,
			OK:         failure.ExitCode == exitOK,
			ExitCode:   failure.ExitCode,
			ErrorCode:  failure.ErrorCode,
			Hint:       failure.RecoveryHint,
			Command:    failure.RecommendedCommand,
		})
		if err != nil {
			logError("json.marshal_failed", "context", "auth_validate_probe", "error", err)
			return exitDownloadFailed
		}
		fmt.Println(string(data))
	}
	return failure.ExitCode
}

func authStatuses(platform string) []authStatus {
	if strings.TrimSpace(platform) != "" {
		p, _ := platformByID(platform)
		return []authStatus{inspectAuthStatus(p)}
	}
	platforms := supportedPlatforms()
	out := make([]authStatus, 0, len(platforms))
	for _, p := range platforms {
		out = append(out, inspectAuthStatus(p))
	}
	return out
}

func inspectAuthStatus(p videoPlatform) authStatus {
	st := authStatus{Platform: p.ID}
	path, err := cookiesCacheFilePath(p)
	if err != nil {
		st.Error = err.Error()
		return st
	}
	st.CookiePath = path
	info, err := os.Stat(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			st.Error = err.Error()
		}
		return st
	}
	if info.IsDir() {
		st.Error = "cookie path is a directory"
		return st
	}
	st.Cached = true
	st.UpdatedAt = info.ModTime().UTC().Format(time.RFC3339)
	ok, err := cookieFileLooksLikeAuthenticated(path, p)
	if err != nil {
		st.Error = err.Error()
		return st
	}
	st.Authenticated = ok
	return st
}

func runGet(opts getOptions) int {
	if opts.BatchFile != "" || opts.FailedOnlyFile != "" {
		return runGetMany(opts)
	}

	rec, code := runGetOne(opts)
	if opts.JSON {
		printResultJSON(rec)
	}
	if opts.AssetIDOnly && rec.OK {
		fmt.Println(rec.AssetID)
	}
	return code
}

func runGetMany(opts getOptions) int {
	urls, err := loadGetInputURLs(opts)
	if err != nil {
		logError("get.input_read_failed", "error", err)
		if opts.JSONL {
			rec := failureRecord("", videoPlatform{}, failureFromExit(exitUsage, err.Error(), ""))
			printResultJSON(rec)
		}
		return exitUsage
	}
	if len(urls) == 0 {
		logError("get.input_empty")
		if opts.JSONL {
			rec := failureRecord("", videoPlatform{}, failureFromExit(exitUsage, "输入文件没有可处理的 URL", ""))
			printResultJSON(rec)
		}
		return exitUsage
	}

	firstFailure := exitOK
	for _, rawURL := range urls {
		child := opts
		child.TargetURL = rawURL
		child.BatchFile = ""
		child.FailedOnlyFile = ""
		child.AssetIDOnly = false
		child.JSON = false

		rec, code := runGetOne(child)
		if opts.JSONL {
			printResultJSON(rec)
		}
		if code != exitOK {
			if firstFailure == exitOK {
				firstFailure = code
			}
			if !opts.ContinueOnError {
				return code
			}
		}
	}
	return firstFailure
}

func loadGetInputURLs(opts getOptions) ([]string, error) {
	if opts.BatchFile != "" {
		return readURLListFile(opts.BatchFile)
	}
	if opts.FailedOnlyFile != "" {
		return readFailedOnlyURLs(opts.FailedOnlyFile)
	}
	if opts.TargetURL != "" {
		return []string{opts.TargetURL}, nil
	}
	return nil, fmt.Errorf("缺少输入")
}

func readURLListFile(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func readFailedOnlyURLs(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	type failedLine struct {
		OK        bool   `json:"ok"`
		SourceURL string `json:"source_url"`
		URL       string `json:"url"`
	}

	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec failedLine
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if rec.OK {
			continue
		}
		rawURL := strings.TrimSpace(rec.SourceURL)
		if rawURL == "" {
			rawURL = strings.TrimSpace(rec.URL)
		}
		if rawURL != "" {
			out = append(out, rawURL)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func runGetOne(opts getOptions) (resultRecord, int) {
	u, err := validateURL(opts.TargetURL)
	if err != nil {
		rec := failureRecord(opts.TargetURL, videoPlatform{}, failureFromExit(exitUsage, fmt.Sprintf("输入的 URL 无效: %v", err), ""))
		appendResultRecordBestEffort(rec)
		logError("get.url_invalid", "url", opts.TargetURL, "error", err)
		return rec, exitUsage
	}

	outputTemplate, outputDir, err := resolveGetOutput(opts.OutDir, opts.NameTemplate)
	if err != nil {
		rec := failureRecord(opts.TargetURL, videoPlatform{}, failureFromExit(exitUsage, err.Error(), ""))
		appendResultRecordBestEffort(rec)
		logError("get.output_options_invalid", "out_dir", opts.OutDir, "name_template", opts.NameTemplate, "error", err)
		return rec, exitUsage
	}

	found, err := detectDeps()
	if err != nil {
		var depErr dependencyError
		if errors.As(err, &depErr) {
			rec := failureRecord(opts.TargetURL, videoPlatform{}, failureFromExit(depErr.ExitCode, depErr.Message, ""))
			appendResultRecordBestEffort(rec)
			logError("deps.validation_failed", "exit_code", depErr.ExitCode, "detail", depErr.Message)
			return rec, depErr.ExitCode
		}
		rec := failureRecord(opts.TargetURL, videoPlatform{}, failureFromExit(exitDownloadFailed, fmt.Sprintf("依赖检测失败: %v", err), ""))
		appendResultRecordBestEffort(rec)
		logError("deps.detect_failed", "error", err)
		return rec, exitDownloadFailed
	}

	p, ok := platformForURL(u)
	if !ok {
		// Unknown platform: still attempt to download, but don't persist any cookies.
		// This avoids storing a full browser cookie jar for an arbitrary site.
		p = videoPlatform{}
	}

	authSources := buildAuthSources()
	cookieFile := ""
	if strings.TrimSpace(p.ID) != "" {
		if v, err := cookiesCacheFilePath(p); err != nil {
			logWarn("auth.cookie_cache_path_unavailable", "platform", p.ID, "error", err)
		} else {
			cookieFile = v
			// Ensure app state dir exists so yt-dlp can dump the cookie jar.
			_ = os.MkdirAll(filepath.Dir(cookieFile), 0o700)
		}
	}

	logInfo("deps.tool_selected", "tool", "yt-dlp", "path", found.YtDlp.Path)
	logInfo("deps.tool_selected", "tool", "ffmpeg", "path", found.FFmpeg.Path)
	logInfo("deps.tool_selected", "tool", "ffprobe", "path", found.FFprobe.Path)
	logInfo("deps.tool_selected", "tool", found.JSRuntimeID, "path", found.JSRuntime.Path)
	if strings.TrimSpace(cookieFile) != "" {
		logInfo("auth.cookie_cache_enabled", "path", cookieFile)
	}
	logInfo("auth.fallback_policy_enabled", "strategy", "cache_then_browser")

	captureOutput := true
	cfg := ytDlpConfig{
		OutputTemplate:   outputTemplate,
		CaptureMovedPath: captureOutput,
		Quiet:            opts.JSON || opts.JSONL,
		ProgressOnly:     opts.AssetIDOnly && !opts.JSON,
	}
	failure, movedPaths := runWithAuthFallback(opts.TargetURL, found, p, authSources, cookieFile, cfg)
	// Self-heal: a generic download failure often means the bundled yt-dlp has
	// gone stale against an evolving site (e.g. Bilibili's HTTP 412). Refresh
	// yt-dlp once and retry before giving up.
	if failure.ExitCode == exitDownloadFailed && failure.ErrorCode == errorDownloadFailed {
		if updated, changed := tryAutoUpdateYtDlp(found.YtDlp); changed {
			logInfo("yt_dlp.auto_update_retry", "old_path", found.YtDlp.Path, "new_path", updated.Path)
			found.YtDlp = updated
			failure, movedPaths = runWithAuthFallback(opts.TargetURL, found, p, authSources, cookieFile, cfg)
		}
	}
	if failure.ExitCode != exitOK {
		rec := resultRecordFor(opts.TargetURL, p)
		rec.OK = false
		rec.ExitCode = failure.ExitCode
		rec.ErrorCode = failure.ErrorCode
		rec.RecoveryHint = failure.RecoveryHint
		rec.RecommendedCommand = failure.RecommendedCommand
		rec.OutputDir = outputDir
		rec.NameTemplate = outputTemplate
		appendResultRecordBestEffort(rec)
		return rec, failure.ExitCode
	}

	if !captureOutput {
		rec := resultRecordFor(opts.TargetURL, p)
		rec.OK = true
		rec.ExitCode = exitOK
		appendResultRecordBestEffort(rec)
		return rec, exitOK
	}

	outputPath := firstCapturedPath(movedPaths)
	if outputPath == "" {
		msg := "下载成功，但未能解析输出文件路径"
		if !opts.AssetIDOnly && !opts.JSON {
			logWarn("get.output_path_missing", "action", "skip_asset_index")
			rec := resultRecordFor(opts.TargetURL, p)
			rec.OK = true
			rec.ExitCode = exitOK
			appendResultRecordBestEffort(rec)
			return rec, exitOK
		}
		rec := resultRecordFor(opts.TargetURL, p)
		rec.OK = false
		rec.ExitCode = exitDownloadFailed
		rec.ErrorCode = errorDownloadFailed
		rec.RecoveryHint = msg
		rec.OutputDir = outputDir
		rec.NameTemplate = outputTemplate
		appendResultRecordBestEffort(rec)
		logError("get.output_path_missing", "error", msg)
		return rec, exitDownloadFailed
	}

	assetID, err := computeAssetID(outputPath)
	if err != nil {
		msg := fmt.Sprintf("生成 asset_id 失败: %v", err)
		rec := resultRecordFor(opts.TargetURL, p)
		rec.OK = false
		rec.ExitCode = exitDownloadFailed
		rec.ErrorCode = errorDownloadFailed
		rec.RecoveryHint = msg
		rec.FilePath = outputPath
		rec.OutputDir = outputDir
		rec.NameTemplate = outputTemplate
		appendResultRecordBestEffort(rec)
		logError("asset_id.compute_failed", "path", outputPath, "error", err)
		return rec, exitDownloadFailed
	}

	now := time.Now().UTC().Format(time.RFC3339)
	rec := resultRecordFor(opts.TargetURL, p)
	rec.OK = true
	rec.ExitCode = exitOK
	rec.AssetID = assetID
	rec.Title = filepath.Base(outputPath)
	rec.FilePath = outputPath
	rec.DownloadedAt = now
	rec.CreatedAt = now
	rec.OutputDir = outputDir
	rec.NameTemplate = outputTemplate
	appendResultRecordBestEffort(rec)
	return rec, exitOK
}

func resolveGetOutput(outDir, nameTemplate string) (template string, resolvedOutDir string, err error) {
	tpl := strings.TrimSpace(nameTemplate)
	if tpl == "" {
		tpl = defaultYtDlpOutputTemplate
	}

	trimmedOutDir := strings.TrimSpace(outDir)
	if trimmedOutDir == "" {
		return tpl, "", nil
	}

	absDir, err := filepath.Abs(trimmedOutDir)
	if err != nil {
		return "", "", fmt.Errorf("解析输出目录失败: %w", err)
	}
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		return "", "", fmt.Errorf("创建输出目录失败: %w", err)
	}

	if filepath.IsAbs(tpl) {
		return "", "", fmt.Errorf("`--name-template` 为绝对路径时，不可再配合 `--output-dir` 使用")
	}

	return filepath.Join(absDir, tpl), absDir, nil
}

func runLs(opts lsOptions) int {
	records, err := readResultRecords()
	if err != nil {
		logError("result_index.read_failed", "error", err)
		return exitDownloadFailed
	}

	filtered := filterResultRecords(records, opts)
	sort.Slice(filtered, func(i, j int) bool {
		return parseRecordTime(filtered[i]).After(parseRecordTime(filtered[j]))
	})
	if opts.Dedupe {
		filtered = dedupeResultRecords(filtered)
	}

	total := len(filtered)

	if len(filtered) > opts.Limit {
		filtered = filtered[:opts.Limit]
	}

	if opts.Format == "json" {
		data, err := json.Marshal(lsJSONResult{
			Total: total,
			Count: len(filtered),
			Limit: opts.Limit,
			Items: filtered,
		})
		if err != nil {
			logError("json.marshal_failed", "context", "ls_result", "error", err)
			return exitDownloadFailed
		}
		fmt.Println(string(data))
		return exitOK
	}

	printAssetTable(filtered)
	return exitOK
}

func filterResultRecords(in []resultRecord, opts lsOptions) []resultRecord {
	q := strings.ToLower(strings.TrimSpace(opts.Query))
	platform := strings.ToLower(strings.TrimSpace(opts.Platform))
	out := make([]resultRecord, 0, len(in))
	for _, r := range in {
		if opts.Failed && r.OK {
			continue
		}
		if opts.OK && !r.OK {
			continue
		}
		if platform != "" && strings.ToLower(strings.TrimSpace(r.Platform)) != platform {
			continue
		}
		if opts.Missing && (!r.OK || strings.TrimSpace(r.FilePath) == "" || fileExists(r.FilePath)) {
			continue
		}
		if q != "" {
			haystack := strings.ToLower(strings.Join([]string{
				r.TaskID,
				r.AssetID,
				r.SourceURL,
				r.Platform,
				r.Title,
				r.FilePath,
				r.ErrorCode,
				r.RecoveryHint,
			}, " "))
			if !strings.Contains(haystack, q) {
				continue
			}
		}
		out = append(out, r)
	}
	return out
}

func dedupeResultRecords(in []resultRecord) []resultRecord {
	seen := make(map[string]struct{}, len(in))
	out := make([]resultRecord, 0, len(in))
	for _, r := range in {
		id := strings.TrimSpace(r.TaskID)
		if id == "" {
			id = strings.TrimSpace(r.AssetID)
		}
		if id == "" {
			out = append(out, r)
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, r)
	}
	return out
}

func parseRecordTime(r resultRecord) time.Time {
	if t, err := time.Parse(time.RFC3339, strings.TrimSpace(r.CreatedAt)); err == nil {
		return t
	}
	return time.Time{}
}

func printAssetTable(records []resultRecord) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "STATUS\tTASK_ID\tASSET_ID\tPLATFORM\tCREATED_AT\tTITLE\tPATH_OR_ERROR")
	for _, r := range records {
		status := "failed"
		if r.OK {
			status = "ok"
			if strings.TrimSpace(r.FilePath) != "" && !fileExists(r.FilePath) {
				status = "missing"
			}
		}
		title := strings.TrimSpace(r.Title)
		if title == "" {
			title = filepath.Base(r.FilePath)
		}
		pathOrError := strings.TrimSpace(r.FilePath)
		if pathOrError == "" {
			pathOrError = strings.TrimSpace(r.ErrorCode)
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			status,
			r.TaskID,
			r.AssetID,
			r.Platform,
			r.CreatedAt,
			title,
			pathOrError,
		)
	}
	_ = w.Flush()
}

func resultsIndexFilePath() (string, error) {
	base, err := appStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "results-v1.jsonl"), nil
}

func appendResultRecordBestEffort(rec resultRecord) {
	if err := appendResultRecord(rec); err != nil {
		logWarn("result_index.append_failed", "error", err, "task_id", rec.TaskID, "asset_id", rec.AssetID)
	}
}

func appendResultRecord(rec resultRecord) error {
	indexPath, err := resultsIndexFilePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o700); err != nil {
		return err
	}

	normalized := rec
	if normalized.SchemaVersion == 0 {
		normalized.SchemaVersion = 1
	}
	if strings.TrimSpace(normalized.TaskID) == "" {
		normalized.TaskID = computeTaskID(normalized.SourceURL)
	}
	if strings.TrimSpace(normalized.CreatedAt) == "" {
		normalized.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if strings.TrimSpace(normalized.Title) == "" && strings.TrimSpace(normalized.FilePath) != "" {
		normalized.Title = filepath.Base(normalized.FilePath)
	}

	f, err := os.OpenFile(indexPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()

	b, err := json.Marshal(normalized)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		return err
	}
	return nil
}

func readResultRecords() ([]resultRecord, error) {
	indexPath, err := resultsIndexFilePath()
	if err != nil {
		return nil, err
	}
	if !fileExists(indexPath) {
		return []resultRecord{}, nil
	}

	f, err := os.Open(indexPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := make([]resultRecord, 0, 64)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec resultRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if strings.TrimSpace(rec.SourceURL) == "" && strings.TrimSpace(rec.TaskID) == "" {
			continue
		}
		if rec.SchemaVersion == 0 {
			rec.SchemaVersion = 1
		}
		if strings.TrimSpace(rec.TaskID) == "" {
			rec.TaskID = computeTaskID(rec.SourceURL)
		}
		if strings.TrimSpace(rec.Title) == "" && strings.TrimSpace(rec.FilePath) != "" {
			rec.Title = filepath.Base(rec.FilePath)
		}
		out = append(out, rec)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func firstCapturedPath(paths []string) string {
	for _, p := range paths {
		v := strings.Trim(strings.TrimSpace(p), "\"")
		if v == "" {
			continue
		}
		abs, err := filepath.Abs(v)
		if err == nil && fileExists(abs) {
			return abs
		}
		if fileExists(v) {
			return v
		}
	}
	return ""
}

func computeAssetID(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return "", err
	}

	h := sha256.New()
	_, _ = h.Write([]byte("mingest-asset-v1\n"))
	_, _ = h.Write([]byte(strconv.FormatInt(info.Size(), 10)))
	_, _ = h.Write([]byte{'\n'})

	const chunk = 1 << 20 // 1MB
	buf := make([]byte, chunk)

	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", err
	}
	_, _ = h.Write(buf[:n])

	if info.Size() > int64(chunk) {
		if _, err := f.Seek(-int64(chunk), io.SeekEnd); err == nil {
			n, err = io.ReadFull(f, buf)
			if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
				return "", err
			}
			_, _ = h.Write(buf[:n])
		}
	}

	sum := hex.EncodeToString(h.Sum(nil))
	if len(sum) < 16 {
		return "", fmt.Errorf("无法生成 asset_id")
	}
	return "ast_" + sum[:16], nil
}

func computeTaskID(rawURL string) string {
	normalized := normalizeTaskURL(rawURL)
	h := sha256.Sum256([]byte("mingest-task-v1\n" + normalized))
	sum := hex.EncodeToString(h[:])
	return "tsk_" + sum[:16]
}

func normalizeTaskURL(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return rawURL
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.Fragment = ""
	return u.String()
}

func resultRecordFor(rawURL string, platform videoPlatform) resultRecord {
	now := time.Now().UTC().Format(time.RFC3339)
	return resultRecord{
		SchemaVersion: 1,
		TaskID:        computeTaskID(rawURL),
		SourceURL:     strings.TrimSpace(rawURL),
		Platform:      strings.TrimSpace(platform.ID),
		ExitCode:      exitOK,
		CreatedAt:     now,
	}
}

func failureRecord(rawURL string, platform videoPlatform, failure failureClassification) resultRecord {
	rec := resultRecordFor(rawURL, platform)
	rec.OK = false
	rec.ExitCode = failure.ExitCode
	rec.ErrorCode = failure.ErrorCode
	rec.RecoveryHint = failure.RecoveryHint
	rec.RecommendedCommand = failure.RecommendedCommand
	return rec
}

func failureFromExit(exitCode int, hint string, recommendedCommand string) failureClassification {
	return failureClassification{
		ExitCode:           exitCode,
		ErrorCode:          errorCodeForExit(exitCode),
		RecoveryHint:       strings.TrimSpace(hint),
		RecommendedCommand: strings.TrimSpace(recommendedCommand),
	}
}

func errorCodeForExit(exitCode int) string {
	switch exitCode {
	case exitOK:
		return ""
	case exitUsage:
		return errorUsage
	case exitAuthRequired:
		return errorAuthRequired
	case exitCookieProblem:
		return errorCookieProblem
	case exitRuntimeMissing, exitFFmpegMissing, exitYtDlpMissing:
		return errorToolMissing
	default:
		return errorDownloadFailed
	}
}

func authLoginCommand(platform videoPlatform) string {
	if strings.TrimSpace(platform.ID) == "" {
		return "mingest auth login <platform>"
	}
	return "mingest auth login " + platform.ID
}

func printResultJSON(v resultRecord) {
	data, err := json.Marshal(v)
	if err != nil {
		logError("json.marshal_failed", "context", "result_record", "error", err)
		return
	}
	fmt.Println(string(data))
}

func validateURL(raw string) (*url.URL, error) {
	u, err := url.ParseRequestURI(raw)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("仅支持 http/https URL")
	}
	if u.Host == "" {
		return nil, fmt.Errorf("URL 缺少主机名")
	}
	return u, nil
}

type dependencyError struct {
	Message  string
	ExitCode int
}

func (e dependencyError) Error() string {
	return e.Message
}

func detectDeps() (deps, error) {
	exeDir, err := executableDir()
	if err != nil {
		return deps{}, err
	}
	wd, _ := os.Getwd()

	// Prefer the managed (self-updated) yt-dlp, then the current working directory
	// (where users typically place the tool bundle), then the executable
	// directory, then PATH.
	ytPath, ok := resolveYtDlpBinary(wd, exeDir)
	if !ok {
		return deps{}, dependencyError{
			Message:  "未找到 yt-dlp。请将 yt-dlp 放在程序同目录，或加入 PATH。",
			ExitCode: exitYtDlpMissing,
		}
	}

	ffmpegPath, ok := findBinary("ffmpeg", wd, exeDir)
	if !ok {
		return deps{}, dependencyError{
			Message:  "未找到 ffmpeg。请将 ffmpeg 放在程序同目录，或加入 PATH。",
			ExitCode: exitFFmpegMissing,
		}
	}

	ffprobePath, ok := findBinary("ffprobe", wd, exeDir)
	if !ok {
		return deps{}, dependencyError{
			Message:  "未找到 ffprobe。请将 ffprobe 与 ffmpeg 放在同一目录（工作目录或程序同目录），或加入 PATH。",
			ExitCode: exitFFmpegMissing,
		}
	}

	// yt-dlp expects ffmpeg/ffprobe to be discoverable together. We pass --ffmpeg-location as a directory.
	if filepath.Dir(ffmpegPath) != filepath.Dir(ffprobePath) {
		return deps{}, dependencyError{
			Message:  fmt.Sprintf("检测到 ffmpeg 与 ffprobe 不在同一目录（ffmpeg=%s, ffprobe=%s）。请将它们放在同一目录，或改用 *_bundled。", ffmpegPath, ffprobePath),
			ExitCode: exitFFmpegMissing,
		}
	}

	nodePath, ok := findBinary("node", wd, exeDir)
	if !ok {
		return deps{}, dependencyError{
			Message:  "未找到 Node.js。请将 node 放在程序同目录，或加入 PATH。",
			ExitCode: exitRuntimeMissing,
		}
	}

	return deps{
		YtDlp:       tool{Name: "yt-dlp", Path: ytPath},
		FFmpeg:      tool{Name: "ffmpeg", Path: ffmpegPath},
		FFprobe:     tool{Name: "ffprobe", Path: ffprobePath},
		JSRuntime:   tool{Name: "node", Path: nodePath},
		JSRuntimeID: "node",
	}, nil
}

func executableDir() (string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Dir(exePath), nil
}

func findBinary(name string, preferredDirs ...string) (string, bool) {
	candidates := []string{name}
	if runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(name), ".exe") {
		candidates = append(candidates, name+".exe")
	}

	for _, c := range candidates {
		for _, dir := range preferredDirs {
			if strings.TrimSpace(dir) == "" {
				continue
			}
			local := filepath.Join(dir, c)
			if isRunnableFile(local) {
				return local, true
			}
		}
	}

	if path, ok := embedtools.Find(name); ok {
		return path, true
	}

	for _, c := range candidates {
		if p, ok := findInPath(c); ok {
			return p, true
		}
	}

	return "", false
}

func findBinaryPreferPath(name string, fallbackDirs ...string) (string, bool) {
	candidates := []string{name}
	if runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(name), ".exe") {
		candidates = append(candidates, name+".exe")
	}

	for _, c := range candidates {
		if p, ok := findInPath(c); ok {
			return p, true
		}
	}

	for _, dir := range fallbackDirs {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		for _, c := range candidates {
			local := filepath.Join(dir, c)
			if isRunnableFile(local) {
				return local, true
			}
		}
	}

	return "", false
}

func findInPath(name string) (string, bool) {
	pathEnv := os.Getenv("PATH")
	if pathEnv == "" && runtime.GOOS == "windows" {
		pathEnv = os.Getenv("Path")
	}
	if pathEnv == "" {
		return "", false
	}

	for _, dir := range filepath.SplitList(pathEnv) {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		candidate := filepath.Join(dir, name)
		if isRunnableFile(candidate) {
			return candidate, true
		}
	}
	return "", false
}

func isRunnableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	// Windows 不依赖可执行位，仅校验存在即可。
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode()&0o111 != 0
}

func buildAuthSources() []authSource {
	if v := selectedBrowserEnv(); v != "" {
		if !isSupportedBrowserName(v) {
			logWarn("auth.browser_env_invalid", "env", envBrowser, "value", v)
			return nil
		}
		return []authSource{{Kind: authKindBrowser, Value: v}}
	}

	browsers := autoBrowserOrder()
	out := make([]authSource, 0, len(browsers))
	for _, b := range browsers {
		out = append(out, authSource{Kind: authKindBrowser, Value: b})
	}
	return out
}

func autoBrowserOrder() []string {
	available := detectBrowsers()
	if len(available) == 1 {
		return available
	}

	// Multiple or unknown: default to chrome first, then others.
	pick := func(list []string, v string) []string {
		for _, x := range list {
			if x == v {
				return list
			}
		}
		return append(list, v)
	}

	out := make([]string, 0, 4)
	if contains(available, "chrome") || len(available) == 0 {
		out = pick(out, "chrome")
	}
	if contains(available, "firefox") || len(available) == 0 {
		out = pick(out, "firefox")
	}
	if contains(available, "chromium") || len(available) == 0 {
		out = pick(out, "chromium")
	}
	if contains(available, "edge") || len(available) == 0 {
		out = pick(out, "edge")
	}
	return out
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func detectBrowsers() []string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return nil
	}

	type browserPath struct {
		Browser string
		Paths   []string
	}

	var checks []browserPath
	switch runtime.GOOS {
	case "linux":
		checks = []browserPath{
			{Browser: "chrome", Paths: []string{filepath.Join(home, ".config", "google-chrome")}},
			{Browser: "chromium", Paths: []string{filepath.Join(home, ".config", "chromium")}},
			{Browser: "edge", Paths: []string{filepath.Join(home, ".config", "microsoft-edge")}},
			{Browser: "firefox", Paths: []string{filepath.Join(home, ".mozilla", "firefox")}},
		}
	case "darwin":
		checks = []browserPath{
			{Browser: "chrome", Paths: []string{filepath.Join(home, "Library", "Application Support", "Google", "Chrome")}},
			{Browser: "chromium", Paths: []string{filepath.Join(home, "Library", "Application Support", "Chromium")}},
			{Browser: "edge", Paths: []string{filepath.Join(home, "Library", "Application Support", "Microsoft Edge")}},
			{Browser: "firefox", Paths: []string{filepath.Join(home, "Library", "Application Support", "Firefox")}},
		}
	case "windows":
		localAppData := os.Getenv("LOCALAPPDATA")
		appData := os.Getenv("APPDATA")
		checks = []browserPath{
			{Browser: "chrome", Paths: []string{filepath.Join(localAppData, "Google", "Chrome", "User Data")}},
			{Browser: "chromium", Paths: []string{filepath.Join(localAppData, "Chromium", "User Data")}},
			{Browser: "edge", Paths: []string{filepath.Join(localAppData, "Microsoft", "Edge", "User Data")}},
			{Browser: "firefox", Paths: []string{filepath.Join(appData, "Mozilla", "Firefox")}},
		}
	default:
		return nil
	}

	var out []string
	for _, c := range checks {
		for _, p := range c.Paths {
			if dirExists(p) {
				out = append(out, c.Browser)
				break
			}
		}
	}
	return out
}

func dirExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func fileExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func runWithAuthFallback(targetURL string, d deps, platform videoPlatform, sources []authSource, cookieFile string, cfg ytDlpConfig) (failureClassification, []string) {
	// 0) Fast path: try cached cookies first (no browser DB access).
	if strings.TrimSpace(cookieFile) != "" {
		logInfo("auth.method_selected", "source", "cookie_cache")
		failure, paths := runYtDlp(d, buildYtDlpArgsWithCookiesFile(targetURL, d, cookieFile, cfg), platform, cfg)
		// Always attempt to filter after yt-dlp touches the cookie jar.
		if fileExists(cookieFile) {
			if err := filterCookieFileForPlatform(cookieFile, platform); err != nil {
				logWarn("auth.cookie_filter_failed", "error", err, "path", cookieFile)
			}
		}
		if failure.ExitCode == exitOK {
			return failure, paths
		}
		// If it's not an auth/cookie issue, browser fallbacks won't help.
		if !shouldTryNextAuth(failure.ExitCode) {
			return failure, nil
		}
	}

	if len(sources) == 0 {
		return failureFromExit(exitAuthRequired, "需要登录。请先准备平台登录状态。", authLoginCommand(platform)), nil
	}

	lastFailure := failureFromExit(exitDownloadFailed, "下载失败。", "")
	for i, src := range sources {
		logInfo("auth.method_selected", "current", i+1, "total", len(sources), "source", authSourceLabel(src))
		args := []string{}
		tmpCookieFile := ""
		tmpCleanup := func() {}
		// IMPORTANT:
		// yt-dlp's --cookies FILE is both an input and an output (it "dumps cookie jar" back).
		// If we pass the persistent cache file when extracting from a browser, an unauthenticated
		// browser (e.g. Edge not logged in) can overwrite the cache and break subsequent runs.
		//
		// To prevent this, browser-based attempts use a temp cookie jar file and only promote it
		// to the persistent cache if it looks authenticated.
		if strings.TrimSpace(cookieFile) != "" && src.Kind == authKindBrowser {
			dir := filepath.Dir(cookieFile)
			p, cleanup, err := createTempCookieJarFile(dir)
			if err == nil {
				tmpCookieFile = p
				tmpCleanup = cleanup
				args = buildYtDlpArgsWithCookieCache(targetURL, d, src, tmpCookieFile, cfg)
			} else {
				// Fallback: proceed without temp jar; this loses caching but keeps functionality.
				args = buildYtDlpArgs(targetURL, d, src, cfg)
			}
		} else {
			args = buildYtDlpArgsWithCookieCache(targetURL, d, src, cookieFile, cfg)
		}
		failure, paths := runYtDlp(d, args, platform, cfg)
		// Best-effort: if the browser attempt produced an authenticated cookie jar, update cache.
		if tmpCookieFile != "" && fileExists(tmpCookieFile) && strings.TrimSpace(cookieFile) != "" {
			if err := filterCookieFileForPlatform(tmpCookieFile, platform); err != nil {
				logWarn("auth.cookie_filter_failed", "error", err, "path", tmpCookieFile)
			} else if ok, err := cookieFileLooksLikeAuthenticated(tmpCookieFile, platform); err == nil && ok {
				if err := copyFileAtomic(tmpCookieFile, cookieFile); err != nil {
					logWarn("auth.cookie_cache_update_failed", "error", err, "path", cookieFile)
				}
			}
		}
		tmpCleanup()
		if strings.TrimSpace(cookieFile) != "" && fileExists(cookieFile) {
			// Keep the cache minimal even if yt-dlp added extra domains.
			if err := filterCookieFileForPlatform(cookieFile, platform); err != nil {
				logWarn("auth.cookie_filter_failed", "error", err, "path", cookieFile)
			}
		}
		if failure.ExitCode == exitOK {
			if i > 0 && selectedBrowserEnv() == "" {
				logInfo("auth.browser_auto_switched", "browser", src.Value, "env", envBrowser)
			}
			return failure, paths
		}
		// Prefer Chrome, but on Windows Chrome cookie decryption frequently fails.
		// When chrome fails, try CDP (Chrome gives us decrypted cookies) before falling back to Firefox.
		if src.Kind == authKindBrowser && src.Value == "chrome" && shouldTryNextAuth(failure.ExitCode) {
			logWarn("auth.chrome_cookie_failed_try_cdp")
			cdpFailure, cdpPaths := tryDownloadWithChromeCDP(targetURL, d, platform, cookieFile, cfg)
			if cdpFailure.ExitCode == exitOK {
				if strings.TrimSpace(cookieFile) != "" && fileExists(cookieFile) {
					if err := filterCookieFileForPlatform(cookieFile, platform); err != nil {
						logWarn("auth.cookie_filter_failed", "error", err, "path", cookieFile)
					}
				}
				return cdpFailure, cdpPaths
			}
			// If CDP cannot provide a working session, guide the user to prepare the managed profile.
			if cdpFailure.ExitCode == exitAuthRequired {
				cmd := authLoginCommand(platform)
				logWarn("auth.cdp_session_not_authorized", "recommended_command", cmd)
				// Keep classification as AUTH_REQUIRED so callers can decide what to do.
				failure = cdpFailure
			} else if cdpFailure.ExitCode == exitCookieProblem {
				failure = cdpFailure
			}
		}

		lastFailure = failure

		if i < len(sources)-1 && shouldTryNextAuth(failure.ExitCode) {
			logWarn("auth.method_failed_try_next", "exit_code", failure.ExitCode)
			continue
		}
		break
	}

	if shouldTryNextAuth(lastFailure.ExitCode) {
		logError("auth.no_valid_session")
		logError("auth.recovery_hint", "hint", "login in browser and retry")
		if selectedBrowserEnv() == "" {
			logError("auth.recovery_hint", "hint", "BROWSER=firefox mingest get <url>")
		} else {
			logError("auth.recovery_hint", "hint", "check BROWSER login/profile and retry")
		}
		cmd := authLoginCommand(platform)
		logError("auth.recovery_hint", "hint", "run auth first", "command", cmd)
		if lastFailure.RecommendedCommand == "" {
			lastFailure.RecommendedCommand = cmd
		}
		if lastFailure.RecoveryHint == "" {
			lastFailure.RecoveryHint = "未检测到可用登录状态。请先登录后重试。"
		}
		if lastFailure.ErrorCode == "" {
			lastFailure.ErrorCode = errorAuthRequired
		}
		return lastFailure, nil
	}
	return lastFailure, nil
}

func shouldTryNextAuth(code int) bool {
	return code == exitAuthRequired || code == exitCookieProblem
}

func authSourceLabel(src authSource) string {
	switch src.Kind {
	case authKindBrowser:
		return "browser_cookies:" + src.Value
	}
	return "unknown"
}

func buildYtDlpArgs(targetURL string, d deps, src authSource, cfg ytDlpConfig) []string {
	args := buildYtDlpBaseArgs(d, cfg)

	switch src.Kind {
	case authKindBrowser:
		browserArg := src.Value
		if p := selectedBrowserProfile(); p != "" {
			browserArg = browserArg + ":" + p
		}
		args = append(args, "--cookies-from-browser", browserArg)
	default:
		// no auth args
	}

	args = append(args, targetURL)
	return args
}

func buildYtDlpArgsWithCookieCache(targetURL string, d deps, src authSource, cookieFile string, cfg ytDlpConfig) []string {
	args := buildYtDlpBaseArgs(d, cfg)

	switch src.Kind {
	case authKindBrowser:
		browserArg := src.Value
		if p := selectedBrowserProfile(); p != "" {
			browserArg = browserArg + ":" + p
		}
		args = append(args, "--cookies-from-browser", browserArg)
	default:
		// no auth args
	}

	if strings.TrimSpace(cookieFile) != "" {
		args = append(args, "--cookies", cookieFile)
	}

	args = append(args, targetURL)
	return args
}

func buildYtDlpArgsWithCookiesFile(targetURL string, d deps, cookieFile string, cfg ytDlpConfig) []string {
	args := buildYtDlpBaseArgs(d, cfg)
	args = append(args, "--cookies", cookieFile, targetURL)
	return args
}

func buildYtDlpBaseArgs(d deps, cfg ytDlpConfig) []string {
	outputTemplate := strings.TrimSpace(cfg.OutputTemplate)
	if outputTemplate == "" {
		outputTemplate = defaultYtDlpOutputTemplate
	}

	ffmpegDir := filepath.Dir(d.FFmpeg.Path)
	args := []string{
		"--ffmpeg-location", ffmpegDir,
		"--js-runtime", d.JSRuntimeID,
	}
	// When yt-dlp's output is piped through our wrapper, Windows locale encodings frequently
	// cause garbled filenames in the console. Forcing UTF-8 makes output consistent.
	if runtime.GOOS == "windows" {
		args = append(args, "--encoding", "utf-8")
	}

	if cfg.ProbeOnly {
		args = append(args, "--skip-download", "--simulate", "--no-playlist")
		return args
	}

	args = append(args,
		"--output", outputTemplate,
		"--embed-thumbnail",
		"--add-metadata",
		"-f", "bestvideo[vcodec^=avc1]+bestaudio[ext=m4a]/best[ext=mp4]/best",
		"--merge-output-format", "mp4",
	)
	if !cfg.Quiet {
		args = append(args,
			"--progress",
			"--newline",
			"--progress-template", "download:"+ytDlpProgressMarker+"%(progress._percent_str)s|%(progress._speed_str)s|%(progress.eta)s",
		)
	}
	if cfg.CaptureMovedPath {
		args = append(args, "--print", "after_move:"+ytDlpPathMarker+"%(filepath)s")
	}
	return args
}

func runYtDlp(d deps, args []string, platform videoPlatform, cfg ytDlpConfig) (failureClassification, []string) {
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		logError("yt_dlp.stdout_pipe_create_failed", "error", err)
		return failureFromExit(exitDownloadFailed, "无法创建 yt-dlp stdout 管道。", ""), nil
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		_ = stdoutR.Close()
		_ = stdoutW.Close()
		logError("yt_dlp.stderr_pipe_create_failed", "error", err)
		return failureFromExit(exitDownloadFailed, "无法创建 yt-dlp stderr 管道。", ""), nil
	}

	procArgs := append([]string{d.YtDlp.Path}, args...)
	env := withPrependedPath(os.Environ(), filepath.Dir(d.JSRuntime.Path))
	// Make yt-dlp output deterministic on Windows consoles and when piped.
	env = withEnvVar(env, "PYTHONUTF8", "1")
	env = withEnvVar(env, "PYTHONIOENCODING", "utf-8")
	proc, err := os.StartProcess(
		d.YtDlp.Path,
		procArgs,
		&os.ProcAttr{
			Env: env,
			Dir: ".",
			Files: []*os.File{
				os.Stdin,
				stdoutW,
				stderrW,
			},
		},
	)
	_ = stdoutW.Close()
	_ = stderrW.Close()

	if err != nil {
		_ = stdoutR.Close()
		_ = stderrR.Close()
		logError("yt_dlp.start_failed", "error", err)
		return failureFromExit(exitDownloadFailed, fmt.Sprintf("无法启动 yt-dlp: %v", err), ""), nil
	}

	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(2)

	stdoutTarget := io.Writer(os.Stdout)
	stderrTarget := io.Writer(os.Stderr)
	progress := newProgressRenderer(stderrTarget)
	if cfg.Quiet {
		stdoutTarget = io.Discard
		stderrTarget = io.Discard
		progress = nil
	} else if cfg.ProgressOnly {
		// Keep progress rendering while silencing non-progress yt-dlp logs.
		stdoutTarget = io.Discard
		stderrTarget = io.Discard
	}

	go streamAndCapture(stdoutR, stdoutTarget, &stdoutBuf, streamOptions{
		HidePathMarker: cfg.CaptureMovedPath,
		Progress:       progress,
	}, &wg)
	go streamAndCapture(stderrR, stderrTarget, &stderrBuf, streamOptions{
		Progress: progress,
	}, &wg)

	state, waitErr := proc.Wait()
	wg.Wait()
	if progress != nil {
		progress.finish()
	}
	combined := stdoutBuf.String() + "\n" + stderrBuf.String()

	if waitErr != nil {
		logError("yt_dlp.wait_failed", "error", waitErr)
		return failureFromExit(exitDownloadFailed, fmt.Sprintf("yt-dlp 执行失败: %v", waitErr), ""), nil
	}
	if state.Success() {
		return failureClassification{ExitCode: exitOK}, extractMovedPaths(stdoutBuf.String(), cfg.CaptureMovedPath)
	}

	failure := classifyFailure(combined, platform)
	if failure.RecoveryHint != "" {
		logWarn("yt_dlp.failure_hint", "hint", failure.RecoveryHint)
	}
	if failure.ExitCode == exitDownloadFailed {
		logError("yt_dlp.exit_code_unexpected", "exit_code", state.ExitCode())
	}

	return failure, nil
}

func extractMovedPaths(stdout string, enabled bool) []string {
	if !enabled {
		return nil
	}
	seen := make(map[string]struct{})
	out := make([]string, 0, 1)
	for _, line := range strings.Split(stdout, "\n") {
		v := strings.TrimSpace(line)
		if !strings.HasPrefix(v, ytDlpPathMarker) {
			continue
		}
		p := strings.Trim(strings.TrimPrefix(v, ytDlpPathMarker), "\"")
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

func streamAndCapture(r *os.File, target io.Writer, buf *bytes.Buffer, opts streamOptions, wg *sync.WaitGroup) {
	defer wg.Done()
	defer r.Close()

	reader := bufio.NewReader(r)
	for {
		chunk, err := reader.ReadString('\n')
		if chunk != "" {
			_, _ = buf.WriteString(chunk)
			line := strings.TrimSpace(chunk)
			if opts.HidePathMarker && strings.HasPrefix(line, ytDlpPathMarker) {
				// Internal marker used to capture output path.
			} else if opts.Progress != nil && strings.HasPrefix(line, ytDlpProgressMarker) {
				opts.Progress.render(strings.TrimPrefix(line, ytDlpProgressMarker))
			} else {
				_, _ = io.WriteString(target, chunk)
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			break
		}
	}
}

func newProgressRenderer(out io.Writer) *progressRenderer {
	file, ok := out.(*os.File)
	isTTY := false
	if ok {
		if info, err := file.Stat(); err == nil {
			isTTY = (info.Mode() & os.ModeCharDevice) != 0
		}
	}
	return &progressRenderer{
		out:         out,
		tty:         isTTY,
		lastPercent: -1,
	}
}

func (p *progressRenderer) render(payload string) {
	if p == nil || p.out == nil {
		return
	}

	percent, speed, eta, ok := parseYtDlpProgressPayload(payload)
	if !ok {
		return
	}

	percentInt := int(percent + 0.5)
	if percentInt < 0 {
		percentInt = 0
	}
	if percentInt > 100 {
		percentInt = 100
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.tty {
		if percentInt == 100 && p.lastPercent == 100 {
			return
		}
		if p.lastPercent >= 0 && percentInt < 100 && percentInt < p.lastPercent+10 {
			return
		}
	}

	bar := renderProgressBar(percentInt, 20)
	msg := fmt.Sprintf("下载进度 %s %3d%% 速度 %s ETA %s", bar, percentInt, speed, eta)
	if p.tty {
		_, _ = io.WriteString(p.out, "\r"+msg)
	} else {
		_, _ = io.WriteString(p.out, msg+"\n")
	}
	p.lastPercent = percentInt
	p.hadRender = true
}

func (p *progressRenderer) finish() {
	if p == nil || p.out == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.tty && p.hadRender {
		_, _ = io.WriteString(p.out, "\n")
	}
}

func parseYtDlpProgressPayload(payload string) (percent float64, speed string, eta string, ok bool) {
	parts := strings.Split(payload, "|")
	if len(parts) < 3 {
		return 0, "", "", false
	}

	p := strings.TrimSpace(strings.TrimSuffix(parts[0], "%"))
	v, err := strconv.ParseFloat(p, 64)
	if err != nil {
		return 0, "", "", false
	}

	speed = strings.TrimSpace(parts[1])
	eta = strings.TrimSpace(parts[2])
	if speed == "" {
		speed = "N/A"
	}
	if eta == "" || eta == "NA" {
		eta = "--:--"
	}
	return v, speed, eta, true
}

func renderProgressBar(percent int, width int) string {
	if width <= 0 {
		return "[]"
	}
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}

	filled := int(float64(width) * float64(percent) / 100.0)
	if filled < 0 {
		filled = 0
	}
	if filled > width {
		filled = width
	}
	return "[" + strings.Repeat("=", filled) + strings.Repeat(".", width-filled) + "]"
}

func withPrependedPath(env []string, dir string) []string {
	if strings.TrimSpace(dir) == "" {
		return env
	}
	pathKey := "PATH"
	if runtime.GOOS == "windows" {
		pathKey = "Path"
	}
	sep := string(os.PathListSeparator)

	out := make([]string, 0, len(env)+1)
	found := false
	for _, kv := range env {
		if strings.HasPrefix(kv, pathKey+"=") {
			found = true
			curr := strings.TrimPrefix(kv, pathKey+"=")
			out = append(out, pathKey+"="+dir+sep+curr)
			continue
		}
		out = append(out, kv)
	}
	if !found {
		out = append(out, pathKey+"="+dir)
	}
	return out
}

func withEnvVar(env []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	found := false
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			found = true
			out = append(out, prefix+value)
			continue
		}
		out = append(out, kv)
	}
	if !found {
		out = append(out, prefix+value)
	}
	return out
}

func classifyFailure(output string, platform videoPlatform) failureClassification {
	lower := strings.ToLower(output)

	authCmd := authLoginCommand(platform)
	name := strings.TrimSpace(platform.Name)
	if name == "" {
		name = strings.TrimSpace(platform.ID)
	}

	if strings.Contains(lower, "could not copy") && strings.Contains(lower, "cookie database") {
		return failureClassification{
			ExitCode:           exitCookieProblem,
			ErrorCode:          errorCookieProblem,
			RecoveryHint:       fmt.Sprintf("浏览器 cookies 数据库无法读取（常见原因: 浏览器仍在占用 cookies 数据库）。请先彻底退出浏览器（含后台进程）后重试；或改用 Firefox；或执行 `%s`（使用 CDP 从浏览器进程内导出 cookies，避免读取数据库）。", authCmd),
			RecommendedCommand: authCmd,
		}
	}

	if strings.Contains(lower, "failed to decrypt with dpapi") {
		return failureClassification{
			ExitCode:           exitCookieProblem,
			ErrorCode:          errorCookieProblem,
			RecoveryHint:       fmt.Sprintf("浏览器 cookies 解密失败。请改用 Firefox，或执行 `%s`。", authCmd),
			RecommendedCommand: authCmd,
		}
	}

	// Chrome's App-Bound Cookie Encryption on Windows intentionally makes third-party decryption harder.
	// When enabled, tools that read/decrypt the cookie DB may fail even with admin rights.
	if strings.Contains(lower, "app-bound") && strings.Contains(lower, "cookie") && strings.Contains(lower, "encrypt") {
		return failureClassification{
			ExitCode:           exitCookieProblem,
			ErrorCode:          errorCookieProblem,
			RecoveryHint:       fmt.Sprintf("检测到 Chrome App-Bound Cookie Encryption 相关错误。此模式下第三方工具可能无法直接解密 Chrome cookies。建议改用 `%s`（CDP 方式）或改用 Firefox/Edge 的账户登录信息。", authCmd),
			RecommendedCommand: authCmd,
		}
	}

	if strings.Contains(lower, "permission denied") && strings.Contains(lower, "cookies") {
		return failureClassification{ExitCode: exitCookieProblem, ErrorCode: errorCookieProblem, RecoveryHint: "读取浏览器 cookies 被拒绝。请检查浏览器进程占用与文件权限。"}
	}

	if strings.Contains(lower, "cannot decrypt v11 cookies: no key found") {
		return failureClassification{
			ExitCode:           exitCookieProblem,
			ErrorCode:          errorCookieProblem,
			RecoveryHint:       fmt.Sprintf("浏览器 cookies 解密失败（keyring 不可用）。如果你是 SSH 会话，请在本机桌面终端运行，或改用 Firefox，或执行 `%s`。", authCmd),
			RecommendedCommand: authCmd,
		}
	}

	if strings.Contains(lower, "cookie") && strings.Contains(lower, "expired") {
		return failureClassification{
			ExitCode:           exitCookieProblem,
			ErrorCode:          errorCookieExpired,
			RecoveryHint:       fmt.Sprintf("登录凭据可能已过期。请执行 `%s` 刷新登录状态后重试。", authCmd),
			RecommendedCommand: authCmd,
		}
	}

	if strings.Contains(lower, "sign in to confirm you're not a bot") ||
		strings.Contains(lower, "sign in to confirm you’re not a bot") {
		target := "目标网站"
		if name != "" {
			target = name
		}
		return failureClassification{
			ExitCode:           exitAuthRequired,
			ErrorCode:          errorBotCheck,
			RecoveryHint:       fmt.Sprintf("需要登录 %s 并完成机器人验证。请先在浏览器完成验证后重试，或执行 `%s`。", target, authCmd),
			RecommendedCommand: authCmd,
		}
	}

	if strings.Contains(lower, "sign in to confirm your age") ||
		(strings.Contains(lower, "this video may be inappropriate for some users") && strings.Contains(lower, "sign in")) {
		target := "目标网站"
		if name != "" {
			target = name
		}
		return failureClassification{
			ExitCode:           exitAuthRequired,
			ErrorCode:          errorAuthRequired,
			RecoveryHint:       fmt.Sprintf("需要登录 %s 并完成额外确认。请在浏览器中登录并打开该视频完成确认后重试；或执行 `%s` 使用工具专用账户登录信息。", target, authCmd),
			RecommendedCommand: authCmd,
		}
	}

	// Generic "cookies suggested" auth-required detection for other extractors (e.g. bilibili).
	// Many sites use wording like: "you have to login ... Use --cookies-from-browser or --cookies for the authentication".
	if strings.Contains(lower, "use --cookies-from-browser") || strings.Contains(lower, "use --cookies") {
		if strings.Contains(lower, "login") ||
			strings.Contains(lower, "sign in") ||
			strings.Contains(lower, "premium member") ||
			strings.Contains(lower, "members only") ||
			strings.Contains(lower, "members-only") ||
			strings.Contains(lower, "authentication") {
			target := "目标网站"
			if name != "" {
				target = name
			}
			return failureClassification{
				ExitCode:           exitAuthRequired,
				ErrorCode:          errorAuthRequired,
				RecoveryHint:       fmt.Sprintf("需要登录 %s（或账号具备相应权限）。请先在浏览器中登录后重试；或执行 `%s`。", target, authCmd),
				RecommendedCommand: authCmd,
			}
		}
	}

	if strings.Contains(lower, "cookies file") && strings.Contains(lower, "netscape") {
		return failureClassification{ExitCode: exitCookieProblem, ErrorCode: errorCookieProblem, RecoveryHint: "cookies 文件格式异常。"}
	}

	if strings.Contains(lower, "no supported javascript runtime could be found") {
		return failureClassification{ExitCode: exitRuntimeMissing, ErrorCode: errorToolMissing, RecoveryHint: "Node.js 不可用。请确认 node 可执行，并可被该程序访问。"}
	}

	if strings.Contains(lower, "ffmpeg not found") {
		return failureClassification{ExitCode: exitFFmpegMissing, ErrorCode: errorToolMissing, RecoveryHint: "ffmpeg 不可用。请将 ffmpeg/ffprobe 放在同一目录（工作目录或程序同目录），或加入 PATH，或改用 *_bundled。"}
	}

	if strings.Contains(lower, "ffprobe not found") {
		return failureClassification{ExitCode: exitFFmpegMissing, ErrorCode: errorToolMissing, RecoveryHint: "ffprobe 不可用。请将 ffmpeg/ffprobe 放在同一目录（工作目录或程序同目录），或加入 PATH，或改用 *_bundled。"}
	}

	if strings.Contains(lower, "http error 429") ||
		strings.Contains(lower, "too many requests") ||
		strings.Contains(lower, "rate limit") ||
		strings.Contains(lower, "rate-limit") {
		return failureClassification{
			ExitCode:     exitDownloadFailed,
			ErrorCode:    errorRateLimited,
			RecoveryHint: "请求过于频繁或被平台限流。请稍后重试，或只重跑失败任务。",
		}
	}

	if strings.Contains(lower, "requested format is not available") ||
		strings.Contains(lower, "format is not available") {
		return failureClassification{
			ExitCode:     exitDownloadFailed,
			ErrorCode:    errorFormatBlocked,
			RecoveryHint: "请求的视频格式当前不可用。可稍后重试，或调整下载格式策略。",
		}
	}

	if strings.Contains(lower, "timed out") ||
		strings.Contains(lower, "connection reset") ||
		strings.Contains(lower, "network is unreachable") ||
		strings.Contains(lower, "temporary failure in name resolution") {
		return failureClassification{
			ExitCode:     exitDownloadFailed,
			ErrorCode:    errorNetwork,
			RecoveryHint: "网络连接失败。请检查网络后只重跑失败任务。",
		}
	}

	return failureClassification{
		ExitCode:     exitDownloadFailed,
		ErrorCode:    errorDownloadFailed,
		RecoveryHint: "下载失败。可先确认 yt-dlp 是否为最新版本，再检查 cookies 是否过期。",
	}
}
