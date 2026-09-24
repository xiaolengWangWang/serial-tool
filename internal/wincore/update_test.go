package wincore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.8.5", "0.8.4", 1},
		{"0.8.4", "0.8.5", -1},
		{"0.8.4", "0.8.4", 0},
		{"v0.8.4", "0.8.4", 0},
		{"0.9.0", "0.10.0", -1},
		{"1.0.0", "0.9.9", 1},
		{"0.8", "0.8.0", 0},
		{"0.9.0-rc1", "0.9.0", -1},
		{"0.9.0", "0.9.0-rc1", 1},
		{"0.9.0-rc2", "0.9.0-rc1", 1},
	}
	for _, c := range cases {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("CompareVersions(%q,%q)=%d,期望 %d", c.a, c.b, got, c.want)
		}
	}
}

// releaseJSON 复刻 GitHub releases/latest 的关键字段。
func releaseJSON(tag, asset string, size int, notes string) string {
	return fmt.Sprintf(`{"tag_name":%q,"name":"CommBox %s","body":%q,
		"html_url":"https://github.com/xiaolengWangWang/serial-tool/releases/tag/%s",
		"assets":[{"name":"CommBox.exe","browser_download_url":"https://github.com/x/y/releases/download/%s/CommBox.exe","size":16000000},
		{"name":%q,"browser_download_url":"https://github.com/x/y/releases/download/%s/%s","size":%d}]}`,
		tag, tag, notes, tag, tag, asset, tag, asset, size)
}

func TestCheckUpdateNewer(t *testing.T) {
	// 用当前平台的后缀拼出附件名,验证在这个 OS 上挑的是本平台的包而非裸 exe。
	asset := "CommBox-0.8.5" + updateAssetSuffix()
	notes := "# 更新\n\n## SHA256\n\n```\n" + strings.Repeat("ab", 32) + "  " + asset + "\n```\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Accept"); got != "application/vnd.github+json" {
			t.Errorf("Accept 头=%q", got)
		}
		if !strings.HasPrefix(r.Header.Get("User-Agent"), "CommBox/") {
			t.Errorf("User-Agent=%q", r.Header.Get("User-Agent"))
		}
		fmt.Fprint(w, releaseJSON("v0.8.5", asset, 6369803, notes))
	}))
	defer srv.Close()

	info, err := checkUpdateFrom(context.Background(), srv.URL, "0.8.4")
	if err != nil {
		t.Fatal(err)
	}
	if info.Version != "0.8.5" || !info.Newer {
		t.Fatalf("版本解析错误: %+v", info)
	}
	// 必须挑 Windows zip,而不是排在前面的裸 exe。
	if info.AssetName != asset || info.AssetSize != 6369803 {
		t.Fatalf("安装包选择错误: %q %d", info.AssetName, info.AssetSize)
	}
	if info.SHA256 != strings.Repeat("ab", 32) {
		t.Fatalf("SHA256 解析错误: %q", info.SHA256)
	}
	if !strings.Contains(info.PageURL, "releases/tag/v0.8.5") {
		t.Fatalf("发布页地址错误: %q", info.PageURL)
	}
}

func TestCheckUpdateSameVersionNotNewer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, releaseJSON("v0.8.4", "CommBox-0.8.4-Windows-x64.zip", 1, "无 SHA256"))
	}))
	defer srv.Close()
	info, err := checkUpdateFrom(context.Background(), srv.URL, "0.8.4")
	if err != nil {
		t.Fatal(err)
	}
	if info.Newer {
		t.Fatal("同版本不应判为有新版本")
	}
	if info.SHA256 != "" {
		t.Fatalf("发布说明没有校验和时应为空,得到 %q", info.SHA256)
	}
}

func TestCheckUpdateServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	if _, err := checkUpdateFrom(context.Background(), srv.URL, "0.8.4"); err == nil {
		t.Fatal("非 200 响应应返回错误")
	}
}

// 连接在响应前被掐断(慢网络下常见)时重试一次,第二次成功就算检查成功。
func TestCheckUpdateRetriesDroppedConnection(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				conn.Close()
			}
			return
		}
		fmt.Fprint(w, releaseJSON("v0.9.2", "CommBox-0.9.2-Windows-x64.zip", 1, ""))
	}))
	defer srv.Close()
	info, err := checkUpdateFrom(context.Background(), srv.URL, "0.9.1")
	if err != nil {
		t.Fatalf("第一次连接被掐断后应重试成功: %v", err)
	}
	if !info.Newer || atomic.LoadInt32(&hits) != 2 {
		t.Fatalf("请求次数=%d info=%+v", hits, info)
	}
}

