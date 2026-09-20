//go:build windows

package vcom

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

var (
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procCreateNamedPipeW      = kernel32.NewProc("CreateNamedPipeW")
	procConnectNamedPipe      = kernel32.NewProc("ConnectNamedPipe")
	procDisconnectNamedPipe   = kernel32.NewProc("DisconnectNamedPipe")
	procDefineDosDeviceW      = kernel32.NewProc("DefineDosDeviceW")
	procQueryDosDeviceW       = kernel32.NewProc("QueryDosDeviceW")
	procGetNamedPipeClientPID = kernel32.NewProc("GetNamedPipeClientProcessId")
	procCancelIoEx            = kernel32.NewProc("CancelIoEx")
	procQueryFullProcessImage = kernel32.NewProc("QueryFullProcessImageNameW")
	procCreateEventW          = kernel32.NewProc("CreateEventW")
	procGetOverlappedResult   = kernel32.NewProc("GetOverlappedResult")
)

// DefineDosDevice 标志位
const (
	dddRawTargetPath      = 0x00000001
	dddRemoveDefinition   = 0x00000002
	dddExactMatchOnRemove = 0x00000004
	dddNoBroadcastSystem  = 0x00000008
)

// 命名管道标志位
const (
	pipeAccessDuplex        = 0x00000003
	pipeTypeByte            = 0x00000000
	pipeReadModeByte        = 0x00000000
	pipeWait                = 0x00000000
	pipeRejectRemoteClients = 0x00000008

	// 单实例:一个虚拟端口同一时刻只允许一个程序打开,
	// 第二个打开者会拿到 ERROR_PIPE_BUSY,对应真实串口的「端口被占用」。
	pipeMaxInstances = 1

	// 收发缓冲区容量,与 Ring 的容量保持一致。
	pipeBufferBytes = 256 * 1024
)

// 需要区分处理的 Win32 错误码
const (
	errPipeConnected        = syscall.Errno(535) // ERROR_PIPE_CONNECTED:调用前客户端已连上,视为成功
	errPipeListening        = syscall.Errno(536) // ERROR_PIPE_LISTENING:本端还没有程序打开
	errPipeBusy             = syscall.Errno(231) // ERROR_PIPE_BUSY:端口已被占用
	errBrokenPipe           = syscall.Errno(109) // ERROR_BROKEN_PIPE:对端已关闭
	errNoData               = syscall.Errno(232) // ERROR_NO_DATA:管道正在关闭
	errInsufficientBuf      = syscall.Errno(122) // ERROR_INSUFFICIENT_BUFFER
	errFileNotFound         = syscall.Errno(2)   // ERROR_FILE_NOT_FOUND
	errOperationAborted     = syscall.Errno(995) // ERROR_OPERATION_ABORTED:I/O 被 CancelIoEx 取消
	errInvalidHandle        = syscall.Errno(6)   // ERROR_INVALID_HANDLE
	errIOPending            = syscall.Errno(997) // ERROR_IO_PENDING:重叠 I/O 已提交,待完成
	errBadPipe              = syscall.Errno(230) // ERROR_BAD_PIPE:管道在监听态,当前没有客户端
	errPipeNotConnected     = syscall.Errno(233) // ERROR_PIPE_NOT_CONNECTED:客户端已断开
	processQueryLimitedInfo = 0x1000
)

