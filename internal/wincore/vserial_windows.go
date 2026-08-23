//go:build windows

package wincore

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"

	"go.bug.st/serial"
)

var vserialSeq int32

// newVserialDevice 创建基于 com0com 的虚拟串口对(Windows)。
// com0com 是第三方内核驱动,提供成对的虚拟 COM 口;未安装时返回 ErrVSerialNeedsDriver。
func newVserialDevice() (*vserialDevice, error) {
	if !com0comInstalled() {
		return nil, fmt.Errorf("%w: 请先安装 com0com 驱动(https://com0com.sourceforge.net/)", ErrVSerialNeedsDriver)
	}
	seq := atomic.AddInt32(&vserialSeq, 1)
	comA := fmt.Sprintf("CNCA%d", seq) // 用户串口软件打开这一端
	comB := fmt.Sprintf("CNCB%d", seq) // 应用读写这一端

	// 创建一对虚拟串口(com0com 命令语法可能随版本变化,需在 Windows 上确认)
	if err := runCom0com("install", "PortName="+comA, "PortName="+comB); err != nil {
		return nil, fmt.Errorf("创建虚拟串口失败: %w", err)
	}
	port, err := serial.Open(comB, &serial.Mode{BaudRate: 115200})
	if err != nil {
		_ = runCom0com("remove", "PortName="+comA)
		return nil, fmt.Errorf("打开虚拟串口失败: %w", err)
	}
	return &vserialDevice{
		master: port,
		link:   comA,
		close: func() {
			_ = port.Close()
			_ = runCom0com("remove", "PortName="+comA)
		},
	}, nil
}

// com0comInstalled 检测 com0com 驱动是否已安装。
func com0comInstalled() bool {
	if out, err := exec.Command("sc", "query", "com0com").CombinedOutput(); err == nil {
		s := strings.ToLower(string(out))
		if strings.Contains(s, "running") || strings.Contains(s, "stopped") {
			return true
		}
	}
	for _, dir := range []string{
		`C:\Program Files\com0com`,
		`C:\Program Files (x86)\com0com`,
	} {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return true
		}
	}
	return false
}

// runCom0com 调用 com0com 的 setup 工具执行命令。
func runCom0com(args ...string) error {
	setup := "setupc.exe"
	for _, p := range []string{
		`C:\Program Files\com0com\setupc.exe`,
		`C:\Program Files (x86)\com0com\setupc.exe`,
	} {
		if _, err := os.Stat(p); err == nil {
			setup = p
			break
		}
	}
	return exec.Command(setup, args...).Run()
}
