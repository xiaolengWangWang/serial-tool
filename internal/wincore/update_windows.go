//go:build windows

package wincore

// updateAssetSuffix 选 Windows zip 包:解压即用,发布说明里带它的 SHA256 可校验。
func updateAssetSuffix() string {
	return "-Windows-x64.zip"
}
