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
	"path/filepath"
	"strings"
)

func cookiesCacheFilePath(p videoPlatform) (string, error) {
	if strings.TrimSpace(p.ID) == "" {
		return "", fmt.Errorf("platform id is empty")
	}
	base, err := appStateDir()
	if err != nil {
		return "", err
	}

	// Backward compatibility: keep the YouTube filename stable.
	if p.ID == "youtube" {
		return filepath.Join(base, "youtube-cookies.txt"), nil
	}

	return filepath.Join(base, p.ID+"-cookies.txt"), nil
}
