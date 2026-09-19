//go:build windows

package wincore

// Windows 虚拟串口(com0com 桥接)尚未完成:驱动安装、端口命名与 setupc 命令超时
// 三处都没有在真机上验证通过,半成品对外开放只会制造"创建成功但收不到数据"的假象。
// 因此 Windows 端不提供虚拟串口设备,统一返回开发中错误;
// macOS / Linux 的 PTY 实现见 vserial_unix.go,不受影响。
func newVserialDevice() (*vserialDevice, error) {
	return nil, ErrVSerialDeveloping
}
