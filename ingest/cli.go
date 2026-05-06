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
	exitDoctorFailed   = 41
	exitSemanticFailed = 42
)

const (
	defaultYtDlpOutputTemplate = "%(title)s.%(ext)s"
	ytDlpPathMarker            = "__MINGEST_PATH__"
	ytDlpProgressMarker        = "__MINGEST_PROGRESS__"
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
	TargetURL    string
	OutDir       string
	NameTemplate string
	AssetIDOnly  bool
	JSON         bool
}

type lsOptions struct {
	Limit  int
	Query  string
	Format string
	Dedupe bool
}

type getJSONResult struct {
	OK           bool   `json:"ok"`
	ExitCode     int    `json:"exit_code"`
	Error        string `json:"error,omitempty"`
	URL          string `json:"url,omitempty"`
	Platform     string `json:"platform,omitempty"`
	OutputPath   string `json:"output_path,omitempty"`
	AssetID      string `json:"asset_id,omitempty"`
	OutputDir    string `json:"out_dir,omitempty"`
	NameTemplate string `json:"name_template,omitempty"`
}

type ytDlpConfig struct {
	OutputTemplate   string
	CaptureMovedPath bool
	Quiet            bool
	ProgressOnly     bool
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

type assetRecord struct {
	AssetID    string `json:"asset_id"`
	URL        string `json:"url"`
	Platform   string `json:"platform"`
	Title      string `json:"title"`
	OutputPath string `json:"output_path"`
	CreatedAt  string `json:"created_at"`
}

type lsJSONResult struct {
	Total int           `json:"total"`
	Count int           `json:"count"`
	Limit int           `json:"limit"`
	Items []assetRecord `json:"items"`
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
	case "prep":
		opts, err := parsePrepOptions(args[2:])
		if err != nil {
			logError("cli.invalid_arguments", "command", "prep", "error", err)
			usage()
			return exitUsage
		}
		return runPrep(opts)
	case "export":
		opts, err := parseExportOptions(args[2:])
		if err != nil {
			logError("cli.invalid_arguments", "command", "export", "error", err)
			usage()
			return exitUsage
		}
		return runExport(opts)
	case "ls":
		opts, err := parseLsOptions(args[2:])
		if err != nil {
			logError("cli.invalid_arguments", "command", "ls", "error", err)
			usage()
			return exitUsage
		}
		return runLs(opts)
	case "doctor":
		opts, err := parseDoctorOptions(args[2:])
		if err != nil {
			logError("cli.invalid_arguments", "command", "doctor", "error", err)
			usage()
			return exitUsage
		}
		return runDoctor(opts)
	case "semantic":
		opts, err := parseSemanticOptions(args[2:])
		if err != nil {
			logError("cli.invalid_arguments", "command", "semantic", "error", err)
			usage()
			return exitUsage
		}
		return runSemantic(opts)
	case "auth", "login":
		if len(args) != 3 {
			usage()
			return exitUsage
		}
		p, ok := platformByID(args[2])
		if !ok {
			logError("auth.unsupported_platform", "platform", strings.TrimSpace(args[2]))
			usage()
			return exitUsage
		}
		return runAuth(p)
	default:
		usage()
		return exitUsage
	}
}

