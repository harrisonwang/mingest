//go:build windows && embedtools

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
	"embed"
	"strings"
)

// Windows embedded tools. These files are expected to exist at build time.
//
// Layout:
//   assets/windows/yt-dlp.exe
//   assets/windows/ffmpeg.exe
//   assets/windows/ffprobe.exe (optional but recommended)
//   assets/windows/deno.exe

//go:embed assets/windows/*
var embeddedFS embed.FS

// embeddedBinaries is populated from embeddedFS at init time.
// Keys are canonical tool names without extension (e.g. "ffmpeg", "ffprobe").
var embeddedBinaries = map[string][]byte{}

func init() {
	entries, err := embeddedFS.ReadDir("assets/windows")
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		b, err := embeddedFS.ReadFile("assets/windows/" + name)
		if err != nil || len(b) == 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSuffix(name, ".exe"))
		embeddedBinaries[key] = b
	}
}
