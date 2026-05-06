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
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func runAuth(platform videoPlatform) int {
	chromePath, err := findChromeExecutable()
	if err != nil {
		logError("auth.chrome_not_found", "error", err)
		return exitCookieProblem
	}
	profileDir, err := chromeProfileDir()
	if err != nil {
		logError("auth.chrome_profile_path_resolve_failed", "error", err)
		return exitCookieProblem
	}
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		logError("auth.chrome_profile_dir_create_failed", "error", err, "path", profileDir)
		return exitCookieProblem
	}

	logInfo("auth.chrome_selected", "path", chromePath)
	logInfo("auth.chrome_profile_selected", "path", profileDir)
	name := platform.Name
	if strings.TrimSpace(name) == "" {
		name = platform.ID
	}
	logInfo("auth.user_login_prompt", "platform", name)

	cookies, err := chromeAuthViaCDP(chromePath, profileDir, platform)
	if err != nil {
		logError("auth.cdp_login_failed", "error", err, "platform", platform.ID)
		return exitAuthRequired
	}

	cookiePath, err := cookiesCacheFilePath(platform)
	if err != nil {
		logError("auth.cookie_cache_path_resolve_failed", "error", err, "platform", platform.ID)
		return exitCookieProblem
	}
	// Best effort: keep the cookie file private. Windows ignores chmod.
	if err := writeNetscapeCookieFile(cookiePath, cookies, platform.AllowsCookieDomain); err != nil {
		logError("auth.cookie_cache_write_failed", "error", err, "path", cookiePath)
		return exitCookieProblem
	}
	_ = os.Chmod(cookiePath, 0o600)

	logInfo("auth.ready", "path", cookiePath)
	return exitOK
}

func tryDownloadWithChromeCDP(targetURL string, d deps, platform videoPlatform, cookieCacheFile string, cfg ytDlpConfig) (int, []string) {
	chromePath, err := findChromeExecutable()
	if err != nil {
		logWarn("auth.chrome_not_found", "error", err)
		return exitCookieProblem, nil
	}
	profileDir, err := chromeProfileDir()
	if err != nil {
		logWarn("auth.chrome_profile_path_resolve_failed", "error", err)
		return exitCookieProblem, nil
	}

	cookieFile, cleanup, cookies, err := exportCookiesFromChromeCDP(chromePath, profileDir, platform, true)
	if err != nil {
		logWarn("auth.cdp_cookie_export_failed", "error", err)
		return exitCookieProblem, nil
	}
	defer cleanup()

	if !looksLikeLoggedIn(cookies, platform) {
		// This is a stronger signal than inferring from yt-dlp output: we didn't even get auth cookies.
		return exitAuthRequired, nil
	}

	// Best-effort: refresh the persistent cache so subsequent `mingest get` runs can use it directly.
	if strings.TrimSpace(cookieCacheFile) != "" {
		_ = os.MkdirAll(filepath.Dir(cookieCacheFile), 0o700)
		if err := writeNetscapeCookieFile(cookieCacheFile, cookies, platform.AllowsCookieDomain); err == nil {
			_ = os.Chmod(cookieCacheFile, 0o600)
		}
	}

	args := buildYtDlpArgsWithCookiesFile(targetURL, d, cookieFile, cfg)
	return runYtDlp(d, args, platform, cfg)
}

func chromeProfileDir() (string, error) {
	base, err := appStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "chrome-profile"), nil
}

func appStateDir() (string, error) {
	// Prefer LocalAppData on Windows since this is large, non-roaming state.
	if runtime.GOOS == "windows" {
		if v := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); v != "" {
			return filepath.Join(v, "mingest"), nil
		}
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "mingest"), nil
}

func findChromeExecutable() (string, error) {
	if p := strings.TrimSpace(os.Getenv("MINGEST_CHROME_PATH")); p != "" {
		if isRunnableFile(p) {
			return p, nil
		}
		return "", fmt.Errorf("MINGEST_CHROME_PATH 无效: %s", p)
	}

	switch runtime.GOOS {
	case "windows":
		dirs := []string{
			filepath.Join(os.Getenv("PROGRAMFILES"), "Google", "Chrome", "Application"),
			filepath.Join(os.Getenv("PROGRAMFILES(X86)"), "Google", "Chrome", "Application"),
			filepath.Join(os.Getenv("LOCALAPPDATA"), "Google", "Chrome", "Application"),
		}
		if p, ok := findBinaryPreferPath("chrome", dirs...); ok {
			return p, nil
		}
	case "linux":
		if p, ok := findBinaryPreferPath("google-chrome", ""); ok {
			return p, nil
		}
		if p, ok := findBinaryPreferPath("chrome", ""); ok {
			return p, nil
		}
	case "darwin":
		// macOS packaged Chrome path
		candidate := "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
		if isRunnableFile(candidate) {
			return candidate, nil
		}
		if p, ok := findBinaryPreferPath("google-chrome", ""); ok {
			return p, nil
		}
	}

	return "", errors.New("未找到 Chrome。可通过 MINGEST_CHROME_PATH 指定 chrome 可执行文件路径")
}