// 服务器已回了状态码(如限流 403)说明网络是通的,重试没有意义。
func TestCheckUpdateDoesNotRetryHTTPError(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	if _, err := checkUpdateFrom(context.Background(), srv.URL, "0.9.1"); err == nil {
		t.Fatal("403 应返回错误")
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("HTTP 错误不应重试,实际请求 %d 次", got)
	}
}

// 实测国内连 api.github.com 握手 12~21 秒、整次 15~25 秒。超时不能退回
// 标准库默认的 10 秒握手,调用方的总时长也得容得下全部重试。
func TestUpdateTimeoutsFitSlowGitHub(t *testing.T) {
	const worstHandshake, worstRequest = 25 * time.Second, 30 * time.Second
	if got := updateHTTPTransport().TLSHandshakeTimeout; got < worstHandshake {
		t.Errorf("TLS 握手超时 %v 小于实测最慢 %v", got, worstHandshake)
	}
	if updateClient().Timeout < worstRequest {
		t.Errorf("单次检查超时 %v 小于实测最慢 %v", updateClient().Timeout, worstRequest)
	}
	if UpdateCheckBudget < updateCheckAttempts*updateClient().Timeout {
		t.Errorf("总时长 %v 容不下 %d 次请求", UpdateCheckBudget, updateCheckAttempts)
	}
}

// 检查和下载都必须走放宽了超时的 updateTransport,漏掉任何一个都会在慢网络下失败。
func TestCheckAndDownloadUseUpdateTransport(t *testing.T) {
	var dials int32
	saved := updateHTTPTransport()
	defer func() { updateTransport = saved }()
	updateTransport = saved.Clone()
	updateTransport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		atomic.AddInt32(&dials, 1)
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}

	payload := []byte("zip")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".zip") {
			w.Write(payload)
			return
		}
		fmt.Fprint(w, releaseJSON("v0.9.2", "CommBox-0.9.2-Windows-x64.zip", len(payload), ""))
	}))
	defer srv.Close()

	if _, err := checkUpdateFrom(context.Background(), srv.URL, "0.9.1"); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&dials) == 0 {
		t.Fatal("检查请求没有走 updateTransport")
	}
	updateTransport.CloseIdleConnections() // 让下载重新拨号,才能证明它也走这个 Transport
	before := atomic.LoadInt32(&dials)
	info := UpdateInfo{AssetURL: srv.URL + "/CommBox-0.9.2-Windows-x64.zip", AssetName: "CommBox-0.9.2-Windows-x64.zip"}
	if _, err := downloadAsset(context.Background(), info, t.TempDir(), nil); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&dials) == before {
		t.Fatal("下载请求没有走 updateTransport")
	}
}

// 下载地址来自响应正文,非 GitHub 域名一律不认,避免被异常响应引到别处。
func TestAllowedDownloadURL(t *testing.T) {
	ok := []string{
		"https://github.com/x/y/releases/download/v1/a.zip",
		"https://objects.githubusercontent.com/a.zip",
		"https://release-assets.githubusercontent.com/a.zip",
	}
	bad := []string{
		"http://github.com/x/y/a.zip",
		"https://github.com.evil.example/a.zip",
		"https://evil.example/a.zip",
		"",
	}
	for _, u := range ok {
		if !allowedDownloadURL(u) {
			t.Errorf("应允许 %q", u)
		}
	}
	for _, u := range bad {
		if allowedDownloadURL(u) {
			t.Errorf("应拒绝 %q", u)
		}
	}
}

