package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 发布安装包的仓库:Gitee 上是 GitHub 仓库的镜像,两边发同样的安装包和说明。
const (
	updateGitHubRepo = "xiaolengWangWang/serial-tool"
	updateGiteeRepo  = "sun_xuanqi/serial-tool"
)

// UpdateReleasesPage 是检查失败时给用户手动下载的发布页。
const UpdateReleasesPage = "https://gitee.com/" + updateGiteeRepo + "/releases"

// updateSource 是一个发布安装包的地方。检查只发 GET,不带任何用户数据,
// 返回的也只是公开的发布信息;GitHub 和 Gitee 的 releases/latest 字段相同。
type updateSource struct {
	name string // 给用户看的名字
	api  string // 最新正式版的接口地址
	page string // 接口没给发布页地址时(Gitee 不给)用的下载页
}

// updateSources 是检查更新时同时查询的下载源,一样新时取排在前面的。国内连 GitHub
// 握手要十几秒、下载常断,Gitee 排在前面。测试时换成本地服务器。
var updateSources = []updateSource{
	{"Gitee", "https://gitee.com/api/v5/repos/" + updateGiteeRepo + "/releases/latest", UpdateReleasesPage},
	{"GitHub", "https://api.github.com/repos/" + updateGitHubRepo + "/releases/latest", "https://github.com/" + updateGitHubRepo + "/releases"},
}

// updateAssetSuffix 返回当前平台对应的安装包文件名后缀,用来从发布附件里挑出
// 该平台的包(发布页同时还挂着裸 exe、其他平台的包)。实现按平台拆到
// update_windows.go / update_darwin.go / update_linux.go,macOS 还要按芯片区分。

// UpdateInfo 描述一次检查结果。没有可用安装包时 AssetURL 为空,
// 此时界面只提供"打开发布页",不提供下载。
type UpdateInfo struct {
	Version   string   // 去掉 v 前缀的版本号
	Name      string   // 发布标题
	Notes     string   // 发布说明正文
	PageURL   string   // 发布页地址
	AssetURL  string   // 安装包下载地址
	Mirrors   []string // 别的下载源上同一个安装包的地址,下载出错时换着用
	AssetName string
	AssetSize int64  // 0 表示未知(Gitee 不给大小)
	SHA256    string // 从发布说明里解析出的安装包校验和,可能为空
	Newer     bool   // 是否比传入的当前版本新
	Source    string // 从哪个下载源查到的
}

// ShouldAutoPrompt 判断启动时的自动检查是否值得弹窗:有新版本,且这次发布
// 带了本平台的安装包。各平台共用同一个"最新发布",只发 macOS 的版本不该
// 在 Windows 上每次启动都弹一个没法下载的提示(反之亦然);手动检查照常告知。
func (i UpdateInfo) ShouldAutoPrompt() bool { return i.Newer && i.AssetURL != "" }

// CheckUpdate 同时查询全部下载源的最新发布并与 current 比较,取版本最新的那个;
// 一样新时优先带本平台安装包的,再按 updateSources 的顺序。别的下载源上同版本的
// 同名安装包记进 Mirrors。全部下载源都查不到才报错。
func CheckUpdate(ctx context.Context, current string) (UpdateInfo, error) {
	return checkUpdateSources(ctx, updateSources, current)
}

func checkUpdateSources(ctx context.Context, sources []updateSource, current string) (UpdateInfo, error) {
	infos := make([]UpdateInfo, len(sources))
	errs := make([]error, len(sources))
	var wg sync.WaitGroup
	for i, src := range sources {
		wg.Add(1)
		go func() {
			defer wg.Done()
			infos[i], errs[i] = checkUpdateFrom(ctx, src.api, current)
			infos[i].Source = src.name
			if infos[i].PageURL == "" {
				infos[i].PageURL = src.page
			}
		}()
	}
	wg.Wait()
	best := -1
	var failed []string
	for i := range sources {
		if errs[i] != nil {
			failed = append(failed, sources[i].name+": "+errs[i].Error())
			continue
		}
		if best < 0 {
			best = i
			continue
		}
		c := CompareVersions(infos[i].Version, infos[best].Version)
		if c > 0 || c == 0 && infos[best].AssetURL == "" && infos[i].AssetURL != "" {
			best = i
		}
	}
	if best < 0 {
		return UpdateInfo{}, errors.New(strings.Join(failed, ";"))
	}
	info := infos[best]
	for i, other := range infos {
		if i == best || errs[i] != nil || other.AssetURL == "" || other.Version != info.Version || other.AssetName != info.AssetName {
			continue
		}
		info.Mirrors = append(info.Mirrors, other.AssetURL)
		if info.AssetSize <= 0 {
			info.AssetSize = other.AssetSize
		}
		if info.SHA256 == "" {
			info.SHA256 = other.SHA256
		}
	}
	return info, nil
}

