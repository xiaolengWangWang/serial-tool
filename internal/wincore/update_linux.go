//go:build linux

package wincore

import "runtime"

// updateAssetSuffix 挑 Linux CLI 裸二进制:发布名形如 commbox-linux-amd64。
// Linux 只发命令行版,附件就是可执行文件本身,没有压缩包。
func updateAssetSuffix() string {
	return "commbox-linux-" + runtime.GOARCH
}
