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
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Runtime self-update for yt-dlp.
//
// Video sites (notably Bilibili) evolve their anti-crawl faster than we cut
// bundled releases, so a frozen yt-dlp goes stale and starts failing (e.g.
// Bilibili returns HTTP 412 on the playurl API). To self-heal, mingest keeps a
// managed copy of yt-dlp under <appStateDir>/tools and can refresh it from the
// official GitHub releases — either on demand (`mingest update`) or
// automatically after a generic download failure.
const (
	// envDisableAutoUpdate opts out of the failure-triggered auto-update.
	envDisableAutoUpdate = "MINGEST_NO_AUTO_UPDATE"

	managedToolsDirName  = "tools"
	ytDlpUpdateStampName = ".yt-dlp-update-check"

	// autoUpdateMinInterval rate-limits the failure-triggered auto-update across
	// processes so a shell loop of failing `mingest get` calls (e.g. a site is
	// genuinely down) doesn't re-download yt-dlp every time.
	autoUpdateMinInterval = 2 * time.Hour

	ytDlpDownloadTimeout = 10 * time.Minute
)

var (
	autoUpdateOnce    sync.Once
	autoUpdatedTool   tool
	autoUpdateChanged bool
)

func managedToolsDir() (string, error) {
	base, err := appStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, managedToolsDirName), nil
}

// managedYtDlpPath is where the self-updated yt-dlp binary lives. It is separate
// from any bundled/embedded copy (which extract.go may cache next to the exe and
// skip overwriting) so updates always take effect.
func managedYtDlpPath() (string, error) {
	dir, err := managedToolsDir()
	if err != nil {
		return "", err
	}
	name := "yt-dlp"
	if runtime.GOOS == "windows" {
		name = "yt-dlp.exe"
	}
	return filepath.Join(dir, name), nil
}

// resolveYtDlpBinary prefers the managed (self-updated) yt-dlp over any bundled
// or PATH copy, so once refreshed it keeps being used.
func resolveYtDlpBinary(preferredDirs ...string) (string, bool) {
	if p, err := managedYtDlpPath(); err == nil && isRunnableFile(p) {
		logDebug("deps.ytdlp_managed_preferred", "path", p)
		return p, true
	}
	return findBinary("yt-dlp", preferredDirs...)
}

// ytDlpReleaseAsset maps the current platform to the yt-dlp release asset name.
// This mirrors scripts/fetch-embed-tools.sh so the self-updated binary matches
// what the bundled packages ship.
func ytDlpReleaseAsset() (string, error) {
	switch runtime.GOOS {
	case "windows":
		return "yt-dlp.exe", nil
	case "darwin":
		return "yt-dlp_macos", nil
	case "linux":
		return "yt-dlp", nil
	default:
		return "", fmt.Errorf("暂不支持在 %s 上自动更新 yt-dlp", runtime.GOOS)
	}
}

func ytDlpLatestURL() (string, error) {
	asset, err := ytDlpReleaseAsset()
	if err != nil {
		return "", err
	}
	return "https://github.com/yt-dlp/yt-dlp/releases/latest/download/" + asset, nil
}

// toolVersion runs `<path> --version` and returns the trimmed output. It doubles
// as a runnability check for a freshly downloaded binary.
func toolVersion(path string) (string, error) {
	cmd := exec.Command(path, "--version")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// updateYtDlp downloads the latest yt-dlp for this platform into the managed
// tools dir. The download is written to a temp file and only promoted to the
// final path after it verifies as runnable, so a partial/corrupt download can't
// clobber a working managed binary. Returns the new version string.
func updateYtDlp() (string, error) {
	dst, err := managedYtDlpPath()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	url, err := ytDlpLatestURL()
	if err != nil {
		return "", err
	}

	tmp, err := os.CreateTemp(dir, "mingest-ytdlp-*.download")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if err := downloadTo(url, tmp); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(tmpPath, 0o755); err != nil {
			return "", err
		}
	}

	version, err := toolVersion(tmpPath)
	if err != nil {
		return "", fmt.Errorf("下载的 yt-dlp 无法运行: %w", err)
	}

	if err := replaceFile(tmpPath, dst); err != nil {
		return "", err
	}
	if runtime.GOOS != "windows" {
		_ = os.Chmod(dst, 0o755)
	}
	return version, nil
}