func checkUpdateFrom(ctx context.Context, api, current string) (UpdateInfo, error) {
	resp, err := getRelease(ctx, api)
	if err != nil {
		return UpdateInfo{}, fmt.Errorf("连接发布服务器失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return UpdateInfo{}, fmt.Errorf("发布服务器返回 %s", resp.Status)
	}
	var payload struct {
		TagName string `json:"tag_name"`
		Name    string `json:"name"`
		Body    string `json:"body"`
		HTMLURL string `json:"html_url"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
			Size int64  `json:"size"`
		} `json:"assets"`
	}
	// 发布说明可能很长,但上限仍要有,避免异常响应把内存吃光。
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&payload); err != nil {
		return UpdateInfo{}, fmt.Errorf("解析发布信息失败: %w", err)
	}
	version := strings.TrimPrefix(strings.TrimSpace(payload.TagName), "v")
	if version == "" {
		return UpdateInfo{}, fmt.Errorf("发布信息缺少版本号")
	}
	info := UpdateInfo{
		Version: version,
		Name:    strings.TrimSpace(payload.Name),
		Notes:   strings.TrimSpace(payload.Body),
		PageURL: payload.HTMLURL,
		Newer:   CompareVersions(version, current) > 0,
	}
	for _, a := range payload.Assets {
		if strings.HasSuffix(a.Name, updateAssetSuffix()) && allowedDownloadURL(a.URL) {
			info.AssetURL, info.AssetName, info.AssetSize = a.URL, a.Name, a.Size
			break
		}
	}
	info.SHA256 = sha256FromNotes(info.Notes, info.AssetName)
	return info, nil
}

// getRelease 发出检查请求。连接层失败(握手超时、连接被重置)重试一次,
// 慢网络下偶发失败后第二次往往能通;服务器已回了状态码的不重试。
func getRelease(ctx context.Context, api string) (*http.Response, error) {
	var err error
	for attempt := 0; attempt < updateCheckAttempts; attempt++ {
		var req *http.Request
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, api, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("User-Agent", "CommBox/"+Version)
		var resp *http.Response
		resp, err = updateClient().Do(req)
		if err == nil {
			return resp, nil
		}
		if ctx.Err() != nil {
			break
		}
	}
	return nil, err
}

// 国内连 GitHub 时 TLS 握手常要十几到二十几秒(2026-09 实测 api.github.com
// 握手 12~21 秒、整次请求 15~25 秒),标准库默认的 10 秒握手超时几乎必然失败。
// 检查和下载都走 updateHTTPTransport,下载要连 github.com 和资源 CDN,同样慢。
var (
	updateTransportOnce sync.Once
	updateTransport     *http.Transport // 测试在创建后可替换
)

// updateHTTPTransport 在第一次检查或下载时才创建 Transport。不能写成包级变量的
// 初始化:那会在包初始化时执行,不用检查更新的 CommBox-CLI 也会链接整套 HTTPS
// 客户端(TLS、证书校验、HTTP/2),exe 平白大 1.7 MB。
func updateHTTPTransport() *http.Transport {
	updateTransportOnce.Do(func() {
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.TLSHandshakeTimeout = 45 * time.Second
		t.ResponseHeaderTimeout = 30 * time.Second
		updateTransport = t
	})
	return updateTransport
}

const (
	updateCheckTimeout  = 60 * time.Second // 单次检查请求:慢握手加几秒响应
	updateCheckAttempts = 2
)

// UpdateCheckBudget 是调用方给整次检查(含重试)留的总时间。
const UpdateCheckBudget = updateCheckAttempts*updateCheckTimeout + 5*time.Second

func updateClient() *http.Client {
	return &http.Client{Transport: updateHTTPTransport(), Timeout: updateCheckTimeout}
}

// allowedDownloadURL 只信任 GitHub 和 Gitee 自己的下载域名。检查接口是固定地址且走 TLS,
// 但下载地址来自响应正文,限制域名可避免被一个异常响应引到别处。Gitee 的附件会跳到
// foruda.gitee.com,同属 gitee.com。
func allowedDownloadURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, ok := range []string{"github.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com", "gitee.com"} {
		if host == ok || strings.HasSuffix(host, "."+ok) {
			return true
		}
	}
	return false
}

// sha256FromNotes 从发布说明里找出安装包的校验和。发布说明按
// "<64 位十六进制>  <文件名>" 的格式附带 SHA256,找不到就返回空串。
func sha256FromNotes(notes, asset string) string {
	if asset == "" {
		return ""
	}
	for _, line := range strings.Split(notes, "\n") {
		line = strings.TrimSpace(strings.Trim(line, "`"))
		if !strings.HasSuffix(line, asset) {
			continue
		}
		sum := strings.TrimSpace(strings.TrimSuffix(line, asset))
		sum = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(sum, "*")))
		if len(sum) == 64 {
			if _, err := hex.DecodeString(sum); err == nil {
				return sum
			}
		}
	}
	return ""
}