type chromeCookie struct {
	Name    string  `json:"name"`
	Value   string  `json:"value"`
	Domain  string  `json:"domain"`
	Path    string  `json:"path"`
	Expires float64 `json:"expires"`
	Secure  bool    `json:"secure"`
}

func exportCookiesFromChromeCDP(chromePath, profileDir string, platform videoPlatform, headless bool) (string, func(), []chromeCookie, error) {
	// Start Chrome with our managed profile and export cookies from inside Chrome (no SQLite access).
	// Opening the target site helps ensure the profile cookie store is initialized before we read it.
	openURL := strings.TrimSpace(platform.LoginURL)
	if openURL == "" {
		openURL = "about:blank"
	}
	proc, port, stop, err := startChrome(chromePath, profileDir, headless, openURL)
	if err != nil {
		return "", nil, nil, err
	}
	defer stop()

	wsURL, err := waitForFirstPageWSURL(port, 15*time.Second)
	if err != nil {
		return "", nil, nil, err
	}

	// Give Chrome a moment to finish initializing the cookie store for the profile.
	time.Sleep(500 * time.Millisecond)

	cookies, err := cdpGetAllCookies(wsURL)
	if err != nil {
		return "", nil, nil, err
	}

	_ = proc // proc is kept to ensure Chrome stays alive until cookies are fetched.

	f, err := os.CreateTemp("", "mingest-cookies-*.txt")
	if err != nil {
		return "", nil, nil, err
	}
	path := f.Name()
	_ = f.Close()

	if err := writeNetscapeCookieFile(path, cookies, platform.AllowsCookieDomain); err != nil {
		_ = os.Remove(path)
		return "", nil, nil, err
	}

	cleanup := func() { _ = os.Remove(path) }
	return path, cleanup, cookies, nil
}

func chromeAuthViaCDP(chromePath, profileDir string, platform videoPlatform) ([]chromeCookie, error) {
	openURL := strings.TrimSpace(platform.LoginURL)
	if openURL == "" {
		openURL = "about:blank"
	}
	proc, port, stop, err := startChrome(chromePath, profileDir, false, openURL)
	if err != nil {
		return nil, err
	}
	defer stop()
	_ = proc

	if err := waitForDevTools(port, 15*time.Second); err != nil {
		return nil, err
	}

	logInfo("auth.cdp_manual_verification_prompt")

	reader := bufio.NewReader(os.Stdin)
	_, _ = reader.ReadString('\n')

	wsURL, err := waitForFirstPageWSURL(port, 5*time.Second)
	if err != nil {
		return nil, err
	}
	cookies, err := cdpGetAllCookies(wsURL)
	if err != nil {
		return nil, err
	}
	if !looksLikeLoggedIn(cookies, platform) {
		return nil, errors.New("未检测到有效登录 cookies（可能未登录或未完成验证）")
	}
	return cookies, nil
}

func looksLikeLoggedIn(cookies []chromeCookie, platform videoPlatform) bool {
	if len(platform.AuthCookieNames) == 0 {
		return false
	}
	for _, c := range cookies {
		if !platform.AllowsCookieDomain(c.Domain) {
			continue
		}
		for _, want := range platform.AuthCookieNames {
			if c.Name == want && c.Value != "" {
				return true
			}
		}
	}
	return false
}

func writeNetscapeCookieFile(path string, cookies []chromeCookie, allowDomain func(string) bool) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	_, _ = fmt.Fprintln(f, "# Netscape HTTP Cookie File")
	_, _ = fmt.Fprintln(f, "# This file was generated by mingest. DO NOT EDIT.")

	for _, c := range cookies {
		if allowDomain != nil && !allowDomain(c.Domain) {
			continue
		}
		domain := c.Domain
		if strings.TrimSpace(domain) == "" {
			continue
		}
		includeSubdomains := "FALSE"
		if strings.HasPrefix(domain, ".") {
			includeSubdomains = "TRUE"
		}
		secure := "FALSE"
		if c.Secure {
			secure = "TRUE"
		}

		// IMPORTANT:
		// For session cookies Chrome reports expires=-1. Netscape format requires an expires column,
		// but writing "0" makes Python treat the cookie as already expired. An empty expires field
		// is the correct representation for a session cookie.
		expires := ""
		if c.Expires > 0 {
			expires = strconv.FormatInt(int64(c.Expires), 10)
		}

		// domain	flag	path	secure	expiration	name	value
		_, _ = fmt.Fprintf(f, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			domain, includeSubdomains, c.Path, secure, expires, c.Name, c.Value)
	}

	return nil
}