func usage() {
	fmt.Println("用法:")
	fmt.Println("  mingest get <url> [--out-dir <dir>] [--name-template <tpl>] [--asset-id-only] [--json]")
	fmt.Println("  mingest prep <asset_ref> --goal <subtitle|highlights|shorts> [--lang <auto|zh|en>] [--max-clips <n>] [--clip-seconds <sec>] [--subtitle-style <clean|shorts>] [--json]")
	fmt.Println("  mingest export <asset_ref> --to <premiere|resolve|capcut> [--with <srt,edl,csv,fcpxml>] [--out-dir <dir>] [--zip] [--json]")
	fmt.Println("  mingest ls [--limit <n>] [--query <text>] [--format <table|json>] [--dedupe]")
	fmt.Println("  mingest doctor <asset_ref> [--target <youtube|bilibili|shorts>] [--strict] [--json]")
	fmt.Println("  mingest semantic <asset_ref> [--target <youtube|bilibili|shorts>] [--provider <auto|openai|openrouter>] [--model <name>] [--visual-diversity <0-1>] [--apply] [--json]")
	fmt.Println("  mingest auth <platform>")
	fmt.Println()
	fmt.Println("get 参数:")
	fmt.Println("  --out-dir <dir>           设置下载目录（默认当前工作目录）")
	fmt.Println("  --name-template <tpl>     设置输出模板（默认 %(title)s.%(ext)s）")
	fmt.Println("  --asset-id-only           仅输出 asset_id（便于脚本串联）")
	fmt.Println("  --json                    输出 JSON 结果")
	fmt.Println()
	fmt.Println("prep 参数:")
	fmt.Println("  --goal <v>                处理目标：subtitle|highlights|shorts")
	fmt.Println("  --lang <v>                语言（默认 auto）")
	fmt.Println("  --max-clips <n>           建议片段数（默认 subtitle/highlights=5, shorts=3）")
	fmt.Println("  --clip-seconds <n>        单片段建议时长秒数（默认 subtitle/highlights=45, shorts=30）")
	fmt.Println("  --subtitle-style <v>      字幕模板风格：clean|shorts（默认 clean）")
	fmt.Println("  --json                    输出 JSON 结果")
	fmt.Println()
	fmt.Println("export 参数:")
	fmt.Println("  --to <v>                  目标软件：premiere|resolve|capcut（jianying 也可）")
	fmt.Println("  --with <srt,edl,csv,fcpxml> 导出内容（默认 premiere/resolve=fcpxml,srt；capcut=srt,csv）")
	fmt.Println("  --out-dir <dir>           导出目录（默认素材目录下 .mingest/export）")
	fmt.Println("  --zip                     额外打包 zip")
	fmt.Println("  --json                    输出 JSON 结果")
	fmt.Println()
	fmt.Println("ls 参数:")
	fmt.Println("  --limit <n>               最多返回 n 条（默认 20）")
	fmt.Println("  --query <text>            关键字过滤（匹配 asset_id/url/title/path/platform）")
	fmt.Println("  --format <table|json>     输出格式（默认 table）")
	fmt.Println("  --dedupe                  按 asset_id 去重（仅保留最新一条）")
	fmt.Println()
	fmt.Println("doctor 参数:")
	fmt.Println("  --target <v>              发布目标：youtube|bilibili|shorts（默认 youtube）")
	fmt.Println("  --strict                  启用更严格阈值")
	fmt.Println("  --json                    输出 JSON 诊断结果")
	fmt.Println()
	fmt.Println("semantic 参数:")
	fmt.Println("  --target <v>              目标场景：youtube|bilibili|shorts（默认 shorts）")
	fmt.Println("  --provider <v>            LLM 提供方：auto|openai|openrouter（默认 auto）")
	fmt.Println("  --model <v>               模型名（默认 openai: gpt-4.1-mini / openrouter: openai/gpt-4.1-mini）")
	fmt.Println("  --base-url <url>          自定义 OpenAI 兼容网关地址（可用于 OpenRouter）")
	fmt.Println("  --api-key <key>           API Key（也可通过环境变量注入）")
	fmt.Println("  --candidate-limit <n>     Stage A 候选上限（默认 20）")
	fmt.Println("  --preview-limit <n>       Stage D 预览数量（默认 8）")
	fmt.Println("  --visual-diversity <0-1>  视觉去重强度（默认 0.5，越大越严格）")
	fmt.Println("  --top-k <n>               Stage C/E 最终片段数（默认 3）")
	fmt.Println("  --no-llm                  跳过 Stage B，仅使用规则分")
	fmt.Println("  --decisions <path>        Stage E 使用指定评审决策文件")
	fmt.Println("  --apply                   Stage E：写回 prep-plan 并执行 doctor 闸门")
	fmt.Println("  --strict                  Stage E doctor 使用严格阈值")
	fmt.Println("  --json                    输出 JSON 结果")
	fmt.Println()
	fmt.Println("平台:")
	fmt.Println("  - youtube")
	fmt.Println("  - bilibili")
	fmt.Println()
	fmt.Println("行为:")
	fmt.Println("  - 自动检测并调用 yt-dlp / ffmpeg / ffprobe / deno|node")
	fmt.Println("  - 自动维护 cookies 缓存（优先使用；必要时从浏览器读取 cookies 刷新账户登录信息）")
	fmt.Println("  - 若 Windows 下 Chrome cookies 读取/解密失败，可用 `mingest auth <platform>`（CDP）准备工具专用账户登录信息")
	fmt.Println()
	fmt.Println("可选环境变量:")
	fmt.Println("  - MINGEST_BROWSER=chrome|firefox|chromium|edge")
	fmt.Println("  - MINGEST_BROWSER_PROFILE=Default|Profile 1|...")
	fmt.Println("  - MINGEST_JS_RUNTIME=node|deno")
	fmt.Println("  - MINGEST_CHROME_PATH=C:\\\\Path\\\\To\\\\chrome.exe")
	fmt.Println("  - MINGEST_WHISPER_PATH=/path/to/whisper")
	fmt.Println("  - MINGEST_WHISPER_MODEL=tiny|base|small|medium|large")
	fmt.Println("  - MINGEST_OPENAI_API_KEY / OPENAI_API_KEY")
	fmt.Println("  - MINGEST_OPENROUTER_API_KEY / OPENROUTER_API_KEY")
	fmt.Println("  - MINGEST_OPENROUTER_BASE_URL=https://openrouter.ai/api/v1")
	fmt.Println("  - MINGEST_LLM_MODEL=gpt-4.1-mini|openai/gpt-4.1-mini")
	fmt.Println("  - MINGEST_LOG_LEVEL=debug|info|warn|error（默认 info）")
	fmt.Println("  - MINGEST_LOG_FORMAT=text|json（默认 text）")
	fmt.Println()
	fmt.Println("退出码:")
	fmt.Println("  - 20: 需要登录（AUTH_REQUIRED）")
	fmt.Println("  - 21: cookies 读取/解密问题（COOKIE_PROBLEM）")
	fmt.Println("  - 30: JS runtime 缺失（RUNTIME_MISSING）")
	fmt.Println("  - 31: ffmpeg 缺失（FFMPEG_MISSING）")
	fmt.Println("  - 32: yt-dlp 缺失（YTDLP_MISSING）")
	fmt.Println("  - 40: 下载失败（DOWNLOAD_FAILED）")
	fmt.Println("  - 41: doctor 检查未通过（DOCTOR_FAILED）")
	fmt.Println("  - 42: semantic 流程执行失败（SEMANTIC_FAILED）")
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
	case "-v", "--version", "version":
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
		case arg == "--out-dir":
			if i+1 >= len(args) {
				return getOptions{}, fmt.Errorf("`--out-dir` 缺少参数")
			}
			i++
			opts.OutDir = strings.TrimSpace(args[i])
			outDirProvided = true
		case strings.HasPrefix(arg, "--out-dir="):
			opts.OutDir = strings.TrimSpace(strings.TrimPrefix(arg, "--out-dir="))
			outDirProvided = true
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

	if strings.TrimSpace(opts.TargetURL) == "" {
		return getOptions{}, fmt.Errorf("缺少 URL。用法: mingest get <url>")
	}
	if opts.AssetIDOnly && opts.JSON {
		return getOptions{}, fmt.Errorf("`--asset-id-only` 与 `--json` 不能同时使用")
	}
	if outDirProvided && strings.TrimSpace(opts.OutDir) == "" {
		return getOptions{}, fmt.Errorf("`--out-dir` 不能为空")
	}
	if nameTemplateProvided && strings.TrimSpace(opts.NameTemplate) == "" {
		return getOptions{}, fmt.Errorf("`--name-template` 不能为空")
	}
	return opts, nil
}

