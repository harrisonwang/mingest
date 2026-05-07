//go:build darwin && embedtools

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

// macOS embedded tools. These files are expected to exist at build time.
//
// Layout:
//   assets/darwin/yt-dlp
//   assets/darwin/ffmpeg
//   assets/darwin/ffprobe (optional but recommended)
//   assets/darwin/node

//go:embed assets/darwin/*
var embeddedFS embed.FS

var embeddedBinaries = map[string][]byte{}

func init() {
	entries, err := embeddedFS.ReadDir("assets/darwin")
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		b, err := embeddedFS.ReadFile("assets/darwin/" + name)
		if err != nil || len(b) == 0 {
			continue
		}
		key := strings.ToLower(name)
		embeddedBinaries[key] = b
	}
}