func TestCheckUpdateRejectsForeignAssetHost(t *testing.T) {
	body := `{"tag_name":"v0.9.0","name":"n","body":"","html_url":"https://github.com/a/b",
		"assets":[{"name":"CommBox-0.9.0-Windows-x64.zip","browser_download_url":"https://evil.example/a.zip","size":1}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, body)
	}))
	defer srv.Close()
	info, err := checkUpdateFrom(context.Background(), srv.URL, "0.8.4")
	if err != nil {
		t.Fatal(err)
	}
	if info.AssetURL != "" {
		t.Fatalf("非官方下载域名不应被采纳: %q", info.AssetURL)
	}
}

func TestDownloadUpdateVerifiesChecksum(t *testing.T) {
	payload := []byte("commbox-zip-payload")
	sum := sha256.Sum256(payload)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(payload)
	}))
	defer srv.Close()
	dir := t.TempDir()
	info := UpdateInfo{AssetURL: srv.URL + "/a.zip", AssetName: "CommBox-0.8.5-Windows-x64.zip", AssetSize: int64(len(payload)), SHA256: hex.EncodeToString(sum[:])}

	// httptest 是 http://127.0.0.1,域名白名单必须先拦下来。
	if _, err := DownloadUpdate(context.Background(), info, dir, nil); err == nil {
		t.Fatal("非 GitHub 域名应被拒绝")
	}

	var last int64
	path, err := downloadAsset(context.Background(), info, dir, func(done, total int64) { last = done })
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != info.AssetName {
		t.Fatalf("落地文件名错误: %q", path)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(payload) {
		t.Fatalf("文件内容错误: %v", err)
	}
	if last != int64(len(payload)) {
		t.Fatalf("进度回调最后一次应为 %d,得到 %d", len(payload), last)
	}
	// 临时文件必须清干净,只留下最终文件。
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("目录里应只剩安装包,实际 %d 个文件", len(entries))
	}
}

func TestDownloadUpdateChecksumMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("tampered"))
	}))
	defer srv.Close()
	dir := t.TempDir()
	info := UpdateInfo{AssetURL: srv.URL + "/a.zip", AssetName: "CommBox-0.8.5-Windows-x64.zip", SHA256: strings.Repeat("00", 32)}
	if _, err := downloadAsset(context.Background(), info, dir, nil); err == nil {
		t.Fatal("校验和不一致时应报错")
	}
	// 校验失败后不能留下文件,否则用户会去装一个被改过的包。
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("校验失败后目录应为空,实际 %d 个文件", len(entries))
	}
}

// TestLiveUpdateDownload 打通真实链路:查最新发布 → 下载安装包 → 校验 SHA256。
// 需要联网,默认跳过,用 COMMBOX_LIVE_UPDATE=1 go test -run TestLiveUpdateDownload 手动跑。
func TestLiveUpdateDownload(t *testing.T) {
	if os.Getenv("COMMBOX_LIVE_UPDATE") != "1" {
		t.Skip("联网用例,默认跳过")
	}
	info, err := CheckUpdate(context.Background(), "0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if !info.Newer || info.AssetURL == "" {
		t.Fatalf("线上发布信息不完整: %+v", info)
	}
	t.Logf("最新版本 v%s 安装包 %s(%d 字节) SHA256=%s", info.Version, info.AssetName, info.AssetSize, info.SHA256)
	if info.SHA256 == "" {
		t.Fatal("发布说明里没解析到 SHA256,下载将无法校验")
	}
	path, err := DownloadUpdate(context.Background(), info, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.AssetSize > 0 && st.Size() != info.AssetSize {
		t.Fatalf("下载大小 %d 与发布信息 %d 不符", st.Size(), info.AssetSize)
	}
}

// assetServer 模拟慢网络下的 GitHub 资源地址:前 failures 次请求只发一半,
// 然后断开(stall 为真时改为不再发数据、也不断开);之后正常响应。
// honorRange 为假时无视 Range,总是发整个文件。
func assetServer(t *testing.T, payload []byte, failures int32, stall, honorRange bool) (*httptest.Server, *int32, func() []string) {
	var hits int32
	var mu sync.Mutex
	var ranges []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&hits, 1)
		mu.Lock()
		ranges = append(ranges, r.Header.Get("Range"))
		mu.Unlock()
		if n <= failures {
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			w.Write(payload[:len(payload)/2])
			w.(http.Flusher).Flush()
			if stall {
				<-r.Context().Done()
				return
			}
			panic(http.ErrAbortHandler) // 声明了完整长度却只发一半就断开,客户端读到 unexpected EOF
		}
		if honorRange {
			http.ServeContent(w, r, "a.zip", time.Time{}, bytes.NewReader(payload))
			return
		}
		w.Write(payload)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), ranges...) }
}

func testPayload() ([]byte, UpdateInfo) {
	payload := make([]byte, 256<<10)
	for i := range payload {
		payload[i] = byte(i * 7)
	}
	sum := sha256.Sum256(payload)
	return payload, UpdateInfo{AssetName: "CommBox-9.9.9-Windows-x64.zip", AssetSize: int64(len(payload)), SHA256: hex.EncodeToString(sum[:])}
}

func assertDownloaded(t *testing.T, path string, payload []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("下载内容不对: %d 字节", len(got))
	}
}

// 连接中途断开后带 Range 从断点续传,不从头再下。
func TestDownloadResumesAfterDrop(t *testing.T) {
	payload, info := testPayload()
	srv, hits, ranges := assetServer(t, payload, 1, false, true)
	info.AssetURL = srv.URL + "/a.zip"
	path, err := downloadAsset(context.Background(), info, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	assertDownloaded(t, path, payload)
	r := ranges()
	if atomic.LoadInt32(hits) != 2 || r[0] != "" || !strings.HasPrefix(r[1], "bytes=") || r[1] == "bytes=0-" {
		t.Fatalf("应断点续传一次,实际请求 %d 次,Range=%q", *hits, r)
	}
}

// 服务器不认 Range、重发整个文件时,要丢掉已写的部分从头写,不能拼出一个错包。
func TestDownloadRestartsWhenRangeIgnored(t *testing.T) {
	payload, info := testPayload()
	srv, hits, _ := assetServer(t, payload, 1, false, false)
	info.AssetURL = srv.URL + "/a.zip"
	path, err := downloadAsset(context.Background(), info, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	assertDownloaded(t, path, payload)
	if atomic.LoadInt32(hits) != 2 {
		t.Fatalf("请求次数=%d", *hits)
	}
}

// 连接没断但不再来数据(慢网络常见的假死),停滞超时后断开并续传。
func TestDownloadResumesAfterStall(t *testing.T) {
	saved := downloadStallTimeout
	defer func() { downloadStallTimeout = saved }()
	downloadStallTimeout = 300 * time.Millisecond

	payload, info := testPayload()
	srv, hits, _ := assetServer(t, payload, 1, true, true)
	info.AssetURL = srv.URL + "/a.zip"
	start := time.Now()
	path, err := downloadAsset(context.Background(), info, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	assertDownloaded(t, path, payload)
	if atomic.LoadInt32(hits) != 2 || time.Since(start) > 10*time.Second {
		t.Fatalf("请求次数=%d 用时=%v", *hits, time.Since(start))
	}
}

// 次次都断就按次数上限放弃,并且不留半截文件。
func TestDownloadGivesUpAfterAttempts(t *testing.T) {
	saved := downloadAttempts
	defer func() { downloadAttempts = saved }()
	downloadAttempts = 3

	payload, info := testPayload()
	srv, hits, _ := assetServer(t, payload, 100, false, true)
	info.AssetURL = srv.URL + "/a.zip"
	dir := t.TempDir()
	_, err := downloadAsset(context.Background(), info, dir, nil)
	if err == nil || !strings.Contains(err.Error(), "已尝试 3 次") {
		t.Fatalf("应在 3 次后放弃,得到 %v", err)
	}
	if atomic.LoadInt32(hits) != 3 {
		t.Fatalf("请求次数=%d", *hits)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("放弃后目录应为空,实际 %d 个文件", len(entries))
	}
}

// 服务器明确拒绝(如 404)时不重试。
func TestDownloadDoesNotRetryHTTPError(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		http.NotFound(w, r)
	}))
	defer srv.Close()
	_, info := testPayload()
	info.AssetURL = srv.URL + "/a.zip"
	if _, err := downloadAsset(context.Background(), info, t.TempDir(), nil); err == nil {
		t.Fatal("404 应返回错误")
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("HTTP 错误不应重试,实际请求 %d 次", got)
	}
}

// 慢但一直在收数据的下载不能被停滞超时掐断:总时长超过停滞超时也要一次下完。
func TestDownloadSlowButSteadyIsNotCut(t *testing.T) {
	saved := downloadStallTimeout
	defer func() { downloadStallTimeout = saved }()
	downloadStallTimeout = 400 * time.Millisecond

	payload, info := testPayload()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		const chunks = 16 // 每 50ms 一块,共约 800ms,是停滞超时的两倍;块间隔只有它的 1/8
		for i := 0; i < chunks; i++ {
			w.Write(payload[i*len(payload)/chunks : (i+1)*len(payload)/chunks])
			w.(http.Flusher).Flush()
			time.Sleep(50 * time.Millisecond)
		}
	}))
	defer srv.Close()
	info.AssetURL = srv.URL + "/a.zip"
	path, err := downloadAsset(context.Background(), info, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	assertDownloaded(t, path, payload)
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("持续收数据时不应断开重连,实际请求 %d 次", got)
	}
}

// 建连和等响应头慢(两次慢握手)不算停滞:那段由 Transport 的各项超时管,
// 算进停滞超时的话每次重试都会在同一处被掐断。
func TestDownloadSlowConnectionSetupIsNotCut(t *testing.T) {
	saved := downloadStallTimeout
	defer func() { downloadStallTimeout = saved }()
	downloadStallTimeout = 300 * time.Millisecond

	payload, info := testPayload()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		time.Sleep(900 * time.Millisecond) // 响应头晚于停滞超时的三倍才到
		w.Write(payload)
	}))
	defer srv.Close()
	info.AssetURL = srv.URL + "/a.zip"
	path, err := downloadAsset(context.Background(), info, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	assertDownloaded(t, path, payload)
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("响应头慢不应触发重试,实际请求 %d 次", got)
	}
}
