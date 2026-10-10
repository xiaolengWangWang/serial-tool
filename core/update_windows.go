//go:build windows

package core

import (
	"errors"
	"os"
	"path/filepath"
)

// updateAssetSuffix 选 Windows zip 包:解压即用,发布说明里带它的 SHA256 可校验。
// 程序目录里没有 CommBox-CLI.exe 的是只含主程序的 GUI 包,更新也选 GUI 包,
// 不多装命令行和 VirtualCOM 程序。
func updateAssetSuffix() string {
	if dir, ok := updateInstallDir(); ok {
		if _, err := os.Stat(filepath.Join(dir, "CommBox-CLI.exe")); errors.Is(err, os.ErrNotExist) {
			return "-Windows-x64-GUI.zip"
		}
	}
	return "-Windows-x64.zip"
}