func downloadTo(url string, dst io.Writer) error {
	client := &http.Client{Timeout: ytDlpDownloadTimeout}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "mingest-updater")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载 yt-dlp 失败: HTTP %d", resp.StatusCode)
	}
	if _, err := io.Copy(dst, resp.Body); err != nil {
		return err
	}
	return nil
}

func autoUpdateEnabled() bool {
	switch strings.ToLower(envString(envDisableAutoUpdate)) {
	case "1", "true", "yes", "on":
		return false
	default:
		return true
	}
}

func updateStampPath() (string, error) {
	dir, err := managedToolsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ytDlpUpdateStampName), nil
}

func writeUpdateStamp() {
	p, err := updateStampPath()
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	_ = os.WriteFile(p, []byte(time.Now().UTC().Format(time.RFC3339)), 0o600)
}

func recentlyCheckedForUpdate() bool {
	p, err := updateStampPath()
	if err != nil {
		return false
	}
	info, err := os.Stat(p)
	if err != nil {
		return false
	}
	return time.Since(info.ModTime()) < autoUpdateMinInterval
}

// tryAutoUpdateYtDlp is the failure self-heal path: at most once per process
// (and rate-limited across processes), it fetches the latest yt-dlp and returns
// the managed tool if it is now usable and differs from the currently used one.
func tryAutoUpdateYtDlp(current tool) (tool, bool) {
	if !autoUpdateEnabled() {
		return current, false
	}

	autoUpdateOnce.Do(func() {
		if recentlyCheckedForUpdate() {
			logInfo("yt_dlp.auto_update_skip_recent")
			return
		}
		writeUpdateStamp()

		oldVersion, _ := toolVersion(current.Path)
		logInfo("yt_dlp.auto_update_start", "current_version", oldVersion)

		newVersion, err := updateYtDlp()
		if err != nil {
			logWarn("yt_dlp.auto_update_failed", "error", err)
			return
		}

		managed, err := managedYtDlpPath()
		if err != nil {
			logWarn("yt_dlp.auto_update_path_unavailable", "error", err)
			return
		}
		autoUpdatedTool = tool{Name: "yt-dlp", Path: managed}
		// Retry only makes sense if the binary actually changed: either a newer
		// version, or we switched away from a non-managed (bundled/PATH) copy.
		autoUpdateChanged = newVersion != oldVersion || current.Path != managed
		logInfo("yt_dlp.auto_updated", "old_version", oldVersion, "new_version", newVersion, "changed", autoUpdateChanged)
	})

	if autoUpdateChanged {
		return autoUpdatedTool, true
	}
	return current, false
}

// runUpdate implements `mingest update [yt-dlp]`: force-refresh the managed
// yt-dlp regardless of rate limiting.
func runUpdate(args []string) int {
	for _, a := range args {
		a = strings.ToLower(strings.TrimSpace(a))
		if a == "" || a == "yt-dlp" || a == "ytdlp" {
			continue
		}
		logError("update.unsupported_target", "target", a)
		usage()
		return exitUsage
	}

	oldVersion := currentYtDlpVersionBestEffort()
	logInfo("update.start", "tool", "yt-dlp", "current_version", oldVersion)

	newVersion, err := updateYtDlp()
	if err != nil {
		logError("update.failed", "error", err)
		return exitDownloadFailed
	}
	writeUpdateStamp()

	switch {
	case oldVersion != "" && oldVersion == newVersion:
		fmt.Printf("yt-dlp 已是最新版本: %s\n", newVersion)
	case oldVersion == "":
		fmt.Printf("yt-dlp 已安装: %s\n", newVersion)
	default:
		fmt.Printf("yt-dlp 已更新: %s -> %s\n", oldVersion, newVersion)
	}
	return exitOK
}

func currentYtDlpVersionBestEffort() string {
	wd, _ := os.Getwd()
	exeDir, _ := executableDir()
	p, ok := resolveYtDlpBinary(wd, exeDir)
	if !ok {
		return ""
	}
	v, err := toolVersion(p)
	if err != nil {
		return ""
	}
	return v
}