func parseLsOptions(args []string) (lsOptions, error) {
	opts := lsOptions{
		Limit:  20,
		Format: "table",
	}

	var limitProvided bool
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		switch {
		case arg == "--dedupe":
			opts.Dedupe = true
		case arg == "--limit":
			if i+1 >= len(args) {
				return lsOptions{}, fmt.Errorf("`--limit` 缺少参数")
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
		case arg == "--query":
			if i+1 >= len(args) {
				return lsOptions{}, fmt.Errorf("`--query` 缺少参数")
			}
			i++
			opts.Query = strings.TrimSpace(args[i])
		case strings.HasPrefix(arg, "--query="):
			opts.Query = strings.TrimSpace(strings.TrimPrefix(arg, "--query="))
		case arg == "--format":
			if i+1 >= len(args) {
				return lsOptions{}, fmt.Errorf("`--format` 缺少参数")
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
	if limitProvided && opts.Limit <= 0 {
		return lsOptions{}, fmt.Errorf("`--limit` 必须大于 0")
	}
	return opts, nil
}

func runGet(opts getOptions) int {
	u, err := validateURL(opts.TargetURL)
	if err != nil {
		if opts.JSON {
			printGetJSON(getJSONResult{
				OK:       false,
				ExitCode: exitUsage,
				Error:    fmt.Sprintf("输入的 URL 无效: %v", err),
			})
		}
		logError("get.url_invalid", "url", opts.TargetURL, "error", err)
		return exitUsage
	}

	outputTemplate, outputDir, err := resolveGetOutput(opts.OutDir, opts.NameTemplate)
	if err != nil {
		if opts.JSON {
			printGetJSON(getJSONResult{
				OK:       false,
				ExitCode: exitUsage,
				Error:    err.Error(),
			})
		}
		logError("get.output_options_invalid", "out_dir", opts.OutDir, "name_template", opts.NameTemplate, "error", err)
		return exitUsage
	}

	found, err := detectDeps()
	if err != nil {
		var depErr dependencyError
		if errors.As(err, &depErr) {
			if opts.JSON {
				printGetJSON(getJSONResult{
					OK:       false,
					ExitCode: depErr.ExitCode,
					Error:    depErr.Message,
				})
			}
			logError("deps.validation_failed", "exit_code", depErr.ExitCode, "detail", depErr.Message)
			return depErr.ExitCode
		}
		if opts.JSON {
			printGetJSON(getJSONResult{
				OK:       false,
				ExitCode: exitDownloadFailed,
				Error:    fmt.Sprintf("依赖检测失败: %v", err),
			})
		}
		logError("deps.detect_failed", "error", err)
		return exitDownloadFailed
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
		Quiet:            opts.JSON,
		ProgressOnly:     opts.AssetIDOnly && !opts.JSON,
	}
	code, movedPaths := runWithAuthFallback(opts.TargetURL, found, p, authSources, cookieFile, cfg)
	if code != exitOK {
		if opts.JSON {
			printGetJSON(getJSONResult{
				OK:           false,
				ExitCode:     code,
				Error:        "下载失败",
				URL:          opts.TargetURL,
				Platform:     strings.TrimSpace(p.ID),
				OutputDir:    outputDir,
				NameTemplate: outputTemplate,
			})
		}
		return code
	}

	if !captureOutput {
		return exitOK
	}

	outputPath := firstCapturedPath(movedPaths)
	if outputPath == "" {
		msg := "下载成功，但未能解析输出文件路径"
		if !opts.AssetIDOnly && !opts.JSON {
			logWarn("get.output_path_missing", "action", "skip_asset_index")
			return exitOK
		}
		if opts.JSON {
			printGetJSON(getJSONResult{
				OK:           false,
				ExitCode:     exitDownloadFailed,
				Error:        msg,
				URL:          opts.TargetURL,
				Platform:     strings.TrimSpace(p.ID),
				OutputDir:    outputDir,
				NameTemplate: outputTemplate,
			})
		}
		logError("get.output_path_missing", "error", msg)
		return exitDownloadFailed
	}

	assetID, err := computeAssetID(outputPath)
	if err != nil {
		msg := fmt.Sprintf("生成 asset_id 失败: %v", err)
		if opts.JSON {
			printGetJSON(getJSONResult{
				OK:           false,
				ExitCode:     exitDownloadFailed,
				Error:        msg,
				URL:          opts.TargetURL,
				Platform:     strings.TrimSpace(p.ID),
				OutputPath:   outputPath,
				OutputDir:    outputDir,
				NameTemplate: outputTemplate,
			})
		}
		logError("asset_id.compute_failed", "path", outputPath, "error", err)
		return exitDownloadFailed
	}

	if opts.AssetIDOnly {
		if err := appendAssetRecord(assetRecord{
			AssetID:    assetID,
			URL:        opts.TargetURL,
			Platform:   strings.TrimSpace(p.ID),
			Title:      filepath.Base(outputPath),
			OutputPath: outputPath,
			CreatedAt:  time.Now().UTC().Format(time.RFC3339),
		}); err != nil {
			logWarn("asset_index.append_failed", "error", err, "asset_id", assetID)
		}
		fmt.Println(assetID)
		return exitOK
	}

	if err := appendAssetRecord(assetRecord{
		AssetID:    assetID,
		URL:        opts.TargetURL,
		Platform:   strings.TrimSpace(p.ID),
		Title:      filepath.Base(outputPath),
		OutputPath: outputPath,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		logWarn("asset_index.append_failed", "error", err, "asset_id", assetID)
	}

	if opts.JSON {
		printGetJSON(getJSONResult{
			OK:           true,
			ExitCode:     exitOK,
			URL:          opts.TargetURL,
			Platform:     strings.TrimSpace(p.ID),
			OutputPath:   outputPath,
			AssetID:      assetID,
			OutputDir:    outputDir,
			NameTemplate: outputTemplate,
		})
	}

	return exitOK
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
		return "", "", fmt.Errorf("`--name-template` 为绝对路径时，不可再配合 `--out-dir` 使用")
	}

	return filepath.Join(absDir, tpl), absDir, nil
}

func runLs(opts lsOptions) int {
	records, err := readAssetRecords()
	if err != nil {
		logError("asset_index.read_failed", "error", err)
		return exitDownloadFailed
	}

	filtered := filterAssetRecords(records, opts.Query)
	sort.Slice(filtered, func(i, j int) bool {
		return parseRecordTime(filtered[i]).After(parseRecordTime(filtered[j]))
	})
	if opts.Dedupe {
		filtered = dedupeAssetRecords(filtered)
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

func filterAssetRecords(in []assetRecord, query string) []assetRecord {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		out := make([]assetRecord, len(in))
		copy(out, in)
		return out
	}

	out := make([]assetRecord, 0, len(in))
	for _, r := range in {
		haystack := strings.ToLower(strings.Join([]string{
			r.AssetID,
			r.URL,
			r.Platform,
			r.Title,
			r.OutputPath,
		}, " "))
		if strings.Contains(haystack, q) {
			out = append(out, r)
		}
	}
	return out
}

func dedupeAssetRecords(in []assetRecord) []assetRecord {
	seen := make(map[string]struct{}, len(in))
	out := make([]assetRecord, 0, len(in))
	for _, r := range in {
		id := strings.TrimSpace(r.AssetID)
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

func parseRecordTime(r assetRecord) time.Time {
	if t, err := time.Parse(time.RFC3339, strings.TrimSpace(r.CreatedAt)); err == nil {
		return t
	}
	return time.Time{}
}

func printAssetTable(records []assetRecord) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ASSET_ID\tPLATFORM\tCREATED_AT\tTITLE\tPATH")
	for _, r := range records {
		title := strings.TrimSpace(r.Title)
		if title == "" {
			title = filepath.Base(r.OutputPath)
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			r.AssetID,
			r.Platform,
			r.CreatedAt,
			title,
			r.OutputPath,
		)
	}
	_ = w.Flush()
}

func assetsIndexFilePath() (string, error) {
	base, err := appStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "assets-v1.jsonl"), nil
}

func appendAssetRecord(rec assetRecord) error {
	indexPath, err := assetsIndexFilePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o700); err != nil {
		return err
	}

	normalized := rec
	if strings.TrimSpace(normalized.CreatedAt) == "" {
		normalized.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if strings.TrimSpace(normalized.Title) == "" {
		normalized.Title = filepath.Base(normalized.OutputPath)
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

func readAssetRecords() ([]assetRecord, error) {
	indexPath, err := assetsIndexFilePath()
	if err != nil {
		return nil, err
	}
	if !fileExists(indexPath) {
		return []assetRecord{}, nil
	}

	f, err := os.Open(indexPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := make([]assetRecord, 0, 64)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec assetRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if strings.TrimSpace(rec.AssetID) == "" || strings.TrimSpace(rec.OutputPath) == "" {
			continue
		}
		if strings.TrimSpace(rec.Title) == "" {
			rec.Title = filepath.Base(rec.OutputPath)
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

func printGetJSON(v getJSONResult) {
	data, err := json.Marshal(v)
	if err != nil {
		logError("json.marshal_failed", "context", "get_result", "error", err)
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

	// Prefer current working directory (where users typically place the tool bundle),
	// then the executable directory, then PATH.
	ytPath, ok := findBinary("yt-dlp", wd, exeDir)
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

	jsID := ""
	jsPath := ""
	requestedRuntime := strings.ToLower(strings.TrimSpace(os.Getenv("MINGEST_JS_RUNTIME")))
	switch requestedRuntime {
	case "":
		// default: prefer deno first (bundled), then node
		if denoPath, exists := findBinary("deno", wd, exeDir); exists {
			jsID = "deno"
			jsPath = denoPath
		} else if nodePath, exists := findBinary("node", wd, exeDir); exists {
			jsID = "node"
			jsPath = nodePath
		}
	case "deno", "node":
		if p, exists := findBinary(requestedRuntime, wd, exeDir); exists {
			jsID = requestedRuntime
			jsPath = p
		} else {
			return deps{}, dependencyError{
				Message:  fmt.Sprintf("未找到指定 JS runtime: %s。请将其放在程序同目录，或加入 PATH。", requestedRuntime),
				ExitCode: exitRuntimeMissing,
			}
		}
	default:
		return deps{}, dependencyError{
			Message:  fmt.Sprintf("无效的 MINGEST_JS_RUNTIME: %s（仅支持 node 或 deno）", requestedRuntime),
			ExitCode: exitRuntimeMissing,
		}
	}

	if jsID == "" || jsPath == "" {
		return deps{}, dependencyError{
			Message:  "未找到 JS runtime（deno 或 node）。请将 deno/node 放在程序同目录，或加入 PATH。",
			ExitCode: exitRuntimeMissing,
		}
	}

	return deps{
		YtDlp:       tool{Name: "yt-dlp", Path: ytPath},
		FFmpeg:      tool{Name: "ffmpeg", Path: ffmpegPath},
		FFprobe:     tool{Name: "ffprobe", Path: ffprobePath},
		JSRuntime:   tool{Name: jsID, Path: jsPath},
		JSRuntimeID: jsID,
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
	// 优先查找嵌入的二进制文件
	if path, ok := embedtools.Find(name); ok {
		return path, true
	}

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
	if v := strings.TrimSpace(os.Getenv("MINGEST_BROWSER")); v != "" {
		lower := strings.ToLower(v)
		return []authSource{{Kind: authKindBrowser, Value: lower}}
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

func runWithAuthFallback(targetURL string, d deps, platform videoPlatform, sources []authSource, cookieFile string, cfg ytDlpConfig) (int, []string) {
	// 0) Fast path: try cached cookies first (no browser DB access).
	if strings.TrimSpace(cookieFile) != "" {
		logInfo("auth.method_selected", "source", "cookie_cache")
		code, paths := runYtDlp(d, buildYtDlpArgsWithCookiesFile(targetURL, d, cookieFile, cfg), platform, cfg)
		// Always attempt to filter after yt-dlp touches the cookie jar.
		if fileExists(cookieFile) {
			if err := filterCookieFileForPlatform(cookieFile, platform); err != nil {
				logWarn("auth.cookie_filter_failed", "error", err, "path", cookieFile)
			}
		}
		if code == exitOK {
			return exitOK, paths
		}
		// If it's not an auth/cookie issue, browser fallbacks won't help.
		if !shouldTryNextAuth(code) {
			return code, nil
		}
	}

	if len(sources) == 0 {
		return exitAuthRequired, nil
	}

	lastCode := exitDownloadFailed
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
		code, paths := runYtDlp(d, args, platform, cfg)
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
		if code == exitOK {
			if i > 0 && strings.TrimSpace(os.Getenv("MINGEST_BROWSER")) == "" {
				logInfo("auth.browser_auto_switched", "browser", src.Value, "env", "MINGEST_BROWSER")
			}
			return code, paths
		}
		// Prefer Chrome, but on Windows Chrome cookie decryption frequently fails.
		// When chrome fails, try CDP (Chrome gives us decrypted cookies) before falling back to Firefox.
		if src.Kind == authKindBrowser && src.Value == "chrome" && shouldTryNextAuth(code) {
			logWarn("auth.chrome_cookie_failed_try_cdp")
			cdpCode, cdpPaths := tryDownloadWithChromeCDP(targetURL, d, platform, cookieFile, cfg)
			if cdpCode == exitOK {
				if strings.TrimSpace(cookieFile) != "" && fileExists(cookieFile) {
					if err := filterCookieFileForPlatform(cookieFile, platform); err != nil {
						logWarn("auth.cookie_filter_failed", "error", err, "path", cookieFile)
					}
				}
				return exitOK, cdpPaths
			}
			// If CDP cannot provide a working session, guide the user to prepare the managed profile.
			if cdpCode == exitAuthRequired {
				cmd := "mingest auth <platform>"
				if strings.TrimSpace(platform.ID) != "" {
					cmd = "mingest auth " + platform.ID
				}
				logWarn("auth.cdp_session_not_authorized", "recommended_command", cmd)
				// Keep classification as AUTH_REQUIRED so callers can decide what to do.
				code = exitAuthRequired
			} else if cdpCode == exitCookieProblem {
				code = exitCookieProblem
			}
		}

		lastCode = code

		if i < len(sources)-1 && shouldTryNextAuth(code) {
			logWarn("auth.method_failed_try_next", "exit_code", code)
			continue
		}
		break
	}

	if shouldTryNextAuth(lastCode) {
		logError("auth.no_valid_session")
		logError("auth.recovery_hint", "hint", "login in browser and retry")
		logError("auth.recovery_hint", "hint", "MINGEST_BROWSER=firefox mingest get <url>")
		if strings.TrimSpace(platform.ID) != "" {
			logError("auth.recovery_hint", "hint", "run auth first", "command", "mingest auth "+platform.ID)
		} else {
			logError("auth.recovery_hint", "hint", "run auth first", "command", "mingest auth <platform>")
		}
		return exitAuthRequired, nil
	}
	return lastCode, nil
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
		if p := strings.TrimSpace(os.Getenv("MINGEST_BROWSER_PROFILE")); p != "" {
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
		if p := strings.TrimSpace(os.Getenv("MINGEST_BROWSER_PROFILE")); p != "" {
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

func runYtDlp(d deps, args []string, platform videoPlatform, cfg ytDlpConfig) (int, []string) {
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		logError("yt_dlp.stdout_pipe_create_failed", "error", err)
		return exitDownloadFailed, nil
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		_ = stdoutR.Close()
		_ = stdoutW.Close()
		logError("yt_dlp.stderr_pipe_create_failed", "error", err)
		return exitDownloadFailed, nil
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
		return exitDownloadFailed, nil
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
		return exitDownloadFailed, nil
	}
	if state.Success() {
		return exitOK, extractMovedPaths(stdoutBuf.String(), cfg.CaptureMovedPath)
	}

	code, hint := classifyFailure(combined, platform)
	if hint != "" {
		logWarn("yt_dlp.failure_hint", "hint", hint)
	}
	if code == exitDownloadFailed {
		logError("yt_dlp.exit_code_unexpected", "exit_code", state.ExitCode())
	}

	return code, nil
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

func classifyFailure(output string, platform videoPlatform) (int, string) {
	lower := strings.ToLower(output)

	authCmd := "mingest auth <platform>"
	if strings.TrimSpace(platform.ID) != "" {
		authCmd = "mingest auth " + platform.ID
	}
	name := strings.TrimSpace(platform.Name)
	if name == "" {
		name = strings.TrimSpace(platform.ID)
	}

	if strings.Contains(lower, "could not copy") && strings.Contains(lower, "cookie database") {
		return exitCookieProblem, fmt.Sprintf("浏览器 cookies 数据库无法读取（常见原因: 浏览器仍在占用 cookies 数据库）。请先彻底退出浏览器（含后台进程）后重试；或改用 Firefox；或执行 `%s`（使用 CDP 从浏览器进程内导出 cookies，避免读取数据库）。", authCmd)
	}

	if strings.Contains(lower, "failed to decrypt with dpapi") {
		return exitCookieProblem, fmt.Sprintf("浏览器 cookies 解密失败。请改用 Firefox，或执行 `%s`。", authCmd)
	}

	// Chrome's App-Bound Cookie Encryption on Windows intentionally makes third-party decryption harder.
	// When enabled, tools that read/decrypt the cookie DB may fail even with admin rights.
	if strings.Contains(lower, "app-bound") && strings.Contains(lower, "cookie") && strings.Contains(lower, "encrypt") {
		return exitCookieProblem, fmt.Sprintf("检测到 Chrome App-Bound Cookie Encryption 相关错误。此模式下第三方工具可能无法直接解密 Chrome cookies。建议改用 `%s`（CDP 方式）或改用 Firefox/Edge 的账户登录信息。", authCmd)
	}

	if strings.Contains(lower, "permission denied") && strings.Contains(lower, "cookies") {
		return exitCookieProblem, "读取浏览器 cookies 被拒绝。请检查浏览器进程占用与文件权限。"
	}

	if strings.Contains(lower, "cannot decrypt v11 cookies: no key found") {
		return exitCookieProblem, fmt.Sprintf("浏览器 cookies 解密失败（keyring 不可用）。如果你是 SSH 会话，请在本机桌面终端运行，或改用 Firefox，或执行 `%s`。", authCmd)
	}

	if strings.Contains(lower, "sign in to confirm you're not a bot") ||
		strings.Contains(lower, "sign in to confirm you’re not a bot") {
		target := "目标网站"
		if name != "" {
			target = name
		}
		return exitAuthRequired, fmt.Sprintf("需要登录 %s。请先在浏览器登录后重试，或执行 `%s`。", target, authCmd)
	}

	if strings.Contains(lower, "sign in to confirm your age") ||
		(strings.Contains(lower, "this video may be inappropriate for some users") && strings.Contains(lower, "sign in")) {
		target := "目标网站"
		if name != "" {
			target = name
		}
		return exitAuthRequired, fmt.Sprintf("需要登录 %s 并完成额外确认。请在浏览器中登录并打开该视频完成确认后重试；或执行 `%s` 使用工具专用账户登录信息。", target, authCmd)
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
			return exitAuthRequired, fmt.Sprintf("需要登录 %s（或账号具备相应权限）。请先在浏览器中登录后重试；或执行 `%s`。", target, authCmd)
		}
	}

	if strings.Contains(lower, "cookies file") && strings.Contains(lower, "netscape") {
		return exitCookieProblem, "cookies 文件格式异常。"
	}

	if strings.Contains(lower, "no supported javascript runtime could be found") {
		return exitRuntimeMissing, "JS runtime 不可用。请确认 deno 或 node 可执行，并可被该程序访问。"
	}

	if strings.Contains(lower, "ffmpeg not found") {
		return exitFFmpegMissing, "ffmpeg 不可用。请将 ffmpeg/ffprobe 放在同一目录（工作目录或程序同目录），或加入 PATH，或改用 *_bundled。"
	}

	if strings.Contains(lower, "ffprobe not found") {
		return exitFFmpegMissing, "ffprobe 不可用。请将 ffmpeg/ffprobe 放在同一目录（工作目录或程序同目录），或加入 PATH，或改用 *_bundled。"
	}

	return exitDownloadFailed, "下载失败。可先执行 `yt-dlp -U` 更新，再检查 cookies 是否过期。"
}
