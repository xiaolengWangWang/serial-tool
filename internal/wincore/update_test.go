package wincore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	asset := "CommBox-0.8.5-Windows-x64.zip"
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
