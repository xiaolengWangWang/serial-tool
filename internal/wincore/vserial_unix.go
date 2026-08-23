//go:build !windows

package wincore

import (
	"fmt"
	"os"
	"sync/atomic"

	"github.com/creack/pty"
)

var vserialSeq int32

// newVserialDevice 创建基于 PTY 的虚拟串口设备(macOS/Linux)。
func newVserialDevice() (*vserialDevice, error) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		return nil, fmt.Errorf("创建虚拟串口失败(该平台可能不支持): %w", err)
	}
	// raw 模式:关回显与行规程,避免二进制数据被改写或形成回显环路。
	// 失败即终止创建并清理资源,不允许降级运行(二进制透明传输依赖 raw 模式)。
	if _, err := makeRaw(int(tty.Fd())); err != nil {
		name := tty.Name()
		_ = ptmx.Close()
		_ = tty.Close()
		return nil, fmt.Errorf("虚拟串口 raw 模式设置失败(%s): %w", name, err)
	}
	// 设备名带进程 PID + 序号,保证多实例(多进程)不会撞名、互相覆盖软链
	link := fmt.Sprintf("/tmp/CommBox-vserial-%d-%d", os.Getpid(), atomic.AddInt32(&vserialSeq, 1))
	_ = os.Remove(link)
	created := os.Symlink(tty.Name(), link) == nil
	if !created {
		link = tty.Name() // 建软链失败则用真实设备路径
	}
	return &vserialDevice{
		master: ptmx,
		link:   link,
		close: func() {
			_ = ptmx.Close()
			_ = tty.Close()
			if created {
				_ = os.Remove(link)
			}
		},
	}, nil
}
