package wincore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// UpdateAPIURL 是版本检查地址。只发 GET,不带任何用户数据,
// 返回的也只是公开的发布信息。
const UpdateAPIURL = "https://api.github.com/repos/xiaolengWangWang/serial-tool/releases/latest"

// updateAssetSuffix 挑选 Windows 安装包。发布页同时还挂着裸 exe,
// 更新走 zip:解压即用,且发布说明里带它的 SHA256 可供校验。
const updateAssetSuffix = "-Windows-x64.zip"

// UpdateInfo 描述一次检查结果。没有可用安装包时 AssetURL 为空,
// 此时界面只提供"打开发布页",不提供下载。
type UpdateInfo struct {
	Version   string // 去掉 v 前缀的版本号
	Name      string // 发布标题
	Notes     string // 发布说明正文
	PageURL   string // 发布页地址
	AssetURL  string // 安装包下载地址
	AssetName string
	AssetSize int64
	SHA256    string // 从发布说明里解析出的安装包校验和,可能为空
	Newer     bool   // 是否比传入的当前版本新
}

// CheckUpdate 查询最新发布版本并与 current 比较。
func CheckUpdate(ctx context.Context, current string) (UpdateInfo, error) {
	return checkUpdateFrom(ctx, UpdateAPIURL, current)
}

func checkUpdateFrom(ctx context.Context, api, current string) (UpdateInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api, nil)
	if err != nil {
		return UpdateInfo{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "CommBox/"+Version)
	resp, err := updateClient().Do(req)
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
		if strings.HasSuffix(a.Name, updateAssetSuffix) && allowedDownloadURL(a.URL) {
			info.AssetURL, info.AssetName, info.AssetSize = a.URL, a.Name, a.Size
			break
		}
	}
	info.SHA256 = sha256FromNotes(info.Notes, info.AssetName)
	return info, nil
}

func updateClient() *http.Client {
	return &http.Client{Timeout: 20 * time.Second}
}

// allowedDownloadURL 只信任 GitHub 自己的下载域名。检查接口是固定地址且走 TLS,
// 但下载地址来自响应正文,限制域名可避免被一个异常响应引到别处。
func allowedDownloadURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, ok := range []string{"github.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com"} {
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
		return "", fmt.Errorf("本次发布没有可下载的 Windows 安装包")
	}
	if !allowedDownloadURL(info.AssetURL) {
		return "", fmt.Errorf("下载地址不是 GitHub 官方域名,已拒绝")
	}
	return downloadAsset(ctx, info, dir, progress)
}

// downloadAsset 是不含域名白名单的下载实现,白名单在 DownloadUpdate 里把关。
func downloadAsset(ctx context.Context, info UpdateInfo, dir string, progress func(done, total int64)) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, filepath.Base(info.AssetName))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, info.AssetURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "CommBox/"+Version)
	// 下载 16 MB 级别的安装包,不能套用检查接口的 20s 超时。
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载失败: 服务器返回 %s", resp.Status)
	}
	total := resp.ContentLength
	if total <= 0 {
		total = info.AssetSize
	}
	// 先写临时文件再改名:中途失败或被取消时不会留下一个看着完整的半截包。
	tmp, err := os.CreateTemp(dir, ".commbox-update-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	hash := sha256.New()
	var done int64
	buf := make([]byte, 64<<10)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := tmp.Write(buf[:n]); err != nil {
				tmp.Close()
				os.Remove(tmpName)
				return "", err
			}
			hash.Write(buf[:n])
			done += int64(n)
			if progress != nil {
				progress(done, total)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			tmp.Close()
			os.Remove(tmpName)
			return "", fmt.Errorf("下载中断: %w", readErr)
		}
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return "", err
	}
	if info.SHA256 != "" {
		if got := hex.EncodeToString(hash.Sum(nil)); got != info.SHA256 {
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
