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
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func runFirefoxAuth(platform videoPlatform) int {
	firefoxPath, err := findFirefoxExecutable()
	if err != nil {
		logError("auth.firefox_not_found", "error", err)
		return exitCookieProblem
	}

	name := platform.Name
	if strings.TrimSpace(name) == "" {
		name = platform.ID
	}
	logInfo("auth.firefox_selected", "path", firefoxPath)
	if profile := selectedBrowserProfile(); profile != "" {
		logInfo("auth.firefox_profile_selected", "profile", profile)
	}
	logInfo("auth.user_login_prompt", "platform", name)

	if err := openFirefoxForAuth(firefoxPath, platform); err != nil {
		logError("auth.firefox_open_failed", "error", err)
		return exitCookieProblem
	}

	logInfo("auth.firefox_manual_verification_prompt")
	reader := bufio.NewReader(os.Stdin)
	_, _ = reader.ReadString('\n')

	cookiePath, err := cookiesCacheFilePath(platform)
	if err != nil {
		logError("auth.cookie_cache_path_resolve_failed", "error", err, "platform", platform.ID)
		return exitCookieProblem
	}
	if err := os.MkdirAll(filepath.Dir(cookiePath), 0o700); err != nil {
		logError("auth.cookie_cache_dir_create_failed", "error", err, "path", filepath.Dir(cookiePath))
		return exitCookieProblem
	}
	tmpCookieFile, cleanup, err := createTempCookieJarFile(filepath.Dir(cookiePath))
	if err != nil {
		logError("auth.cookie_temp_create_failed", "error", err)
		return exitCookieProblem
	}
	defer cleanup()

	ytDlpPath, err := detectYtDlpForCookieExport()
	if err != nil {
		logError("deps.yt_dlp_missing", "error", err)
		return exitYtDlpMissing
	}

	if err := exportBrowserCookiesWithYtDlp(ytDlpPath, "firefox", platform, tmpCookieFile); err != nil {
		logWarn("auth.firefox_cookie_export_failed", "error", err)
	}
	if err := filterCookieFileForPlatform(tmpCookieFile, platform); err != nil {
		logError("auth.cookie_filter_failed", "error", err, "path", tmpCookieFile)
		return exitCookieProblem
	}
	ok, err := cookieFileLooksLikeAuthenticated(tmpCookieFile, platform)
	if err != nil {
		logError("auth.cookie_cache_validate_failed", "error", err, "path", tmpCookieFile)
		return exitCookieProblem
	}
	if !ok {
		logError("auth.firefox_no_valid_session", "hint", "请在 Firefox 中登录并完成必要验证，关闭 Firefox 后回到终端按回车")
		return exitAuthRequired
	}
	if err := copyFileAtomic(tmpCookieFile, cookiePath); err != nil {
		logError("auth.cookie_cache_write_failed", "error", err, "path", cookiePath)
		return exitCookieProblem
	}
	logInfo("auth.ready", "path", cookiePath)
	return exitOK
}

func findFirefoxExecutable() (string, error) {
	if p := envString(envFirefoxPath); p != "" {
		if isRunnableFile(p) {
			return p, nil
		}
		return "", fmt.Errorf("FIREFOX_PATH 无效: %s", p)
	}

	switch runtime.GOOS {
	case "windows":
		dirs := []string{
			filepath.Join(os.Getenv("PROGRAMFILES"), "Mozilla Firefox"),
			filepath.Join(os.Getenv("PROGRAMFILES(X86)"), "Mozilla Firefox"),
			filepath.Join(os.Getenv("LOCALAPPDATA"), "Mozilla Firefox"),
		}
		if p, ok := findBinaryPreferPath("firefox", dirs...); ok {
			return p, nil
		}
	case "linux":
		if p, ok := findBinaryPreferPath("firefox", ""); ok {
			return p, nil
		}
	case "darwin":
		candidate := "/Applications/Firefox.app/Contents/MacOS/firefox"
		if isRunnableFile(candidate) {
			return candidate, nil
		}
		if p, ok := findBinaryPreferPath("firefox", ""); ok {
			return p, nil
		}
	}

	return "", errors.New("未找到 Firefox。可通过 FIREFOX_PATH 指定 firefox 可执行文件路径")
}

func openFirefoxForAuth(firefoxPath string, platform videoPlatform) error {
	openURL := strings.TrimSpace(platform.LoginURL)
	if openURL == "" {
		openURL = "about:blank"
	}

	args := []string{firefoxPath}
	if profile := selectedBrowserProfile(); profile != "" {
		args = append(args, "-no-remote")
		if dirExists(profile) {
			args = append(args, "-profile", profile)
		} else {
			args = append(args, "-P", profile)
		}
	}
	args = append(args, openURL)

	proc, err := os.StartProcess(firefoxPath, args, &os.ProcAttr{
		Env: os.Environ(),
		Dir: ".",
		Files: []*os.File{
			os.Stdin,
			os.Stdout,
			os.Stderr,
		},
	})
	if err != nil {
		return err
	}
	return proc.Release()
}

func detectYtDlpForCookieExport() (string, error) {
	exeDir, err := executableDir()
	if err != nil {
		return "", err
	}
	wd, _ := os.Getwd()
	if p, ok := findBinary("yt-dlp", wd, exeDir); ok {
		return p, nil
	}
	return "", errors.New("未找到 yt-dlp。请将 yt-dlp 放在程序同目录，或加入 PATH。")
}

func exportBrowserCookiesWithYtDlp(ytDlpPath, browser string, platform videoPlatform, cookieFile string) error {
	openURL := strings.TrimSpace(platform.LoginURL)
	if openURL == "" {
		openURL = "https://example.com/"
	}

	browserArg := browser
	if profile := selectedBrowserProfile(); profile != "" {
		browserArg += ":" + profile
	}

	args := []string{
		ytDlpPath,
		"--cookies-from-browser", browserArg,
		"--cookies", cookieFile,
		"--skip-download",
		"--simulate",
		"--no-playlist",
		"--force-generic-extractor",
		"--ignore-no-formats-error",
		"--quiet",
	}
	if runtime.GOOS == "windows" {
		args = append(args, "--encoding", "utf-8")
	}
	args = append(args, openURL)

	var stderr bytes.Buffer
	var stdout bytes.Buffer
	cmd := exec.Command(ytDlpPath, args[1:]...)
	cmd.Env = withEnvVar(withEnvVar(os.Environ(), "PYTHONUTF8", "1"), "PYTHONIOENCODING", "utf-8")
	cmd.Dir = "."
	cmd.Stdin = os.Stdin
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err == nil {
		return nil
	} else if stderr.Len() > 0 {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	} else {
		return err
	}
}
