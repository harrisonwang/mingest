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

package embedtools

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// Optional embedded tool binaries.
//
// Default builds do not embed any tools. When built with `-tags embedtools`,
// platform-specific go:embed declarations (see assets_*.go) provide the bytes
// for yt-dlp/ffmpeg/node.

var (
	extractOnce sync.Once
	extractDir  string
	extractErr  error
)

// extractEmbeddedBinaries extracts embedded binaries to a writable directory.
// 优先提取到程序同目录，如果不可写则回退到临时目录
func extractEmbeddedBinaries() (string, error) {
	extractOnce.Do(func() {
		// 优先尝试提取到程序同目录
		exeDir, err := executableDirForEmbed()
		if err == nil {
			// 检查程序目录是否可写
			testFile := filepath.Join(exeDir, ".mingest-write-test")
			if err := os.WriteFile(testFile, []byte("test"), 0644); err == nil {
				os.Remove(testFile) // 清理测试文件
				extractDir = exeDir
				// 提取到程序目录
				if err := extractToDir(exeDir); err == nil {
					return // 成功提取到程序目录
				}
				// 如果提取失败，继续尝试临时目录
			}
		}

		// 回退到临时目录
		tmpDir, err := os.MkdirTemp("", "mingest-embedded-*")
		if err != nil {
			extractErr = fmt.Errorf("创建临时目录失败: %w", err)
			return
		}
		extractDir = tmpDir
		if err := extractToDir(tmpDir); err != nil {
			os.RemoveAll(tmpDir)
			extractErr = err
		}
	})

	return extractDir, extractErr
}

// extractToDir 将嵌入的文件提取到指定目录
func extractToDir(targetDir string) error {
	for name, data := range embeddedBinaries {
		// 跳过空文件（未嵌入的文件）
		if len(data) == 0 {
			continue
		}

		// 根据平台确定文件名
		binaryName := name
		if runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(name), ".exe") {
			binaryName = name + ".exe"
		}

		outputPath := filepath.Join(targetDir, binaryName)

		// 检查文件是否已存在（避免重复提取）
		if info, err := os.Stat(outputPath); err == nil && !info.IsDir() {
			// 文件已存在，跳过提取
			continue
		}

		if err := os.WriteFile(outputPath, data, 0755); err != nil {
			return fmt.Errorf("写入文件 %s 失败: %w", binaryName, err)
		}

		// Windows 不需要设置可执行权限，但其他平台需要
		if runtime.GOOS != "windows" {
			if err := os.Chmod(outputPath, 0755); err != nil {
				return fmt.Errorf("设置执行权限失败 %s: %w", binaryName, err)
			}
		}
	}
	return nil
}

// Find returns the extracted path for an embedded binary if it exists.
func Find(name string) (string, bool) {
	// 检查是否在嵌入列表中，且文件不为空
	var embeddedName string
	for k, data := range embeddedBinaries {
		// 跳过空文件（未嵌入的文件）
		if len(data) == 0 {
			continue
		}
		baseName := k
		if runtime.GOOS == "windows" {
			baseName = strings.TrimSuffix(strings.ToLower(k), ".exe")
		}
		if strings.EqualFold(baseName, name) {
			embeddedName = k
			break
		}
	}

	if embeddedName == "" {
		return "", false
	}

	// 提取嵌入文件
	extractDir, err := extractEmbeddedBinaries()
	if err != nil {
		return "", false
	}

	// 确定输出文件名
	binaryName := name
	if runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(name), ".exe") {
		binaryName = name + ".exe"
	}

	outputPath := filepath.Join(extractDir, binaryName)
	if _, err := os.Stat(outputPath); err == nil {
		return outputPath, true
	}

	return "", false
}

// executableDirForEmbed 获取程序所在目录（用于 embed）
func executableDirForEmbed() (string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return "", err
	}
	// 如果是符号链接，解析真实路径
	realPath, err := filepath.EvalSymlinks(exePath)
	if err == nil {
		exePath = realPath
	}
	return filepath.Dir(exePath), nil
}

// Cleanup removes extracted temp files (only when extracted to a temp dir).
// 注意：如果文件提取到程序同目录，不会自动删除（可缓存复用）
// 只有提取到临时目录时才会清理
func Cleanup() {
	if extractDir == "" {
		return
	}

	// 检查是否是临时目录（包含 "mingest-embedded-" 且不在程序目录）
	exeDir, _ := executableDirForEmbed()
	if exeDir != "" && extractDir == exeDir {
		// 提取到程序目录，不删除（保留文件以便下次使用）
		return
	}

	// 是临时目录，清理它
	if strings.Contains(extractDir, "mingest-embedded-") {
		os.RemoveAll(extractDir)
	}
}