// CompareVersions 比较两个版本号,返回 -1 / 0 / 1。
// 逐段按数字比较;带后缀的段(如 0.9.0-rc1)数字部分相同时,有后缀的算旧版。
func CompareVersions(a, b string) int {
	as := strings.Split(strings.TrimPrefix(strings.TrimSpace(a), "v"), ".")
	bs := strings.Split(strings.TrimPrefix(strings.TrimSpace(b), "v"), ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		an, asuf := versionPart(as, i)
		bn, bsuf := versionPart(bs, i)
		if an != bn {
			if an > bn {
				return 1
			}
			return -1
		}
		if asuf != bsuf {
			// 预发布版排在正式版之前:0.9.0-rc1 < 0.9.0
			if asuf == "" {
				return 1
			}
			if bsuf == "" {
				return -1
			}
			if asuf > bsuf {
				return 1
			}
			return -1
		}
	}
	return 0
}

func versionPart(parts []string, i int) (int, string) {
	if i >= len(parts) {
		return 0, ""
	}
	p := strings.TrimSpace(parts[i])
	cut := len(p)
	for j, r := range p {
		if r < '0' || r > '9' {
			cut = j
			break
		}
	}
	n, _ := strconv.Atoi(p[:cut])
	return n, p[cut:]
}

// DownloadUpdate 把安装包下载到 dir,返回落地路径。progress 可为 nil。
// 发布说明里带了 SHA256 时会校验,不一致则删除文件并报错。
func DownloadUpdate(ctx context.Context, info UpdateInfo, dir string, progress func(done, total int64)) (string, error) {
	if info.AssetURL == "" || info.AssetName == "" {
		return "", fmt.Errorf("本次发布没有可下载的安装包")
	}
	if !allowedDownloadURL(info.AssetURL) {
		return "", fmt.Errorf("下载地址不是 Gitee 或 GitHub 官方域名,已拒绝")
	}
	var mirrors []string
	for _, m := range info.Mirrors {
		if allowedDownloadURL(m) {
			mirrors = append(mirrors, m)
		}
	}
	info.Mirrors = mirrors
	return downloadAsset(ctx, info, dir, progress)
}

// 慢网络下(实测 39 KB/s)13 MB 的安装包要下 6 分钟,中途断一次很常见。
// GitHub 的资源地址支持 Range,断了就从断点接着下,不从头再来;Gitee 不认 Range,
// 只能从头下。不设整体超时,只看是否停滞:慢但一直在收数据就让它下完,用户关窗可随时取消。
var (
	downloadAttempts     = 5
	downloadStallTimeout = 60 * time.Second
)

// downloadFatal 包装不值得重试的错误:本地磁盘写不进去。
type downloadFatal struct{ error }

// downloadRefused 是服务器明确拒绝(404、403 等):只有一个下载源时不值得重试,
// 有镜像时换下一个。
type downloadRefused struct{ error }