// pipePath 返回管道的 Win32 路径,例如 \\.\pipe\VirtualCOM-1234-1。
func pipePath(name string) string { return `\\.\pipe\` + name }

// ntPipePath 返回管道的 NT 设备路径,DefineDosDevice 的目标必须用这种写法。
func ntPipePath(name string) string { return `\Device\NamedPipe\` + name }

// dosPath 返回 COM 口的 Win32 打开路径,例如 \\.\COM10。
// COM10 及以上必须带 \\.\ 前缀才能打开,这是 Win32 的历史包袱。
func dosPath(com string) string { return `\\.\` + com }

// createNamedPipe 建一个字节流模式的单实例命名管道服务端。
func createNamedPipe(name string) (syscall.Handle, error) {
	p, err := syscall.UTF16PtrFromString(pipePath(name))
	if err != nil {
		return 0, err
	}
	r, _, e := procCreateNamedPipeW.Call(
		uintptr(unsafe.Pointer(p)),
		uintptr(pipeAccessDuplex|fileFlagOverlapped),
		uintptr(pipeTypeByte|pipeReadModeByte|pipeWait|pipeRejectRemoteClients),
		uintptr(pipeMaxInstances),
		uintptr(pipeBufferBytes),
		uintptr(pipeBufferBytes),
		0, // 默认超时
		0, // 默认安全描述符:仅当前用户及管理员可访问
	)
	if h := syscall.Handle(r); h != syscall.InvalidHandle {
		return h, nil
	}
	return 0, fmt.Errorf("创建命名管道 %s 失败: %w", pipePath(name), e)
}

func disconnectNamedPipe(h syscall.Handle) error {
	r, _, e := procDisconnectNamedPipe.Call(uintptr(h))
	if r == 0 {
		return e
	}
	return nil
}

// clientPID 返回打开该端口的进程 PID。拿不到时返回 0 和原因,
// 不允许把「获取失败」当成「没人占用」(规格 2.2)。
func clientPID(h syscall.Handle) (uint32, error) {
	var pid uint32
	r, _, e := procGetNamedPipeClientPID.Call(uintptr(h), uintptr(unsafe.Pointer(&pid)))
	if r == 0 {
		return 0, e
	}
	return pid, nil
}

// processName 按 PID 查可执行文件名,用于「显示端口占用程序」。
func processName(pid uint32) (string, error) {
	if pid == 0 {
		return "", fmt.Errorf("PID 为 0")
	}
	h, err := syscall.OpenProcess(processQueryLimitedInfo, false, pid)
	if err != nil {
		return "", err
	}
	defer syscall.CloseHandle(h)

	buf := make([]uint16, syscall.MAX_LONG_PATH)
	size := uint32(len(buf))
	r, _, e := procQueryFullProcessImage.Call(uintptr(h), 0,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if r == 0 {
		return "", e
	}
	return filepath.Base(syscall.UTF16ToString(buf[:size])), nil
}

// defineDosDevice 建立 COM 名到 NT 设备路径的符号链接。
// 未提权时链接建在当前登录会话的设备映射里:同一个登录用户的所有进程都能看到,
// 已实测跨进程可见(另一个进程可以 QueryDosDevice 查到并 CreateFile 打开)。
func defineDosDevice(com, target string) error {
	n, err := syscall.UTF16PtrFromString(com)
	if err != nil {
		return err
	}
	t, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	r, _, e := procDefineDosDeviceW.Call(
		uintptr(dddRawTargetPath|dddNoBroadcastSystem),
		uintptr(unsafe.Pointer(n)), uintptr(unsafe.Pointer(t)))
	if r == 0 {
		return fmt.Errorf("建立 %s -> %s 符号链接失败: %w", com, target, e)
	}
	return nil
}

// removeDosDevice 删除符号链接。带 EXACT_MATCH 以免误删别人建的同名链接。
func removeDosDevice(com, target string) error {
	n, err := syscall.UTF16PtrFromString(com)
	if err != nil {
		return err
	}
	t, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	r, _, e := procDefineDosDeviceW.Call(
		uintptr(dddRemoveDefinition|dddRawTargetPath|dddExactMatchOnRemove),
		uintptr(unsafe.Pointer(n)), uintptr(unsafe.Pointer(t)))
	if r == 0 {
		return fmt.Errorf("删除 %s 符号链接失败: %w", com, e)
	}
	return nil
}

// queryDosDevice 反查一个 DOS 设备名指向的 NT 路径。
func queryDosDevice(com string) (string, error) {
	n, err := syscall.UTF16PtrFromString(com)
	if err != nil {
		return "", err
	}
	buf := make([]uint16, 1024)
	r, _, e := procQueryDosDeviceW.Call(uintptr(unsafe.Pointer(n)),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if r == 0 {
		return "", e
	}
	return syscall.UTF16ToString(buf), nil
}

// listDosDevices 列出当前设备映射里的全部 DOS 设备名。
func listDosDevices() ([]string, error) {
	size := 64 * 1024
	for attempt := 0; attempt < 5; attempt++ {
		buf := make([]uint16, size)
		r, _, e := procQueryDosDeviceW.Call(0, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		if r == 0 {
			if errno, ok := e.(syscall.Errno); ok && errno == errInsufficientBuf {
				size *= 4
				continue
			}
			return nil, e
		}
		return splitDoubleNullList(buf[:r]), nil
	}
	return nil, fmt.Errorf("设备名列表过长,读取失败")
}

// splitDoubleNullList 拆开 Win32 的「双 NUL 结尾字符串数组」。
func splitDoubleNullList(buf []uint16) []string {
	var out []string
	start := 0
	for i, c := range buf {
		if c != 0 {
			continue
		}
		if i > start {
			out = append(out, syscall.UTF16ToString(buf[start:i]))
		}
		start = i + 1
	}
	return out
}

// UsedCOMNames 返回系统当前已占用的 COM 名集合(含真实串口与已建的虚拟口)。
func UsedCOMNames() (map[string]bool, error) {
	names, err := listDosDevices()
	if err != nil {
		return nil, fmt.Errorf("枚举系统设备名失败: %w", err)
	}
	used := make(map[string]bool, len(names))
	for _, n := range names {
		if comNumber(n) > 0 {
			used[strings.ToUpper(n)] = true
		}
	}
	return used, nil
}

// comNumber 从 "COM10" 解析出 10;不是合法 COM 名则返回 0。
func comNumber(name string) int {
	upper := strings.ToUpper(name)
	if !strings.HasPrefix(upper, "COM") {
		return 0
	}
	n, err := strconv.Atoi(upper[3:])
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// createEvent 建一个手动复位、初始未触发的事件对象,供重叠 I/O 等待完成。
// syscall 包没有导出 CreateEvent,这里自己包一层。
func createEvent() (syscall.Handle, error) {
	r, _, e := procCreateEventW.Call(0, 1, 0, 0)
	if r == 0 {
		return 0, e
	}
	return syscall.Handle(r), nil
}

// getOverlappedResult 取一次重叠操作的结果,wait 为真时阻塞到操作结束。
func getOverlappedResult(h syscall.Handle, ov *syscall.Overlapped, done *uint32, wait bool) error {
	var w uintptr
	if wait {
		w = 1
	}
	r, _, e := procGetOverlappedResult.Call(uintptr(h),
		uintptr(unsafe.Pointer(ov)), uintptr(unsafe.Pointer(done)), w)
	if r == 0 {
		return e
	}
	return nil
}
