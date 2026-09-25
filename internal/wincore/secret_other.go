//go:build !windows

package wincore

// 非 Windows 默认没有系统凭据:macOS 钥匙串需要 cgo,由桌面版在启动时用
// SetSecretStore 接入;CLI 不用 AI 设置。
func platformSecretStore() SecretStore { return nil }
