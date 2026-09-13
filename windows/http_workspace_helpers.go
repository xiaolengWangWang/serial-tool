//go:build windows

package main

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"serial-tool/internal/wincore"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type httpWorkspaceFields struct {
	base                                                wincore.HTTPRequestSpec
	method, url, headers, body, timeout, connectTimeout string
	preserveBody, follow, insecure                      bool
}

func (f httpWorkspaceFields) spec() (wincore.HTTPRequestSpec, error) {
	s := f.base
	s.URL = strings.TrimSpace(f.url)
	u, err := url.Parse(s.URL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return s, fmt.Errorf("请输入有效的 http:// 或 https:// 地址")
	}
	s.Method = strings.ToUpper(strings.TrimSpace(f.method))
	if s.Method == "" {
		s.Method = "GET"
	}
	s.Headers = make(http.Header)
	for i, line := range strings.Split(strings.ReplaceAll(f.headers, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		key = strings.TrimSpace(key)
		if !ok || key == "" || strings.ContainsAny(value, "\r\x00") {
			return s, fmt.Errorf("请求头第 %d 行格式无效", i+1)
		}
		for _, c := range key {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", c)) {
				return s, fmt.Errorf("请求头第 %d 行名称无效", i+1)
			}
		}
		s.Headers.Add(key, strings.TrimSpace(value))
	}
	duration := func(text string) (time.Duration, error) {
		n, e := strconv.ParseFloat(strings.TrimSpace(text), 64)
		if e != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 86400 {
			return 0, fmt.Errorf("超时须为 0 到 86400 秒（0 使用引擎默认值）")
		}
		return time.Duration(n * float64(time.Second)), nil
	}
	if s.Timeout, err = duration(f.timeout); err != nil {
		return s, err
	}
	if s.ConnectTimeout, err = duration(f.connectTimeout); err != nil {
		return s, err
	}
	s.FollowRedirects = f.follow
	s.Insecure = f.insecure
	if !f.preserveBody {
		s.Data = nil
		s.Form = nil
		s.Body = []byte(f.body)
	}
	return s, nil
}

func httpWorkspaceRequest(ctx context.Context, e *wincore.Engine, f httpWorkspaceFields) (wincore.HTTPResponseResult, error) {
	s, err := f.spec()
	if err != nil {
		return wincore.HTTPResponseResult{}, err
	}
	return e.DoHTTPRequest(ctx, s)
}

func httpWorkspaceBounded(s string) string {
	const limit = 256 * 1024
	if len(s) <= limit {
		return strings.ToValidUTF8(s, "�")
	}
	end := limit
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return strings.ToValidUTF8(s[:end], "�") + "\r\n… 显示已截断（最多 256 KiB）"
}

func httpWorkspaceHeaders(h http.Header) string {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		for _, v := range h[k] {
			fmt.Fprintf(&b, "%s: %s\r\n", k, v)
		}
	}
	return b.String()
}
