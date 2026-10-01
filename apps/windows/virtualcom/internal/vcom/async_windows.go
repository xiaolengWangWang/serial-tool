//go:build windows

package vcom

import (
	"fmt"
	"syscall"
	"unsafe"
)

// FILE_FLAG_OVERLAPPED:必须带上,否则同一个句柄上的读和写会被内核串行化。
//
// 这是踩过的坑:同步模式下,一个挂起的 ReadFile 会把同句柄上的 WriteFile 一起
// 堵死——读在等对端发数据,写在等读让出句柄,双方互等。串口本来就是全双工,
// 收发必须能同时挂起,所以这里只能走重叠 I/O。
const fileFlagOverlapped = 0x40000000

// asyncIO 把一个句柄包成「读写可并发」的重叠 I/O 句柄。
// 读和写各自持有一个事件对象,互不干扰;取消时用 CancelIoEx 一起打断。
type asyncIO struct {
	h      syscall.Handle
	rEvent syscall.Handle
	wEvent syscall.Handle
}

func newAsyncIO(h syscall.Handle) (*asyncIO, error) {
	r, err := createEvent() // 手动复位,初始未触发
	if err != nil {
		return nil, fmt.Errorf("创建读事件失败: %w", err)
	}
	w, err := createEvent()
	if err != nil {
		syscall.CloseHandle(r)
		return nil, fmt.Errorf("创建写事件失败: %w", err)
	}
	return &asyncIO{h: h, rEvent: r, wEvent: w}, nil
}

// complete 等待一次重叠操作结束,返回实际传输的字节数。
// 操作同步完成(err == nil)时 GetOverlappedResult 会立即返回。
func (a *asyncIO) complete(ov *syscall.Overlapped, start error) (int, error) {
	if start != nil {
		errno, ok := start.(syscall.Errno)
		if !ok || errno != errIOPending {
			return 0, start
		}
	}
	var done uint32
	if err := getOverlappedResult(a.h, ov, &done, true); err != nil {
		return int(done), err
	}
	return int(done), nil
}

func (a *asyncIO) read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	ov := &syscall.Overlapped{HEvent: a.rEvent}
	var done uint32
	err := syscall.ReadFile(a.h, p, &done, ov)
	return a.complete(ov, err)
}

func (a *asyncIO) write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	ov := &syscall.Overlapped{HEvent: a.wEvent}
	var done uint32
	err := syscall.WriteFile(a.h, p, &done, ov)
	return a.complete(ov, err)
}

// connect 等待客户端打开端口(仅服务端管道句柄可用)。
// ConnectNamedPipe 返回 ERROR_PIPE_CONNECTED 表示调用前已经连上,
// 这种情况事件不会被触发,不能去等它,否则会永远挂住。
func (a *asyncIO) connect() error {
	ov := &syscall.Overlapped{HEvent: a.rEvent}
	r, _, e := procConnectNamedPipe.Call(uintptr(a.h), uintptr(unsafe.Pointer(ov)))
	if r != 0 {
		return nil
	}
	errno, ok := e.(syscall.Errno)
	if !ok {
		return e
	}
	switch errno {
	case errPipeConnected:
		return nil
	case errIOPending:
		_, err := a.complete(ov, e)
		return err
	default:
		return e
	}
}

// cancel 取消该句柄上所有挂起的读写,让阻塞中的协程拿到 ERROR_OPERATION_ABORTED。
func (a *asyncIO) cancel() {
	procCancelIoEx.Call(uintptr(a.h), 0)
}

func (a *asyncIO) close() {
	syscall.CloseHandle(a.h)
	syscall.CloseHandle(a.rEvent)
	syscall.CloseHandle(a.wEvent)
}
