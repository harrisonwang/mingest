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
	"os"
	"strings"
)

const (
	envBrowser        = "BROWSER"
	envBrowserProfile = "BROWSER_PROFILE"
	envChromePath     = "CHROME_PATH"
	envFirefoxPath    = "FIREFOX_PATH"
	envLogLevel       = "LOG_LEVEL"
	envLogFormat      = "LOG_FORMAT"
)

func envString(name string) string {
	return strings.TrimSpace(os.Getenv(name))
}

func selectedBrowserEnv() string {
	return strings.ToLower(envString(envBrowser))
}

func selectedBrowserProfile() string {
	return envString(envBrowserProfile)
}

func isSupportedBrowserName(browser string) bool {
	switch strings.ToLower(strings.TrimSpace(browser)) {
	case "chrome", "firefox", "chromium", "edge":
		return true
	default:
		return false
	}
}

func isSupportedAuthLoginBrowser(browser string) bool {
	switch strings.ToLower(strings.TrimSpace(browser)) {
	case "chrome", "firefox":
		return true
	default:
		return false
	}
}