func startChrome(chromePath, profileDir string, headless bool, openURL string) (*os.Process, int, func(), error) {
	port, err := pickFreePort()
	if err != nil {
		return nil, 0, nil, err
	}

	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		return nil, 0, nil, err
	}

	args := []string{
		chromePath,
		"--remote-debugging-address=127.0.0.1",
		fmt.Sprintf("--remote-debugging-port=%d", port),
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-background-networking",
		"--disable-sync",
		"--disable-default-apps",
		"--disable-extensions",
		"--user-data-dir=" + profileDir,
		"--profile-directory=Default",
	}
	if headless {
		args = append(args, "--headless=new", "--disable-gpu")
	}
	if strings.TrimSpace(openURL) != "" {
		args = append(args, openURL)
	}

	proc, err := os.StartProcess(chromePath, args, &os.ProcAttr{
		Env: os.Environ(),
		Dir: ".",
		Files: []*os.File{
			os.Stdin,
			os.Stdout,
			os.Stderr,
		},
	})
	if err != nil {
		return nil, 0, nil, err
	}

	stop := func() {
		_ = proc.Kill()
		_, _ = proc.Wait()
	}

	if err := waitForDevTools(port, 15*time.Second); err != nil {
		stop()
		return nil, 0, nil, err
	}

	return proc, port, stop, nil
}

func pickFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		return 0, errors.New("无法分配端口")
	}
	return addr.Port, nil
}

func waitForDevTools(port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 1 * time.Second}
	u := fmt.Sprintf("http://127.0.0.1:%d/json/version", port)

	for time.Now().Before(deadline) {
		req, _ := http.NewRequest(http.MethodGet, u, nil)
		resp, err := client.Do(req)
		if err == nil && resp != nil && resp.StatusCode == 200 {
			_ = resp.Body.Close()
			return nil
		}
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		time.Sleep(200 * time.Millisecond)
	}
	return errors.New("Chrome DevTools 未就绪（超时）")
}

