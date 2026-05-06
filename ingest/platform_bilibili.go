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

func bilibiliPlatform() videoPlatform {
	return videoPlatform{
		ID:   "bilibili",
		Name: "Bilibili",
		MatchHosts: []string{
			"bilibili.com",
			"b23.tv",
		},
		LoginURL: "https://www.bilibili.com",
		CookieDomainSuffixes: []string{
			"bilibili.com",
		},
		// Signal cookie: SESSDATA is the session token used for logged-in access.
		AuthCookieNames: []string{
			"SESSDATA",
		},
	}
}
