//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"serial-tool/internal/wincore"
)

func TestUpdateSummary(t *testing.T) {
	newer := wincore.UpdateInfo{Version: "0.8.5", Newer: true}
	if got := updateSummary(newer, "0.8.4"); !strings.Contains(got, "0.8.5") || !strings.Contains(got, "0.8.4") {
		t.Fatalf("有新版本时应同时给出两个版本号,得到 %q", got)
	}
	same := wincore.UpdateInfo{Version: "0.8.4"}
	if got := updateSummary(same, "0.8.4"); !strings.Contains(got, "已是最新") {
		t.Fatalf("同版本文案错误: %q", got)
	}
}

func TestUpdateDetail(t *testing.T) {
	info := wincore.UpdateInfo{Name: "CommBox v0.8.5", AssetName: "CommBox-0.8.5-Windows-x64.zip", AssetSize: 6369803}
	got := updateDetail(info)
	for _, want := range []string{"CommBox v0.8.5", "CommBox-0.8.5-Windows-x64.zip", "MB"} {
		if !strings.Contains(got, want) {
			t.Fatalf("详情缺少 %q: %q", want, got)
		}
	}
	// 没有安装包时只显示标题,不能出现空括号或 0 B。
	if got := updateDetail(wincore.UpdateInfo{Name: "只有说明"}); got != "只有说明" {
		t.Fatalf("无安装包时详情错误: %q", got)
	}
}

func TestUpdateProgress(t *testing.T) {
	got := updateProgress(3<<20, 6<<20)
	if !strings.Contains(got, "50%") {
		t.Fatalf("进度百分比错误: %q", got)
	}
	// 总长度未知时不能出现除零或 NaN。
	if got := updateProgress(1024, 0); !strings.Contains(got, "1.0 KB") || strings.Contains(got, "%") {
		t.Fatalf("未知总长度时文案错误: %q", got)
	}
}

func TestNormalizeNotes(t *testing.T) {
	got := normalizeNotes("第一行\n第二行\r\n第三行\n")
	if strings.Count(got, "\r\n") != 3 {
		t.Fatalf("换行应统一为 3 组 CRLF: %q", got)
	}
	if strings.Contains(strings.ReplaceAll(got, "\r\n", ""), "\n") {
		t.Fatalf("不应残留裸 LF: %q", got)
	}
	if strings.Contains(got, "\r\r") {
		t.Fatalf("原有 CRLF 不应被再加一个 CR: %q", got)
	}
}

func TestUpdateDownloadDir(t *testing.T) {
	dir := updateDownloadDir()
	if dir == "" || !filepath.IsAbs(dir) {
		t.Fatalf("下载目录应为绝对路径: %q", dir)
	}
	// 取不到用户目录时退回临时目录,这两种结果都必须可用。
	if home, err := os.UserHomeDir(); err == nil {
		downloads := filepath.Join(home, "Downloads")
		if info, err := os.Stat(downloads); err == nil && info.IsDir() && dir != downloads {
			t.Fatalf("存在下载文件夹时应优先使用它: %q", dir)
		}
	}
}