// downloadAsset 是不含域名白名单的下载实现,白名单在 DownloadUpdate 里把关。
func downloadAsset(ctx context.Context, info UpdateInfo, dir string, progress func(done, total int64)) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, filepath.Base(info.AssetName))
	// 先写临时文件再改名:中途失败或被取消时不会留下一个看着完整的半截包。
	tmp, err := os.CreateTemp(dir, ".commbox-update-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	fail := func(err error) (string, error) {
		tmp.Close()
		os.Remove(tmpName)
		return "", err
	}
	d := &resumableDownload{file: tmp, hash: sha256.New(), total: info.AssetSize, progress: progress}
	// 出错后换下一个下载源:同一个文件,认 Range 的接着下,不认的从头下,最后统一按大小和 SHA256 校验。
	urls := append([]string{info.AssetURL}, info.Mirrors...)
	for attempt := 1; ; attempt++ {
		err = d.fetch(ctx, urls[(attempt-1)%len(urls)])
		if err == nil {
			break
		}
		var fatal downloadFatal
		var refused downloadRefused
		if ctx.Err() != nil || errors.As(err, &fatal) || errors.As(err, &refused) && len(urls) == 1 {
			return fail(err)
		}
		if attempt == downloadAttempts {
			return fail(fmt.Errorf("%w(已尝试 %d 次)", err, attempt))
		}
	}
	if info.AssetSize > 0 && d.done != info.AssetSize {
		return fail(fmt.Errorf("下载不完整: 收到 %d 字节,应为 %d 字节", d.done, info.AssetSize))
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return "", err
	}
	if info.SHA256 != "" {
		if got := hex.EncodeToString(d.hash.Sum(nil)); got != info.SHA256 {
			os.Remove(tmpName)
			return "", fmt.Errorf("校验失败:安装包 SHA256 与发布说明不一致,已删除下载文件")
		}
	}
	if err := os.Rename(tmpName, dst); err != nil {
		os.Remove(tmpName)
		return "", err
	}
	return dst, nil
}

// resumableDownload 记录已写入临时文件的字节数和对应的 SHA256 进度。
type resumableDownload struct {
	file     *os.File
	hash     hash.Hash
	done     int64
	total    int64
	progress func(done, total int64)
}

// fetch 从 d.done 处接着下,直到读完或出错。
func (d *resumableDownload) fetch(parent context.Context, assetURL string) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, assetURL, nil)
	if err != nil {
		return downloadFatal{err}
	}
	req.Header.Set("User-Agent", "CommBox/"+Version)
	if d.done > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", d.done))
	}
	// 建连到收齐响应头由 Transport 的拨号、握手、响应头超时把关。下载要从
	// github.com 跳转到资源 CDN,两次慢握手就可能要四五十秒,不能算进停滞超时。
	resp, err := (&http.Client{Transport: updateHTTPTransport()}).Do(req)
	if err != nil {
		return fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case d.done > 0 && resp.StatusCode == http.StatusPartialContent &&
		strings.HasPrefix(resp.Header.Get("Content-Range"), fmt.Sprintf("bytes %d-", d.done)):
		// 接着断点往后写
	case resp.StatusCode == http.StatusOK:
		// 首次下载,或服务器不认 Range 发来了整个文件:从头写
		if err := d.restart(); err != nil {
			return downloadFatal{err}
		}
		if d.total <= 0 {
			d.total = resp.ContentLength
		}
	default:
		return downloadRefused{fmt.Errorf("下载失败: 服务器返回 %s", resp.Status)}
	}
	// 响应头到了以后只看数据是否还在流动。
	stall := time.AfterFunc(downloadStallTimeout, cancel)
	defer stall.Stop()
	buf := make([]byte, 64<<10)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			stall.Reset(downloadStallTimeout)
			if _, err := d.file.Write(buf[:n]); err != nil {
				return downloadFatal{err}
			}
			d.hash.Write(buf[:n])
			d.done += int64(n)
			if d.progress != nil {
				d.progress(d.done, d.total)
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			if parent.Err() == nil && ctx.Err() != nil {
				return fmt.Errorf("下载中断: 超过 %v 没有收到数据", downloadStallTimeout)
			}
			return fmt.Errorf("下载中断: %w", readErr)
		}
	}
}
func (d *resumableDownload) restart() error {
	if err := d.file.Truncate(0); err != nil {
		return err
	}
	if _, err := d.file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	d.hash.Reset()
	d.done = 0
	return nil
}