type devToolsTarget struct {
	Type                 string `json:"type"`
	URL                  string `json:"url"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

func waitForFirstPageWSURL(port int, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 1 * time.Second}
	u := fmt.Sprintf("http://127.0.0.1:%d/json/list", port)

	for time.Now().Before(deadline) {
		req, _ := http.NewRequest(http.MethodGet, u, nil)
		resp, err := client.Do(req)
		if err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		if resp.StatusCode != 200 {
			time.Sleep(200 * time.Millisecond)
			continue
		}

		var targets []devToolsTarget
		if err := json.Unmarshal(body, &targets); err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		for _, t := range targets {
			if t.Type == "page" && strings.TrimSpace(t.WebSocketDebuggerURL) != "" {
				return t.WebSocketDebuggerURL, nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return "", errors.New("未找到可用的 DevTools page target")
}

func cdpGetAllCookies(wsURL string) ([]chromeCookie, error) {
	ws, err := wsDial(wsURL, 5*time.Second)
	if err != nil {
		return nil, err
	}
	defer ws.Close()

	cdp := &cdpClient{ws: ws, nextID: 1}
	if err := cdp.Call("Network.enable", nil, nil); err != nil {
		return nil, err
	}

	var res struct {
		Cookies []chromeCookie `json:"cookies"`
	}
	if err := cdp.Call("Network.getAllCookies", nil, &res); err != nil {
		return nil, err
	}
	return res.Cookies, nil
}

type cdpClient struct {
	ws     *wsConn
	nextID int
}

func (c *cdpClient) Call(method string, params any, out any) error {
	id := c.nextID
	c.nextID++

	req := map[string]any{
		"id":     id,
		"method": method,
	}
	if params != nil {
		req["params"] = params
	}
	if err := c.ws.WriteJSON(req); err != nil {
		return err
	}

	for {
		msg, err := c.ws.ReadJSONRaw()
		if err != nil {
			return err
		}
		var envelope struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(msg, &envelope); err != nil {
			continue
		}
		if envelope.ID != id {
			continue
		}
		if envelope.Error != nil {
			return fmt.Errorf("%s: %s", method, envelope.Error.Message)
		}
		if out != nil && len(envelope.Result) > 0 {
			if err := json.Unmarshal(envelope.Result, out); err != nil {
				return err
			}
		}
		return nil
	}
}

type wsConn struct {
	c  net.Conn
	br *bufio.Reader
}

func wsDial(rawURL string, timeout time.Duration) (*wsConn, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "ws" {
		return nil, fmt.Errorf("不支持的 WebSocket 协议: %s", u.Scheme)
	}

	host := u.Host
	if !strings.Contains(host, ":") {
		host += ":80"
	}

	d := net.Dialer{Timeout: timeout}
	conn, err := d.Dial("tcp", host)
	if err != nil {
		return nil, err
	}

	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		_ = conn.Close()
		return nil, err
	}
	secKey := base64.StdEncoding.EncodeToString(keyBytes)

	path := u.RequestURI()
	req := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n",
		path, u.Host, secKey)
	if _, err := io.WriteString(conn, req); err != nil {
		_ = conn.Close()
		return nil, err
	}

	br := bufio.NewReader(conn)
	statusLine, err := br.ReadString('\n')
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if !strings.Contains(statusLine, " 101 ") {
		_ = conn.Close()
		return nil, fmt.Errorf("WebSocket 握手失败: %s", strings.TrimSpace(statusLine))
	}
	// Read headers until blank line.
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		if line == "\r\n" {
			break
		}
	}

	return &wsConn{c: conn, br: br}, nil
}

func (w *wsConn) Close() error {
	return w.c.Close()
}

func (w *wsConn) WriteJSON(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return w.writeFrame(0x1, b)
}

func (w *wsConn) ReadJSONRaw() ([]byte, error) {
	for {
		op, payload, err := w.readFrame()
		if err != nil {
			return nil, err
		}
		switch op {
		case 0x1: // text
			return payload, nil
		case 0x9: // ping
			_ = w.writeFrame(0xA, payload)
			continue
		case 0xA: // pong
			continue
		case 0x8: // close
			return nil, io.EOF
		default:
			// ignore other frames
			continue
		}
	}
}

func (w *wsConn) writeFrame(opcode byte, payload []byte) error {
	// Client-to-server frames must be masked.
	const fin = 0x80
	header := []byte{fin | opcode, 0x80}

	n := len(payload)
	switch {
	case n <= 125:
		header[1] |= byte(n)
	case n <= 65535:
		header[1] |= 126
		ext := make([]byte, 2)
		binary.BigEndian.PutUint16(ext, uint16(n))
		header = append(header, ext...)
	default:
		header[1] |= 127
		ext := make([]byte, 8)
		binary.BigEndian.PutUint64(ext, uint64(n))
		header = append(header, ext...)
	}

	maskKey := make([]byte, 4)
	if _, err := rand.Read(maskKey); err != nil {
		return err
	}
	header = append(header, maskKey...)

	masked := make([]byte, n)
	for i := 0; i < n; i++ {
		masked[i] = payload[i] ^ maskKey[i%4]
	}

	if _, err := w.c.Write(header); err != nil {
		return err
	}
	_, err := w.c.Write(masked)
	return err
}

func (w *wsConn) readFrame() (byte, []byte, error) {
	b0, err := w.br.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	b1, err := w.br.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	opcode := b0 & 0x0f
	mask := (b1 & 0x80) != 0
	payloadLen := int(b1 & 0x7f)

	switch payloadLen {
	case 126:
		ext := make([]byte, 2)
		if _, err := io.ReadFull(w.br, ext); err != nil {
			return 0, nil, err
		}
		payloadLen = int(binary.BigEndian.Uint16(ext))
	case 127:
		ext := make([]byte, 8)
		if _, err := io.ReadFull(w.br, ext); err != nil {
			return 0, nil, err
		}
		n := binary.BigEndian.Uint64(ext)
		if n > 10*1024*1024 {
			return 0, nil, errors.New("WebSocket payload 过大")
		}
		payloadLen = int(n)
	}

	var maskKey []byte
	if mask {
		maskKey = make([]byte, 4)
		if _, err := io.ReadFull(w.br, maskKey); err != nil {
			return 0, nil, err
		}
	}

	payload := make([]byte, payloadLen)
	if _, err := io.ReadFull(w.br, payload); err != nil {
		return 0, nil, err
	}
	if mask {
		for i := 0; i < payloadLen; i++ {
			payload[i] ^= maskKey[i%4]
		}
	}

	return opcode, payload, nil
}
