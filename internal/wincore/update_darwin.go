//go:build darwin

package wincore

import "runtime"

// updateAssetSuffix 按芯片挑 macOS 的 DMG。发布脚本对 arm64 出
// CommBox-macOS-AppleSilicon.dmg、对 amd64 出 CommBox-macOS-Intel.dmg,
// 双击挂载即用。取不准的架构退回 Intel 包(Rosetta 下也能跑)。
func updateAssetSuffix() string {
	if runtime.GOARCH == "arm64" {
		return "-macOS-AppleSilicon.dmg"
	}
	return "-macOS-Intel.dmg"
}
