//go:build windows

package vcom

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
)

var deviceSeq atomic.Int64

// device 是一个虚拟串口设备,与 Linux/macOS 版的 vserialDevice 同构:
//
//	Unix   : pty.Open() → ptmx(程序端) + /dev/pts/N(串口软件端),再软链一个稳定名字
//	Windows: CreateNamedPipe → 管道服务端(程序端) + COM 符号链接(串口软件端)
//
// 两边的三件套是一样的:master(程序读写端)、link(用户可见设备名)、close(拆除)。
// 差别在于 Unix 的软件端是内核 pts 字符设备,而这里是命名管道——
// 这正是串口参数类 API 不可用的根因,见 README 兼容性表。
type device struct {
	com  string   // 用户可见名字,如 COM10;对应 Unix 版的 link
	pipe string   // 管道名,如 VirtualCOM-1234-1
	io   *asyncIO // 重叠 I/O 句柄,收发可以同时挂起

	mu        sync.Mutex
	connected bool
	pid       uint32
	proc      string // 占用程序名
	procErr   string // 取不到程序名时的原因,不能用空字符串冒充「没人占用」
	destroyed bool
}

// newDevice 建一个虚拟串口设备:先建管道,再把 COM 名指向它。
// 任一步失败都会把已经建好的部分清理干净,不留半成品。
func newDevice(com string) (*device, error) {
	// 名字带 PID + 序号,保证多实例(多进程)不撞名,与 Unix 版同样的做法。
	pipe := fmt.Sprintf("VirtualCOM-%d-%d", os.Getpid(), deviceSeq.Add(1))

	h, err := createNamedPipe(pipe)
	if err != nil {
		return nil, err
	}
	aio, err := newAsyncIO(h)
	if err != nil {
		syscall.CloseHandle(h)
		return nil, err
	}
	if err := defineDosDevice(com, ntPipePath(pipe)); err != nil {
		aio.close()
		return nil, err
	}
	return &device{com: com, pipe: pipe, io: aio}, nil
}

// accept 阻塞等待串口软件打开这个端口,连上后记录占用方 PID 与程序名。
func (d *device) accept() error {
	if err := d.io.connect(); err != nil {
		return err
	}
	pid, perr := clientPID(d.io.h)

	d.mu.Lock()
	defer d.mu.Unlock()
	d.connected = true
	d.pid = pid
	if perr != nil {
		d.proc, d.procErr = "", fmt.Sprintf("无法获取占用进程: %v", perr)
		return nil
	}
	name, nerr := processName(pid)
	if nerr != nil {
		d.proc, d.procErr = "", fmt.Sprintf("无法获取程序名: %v", nerr)
		return nil
	}
	d.proc, d.procErr = name, ""
	return nil
}

func (d *device) read(p []byte) (int, error)  { return d.io.read(p) }
func (d *device) write(p []byte) (int, error) { return d.io.write(p) }

// dropClient 在串口软件关闭端口后复位管道,使端口可以被再次打开
// (规格 F03 要求支持重复打开关闭)。
func (d *device) dropClient() {
	disconnectNamedPipe(d.io.h)
	d.mu.Lock()
	d.connected = false
	d.pid, d.proc, d.procErr = 0, "", ""
	d.mu.Unlock()
}

// wake 叫醒阻塞在 accept / read / write 上的协程,供拆除时使用。
// 先取消挂起 I/O,再自连一次管道兜底——单靠 CancelIoEx 会和「即将进入阻塞」
// 的那一瞬间赛跑,自连能可靠地把 ConnectNamedPipe 打醒。
func (d *device) wake() {
	d.io.cancel()

	p, err := syscall.UTF16PtrFromString(pipePath(d.pipe))
	if err != nil {
		return
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, 0, 0)
	if err == nil {
		syscall.CloseHandle(h)
	}
}

// destroy 关句柄并删掉 COM 符号链接。必须在相关协程全部退出之后调用。
func (d *device) destroy() {
	d.mu.Lock()
	if d.destroyed {
		d.mu.Unlock()
		return
	}
	d.destroyed = true
	d.mu.Unlock()

	disconnectNamedPipe(d.io.h)
	d.io.close()
	removeDosDevice(d.com, ntPipePath(d.pipe))
}

// occupancy 返回该端口当前的占用情况。
//
// 「有没有程序打开」直接问系统,不依赖 accept 协程的记账:客户端 CreateFile 返回
// 之后,服务端协程还要过一会儿才记上「已连接」,这个窗口里按记账去判断会把
// 正在使用误报成空闲,导致端口刚被打开就允许删除。
//
// 查询本身失败时(既不是「已连接」也不是明确的「未连接」),退回到记账值并在
// 备注里写明原因——宁可报不确定,也不能把使用中说成空闲(规格 2.2)。
func (d *device) occupancy() (connected bool, pid uint32, proc, procErr string) {
	livePID, err := clientPID(d.io.h)

	d.mu.Lock()
	defer d.mu.Unlock()

	switch {
	case err == nil && livePID != 0:
		d.connected = true
		if livePID != d.pid || (d.proc == "" && d.procErr == "") {
			d.pid = livePID
			if name, nerr := processName(livePID); nerr != nil {
				d.proc, d.procErr = "", fmt.Sprintf("无法获取程序名: %v", nerr)
			} else {
				d.proc, d.procErr = name, ""
			}
		}
	case isNotConnectedError(err):
		d.connected = false
		d.pid, d.proc, d.procErr = 0, "", ""
	default:
		d.procErr = fmt.Sprintf("无法确认占用状态: %v", err)
	}
	return d.connected, d.pid, d.proc, d.procErr
}

// isNotConnectedError 判断查询结果是否明确表示「当前没有程序打开这个端口」。
func isNotConnectedError(err error) bool {
	errno, ok := err.(syscall.Errno)
	if !ok {
		return false
	}
	return errno == errBadPipe || errno == errPipeListening || errno == errPipeNotConnected
}

// isTeardownError 判断读写错误是否属于「对端关闭 / 自己在拆除」这类正常退出信号。
func isTeardownError(err error) bool {
	errno, ok := err.(syscall.Errno)
	if !ok {
		return false
	}
	switch errno {
	case errBrokenPipe, errNoData, errOperationAborted, errInvalidHandle:
		return true
	}
	return false
}
