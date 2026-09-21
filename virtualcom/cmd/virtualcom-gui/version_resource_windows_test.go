package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"

	"virtualcom/internal/vcom"
)

// exeFileVersion 读取 PE 文件里的 VERSIONINFO 资源(即"属性 → 详细信息"显示的版本)。
func exeFileVersion(t *testing.T, path string) string {
	t.Helper()
	size, err := windows.GetFileVersionInfoSize(path, nil)
	if err != nil {
		t.Fatalf("%s 没有版本资源: %v", filepath.Base(path), err)
	}
	buf := make([]byte, size)
	if err := windows.GetFileVersionInfo(path, 0, size, unsafe.Pointer(&buf[0])); err != nil {
		t.Fatalf("读取 %s 版本资源: %v", filepath.Base(path), err)
	}
	var ptr unsafe.Pointer
	var n uint32
	// 0804/04B0 与 versioninfo.json 里的 Translation 一致。
	if err := windows.VerQueryValue(unsafe.Pointer(&buf[0]), `\StringFileInfo\080404B0\FileVersion`, unsafe.Pointer(&ptr), &n); err != nil {
		t.Fatalf("查询 %s 的 FileVersion: %v", filepath.Base(path), err)
	}
	return windows.UTF16PtrToString((*uint16)(ptr))
}

// TestBuiltVersionResource 确认 rsrc_windows_amd64.syso 已按当前版本重新生成:
// 测试可执行文件会链接本包的 .syso,所以读自己就能验到发布产物里的那份资源。
func TestBuiltVersionResource(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("定位测试程序: %v", err)
	}
	if got := exeFileVersion(t, exe); got != vcom.Version {
		t.Fatalf("exe 版本资源 = %q, 版本常量 = %q;改版本号后需要重新生成 .syso(goversioninfo -64 -o rsrc_windows_amd64.syso versioninfo.json)", got, vcom.Version)
	}
}

// TestVersionInfoJSONMatchesConstant 防止改了版本常量却漏改某个 versioninfo.json。
func TestVersionInfoJSONMatchesConstant(t *testing.T) {
	for _, path := range []string{"versioninfo.json", filepath.Join("..", "virtualcom", "versioninfo.json")} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("读取 %s: %v", path, err)
		}
		var vi struct {
			StringFileInfo struct {
				FileVersion    string
				ProductVersion string
			}
		}
		if err := json.Unmarshal(data, &vi); err != nil {
			t.Fatalf("解析 %s: %v", path, err)
		}
		if vi.StringFileInfo.FileVersion != vcom.Version || vi.StringFileInfo.ProductVersion != vcom.Version {
			t.Errorf("%s 声明 %q/%q, 版本常量 = %q", path, vi.StringFileInfo.FileVersion, vi.StringFileInfo.ProductVersion, vcom.Version)
		}
	}
}
