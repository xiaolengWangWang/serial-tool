//go:build windows

package wincore

import (
	"embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

//go:embed driver
var driverFS embed.FS

// InstallCom0comDriver 解压内嵌的 com0com 驱动并请求管理员权限安装。
func InstallCom0comDriver() error {
	entries, err := driverFS.ReadDir("driver")
	if err != nil {
		return fmt.Errorf("读取内嵌驱动失败: %w", err)
	}
	dir, err := os.MkdirTemp("", "commbox-com0com")
	if err != nil {
		return err
	}

	// 按架构优先选 x64，其次 x86，最后任意 exe
	archTag := "x64"
	if runtime.GOARCH == "386" {
		archTag = "x86"
	}

	setup := ""
	fallback := ""
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := driverFS.ReadFile("driver/" + e.Name())
		if err != nil {
			continue
		}
		dst := filepath.Join(dir, e.Name())
		if err := os.WriteFile(dst, data, 0o755); err != nil {
			continue
		}
		if filepath.Ext(e.Name()) == ".exe" {
			if strings.Contains(e.Name(), archTag) {
				setup = dst
			} else {
				fallback = dst
			}
		}
	}
	if setup == "" {
		setup = fallback
	}
	if setup == "" {
		os.RemoveAll(dir)
		return fmt.Errorf("内嵌驱动中未找到安装程序(.exe),请将 com0com 安装文件放入 driver 目录后重新构建")
	}
	// -Wait 确保安装程序运行完毕后再返回，避免临时目录提前被清理
	err = exec.Command("powershell", "Start-Process", "-Verb", "RunAs", "-Wait", "-FilePath", setup).Run()
	os.RemoveAll(dir)
	return err
}

