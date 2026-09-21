//go:build windows

package wincore

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/sys/windows"
)

func virtualCOMName(name string) string {
	name = strings.ToUpper(strings.TrimSpace(name))
	name = strings.TrimPrefix(name, `\\.\`)
	if !strings.HasPrefix(name, "COM") || !decimalDigits(name[3:]) {
		return ""
	}
	n, err := strconv.ParseUint(name[3:], 10, 32)
	if err != nil || n == 0 {
		return ""
	}
	return fmt.Sprintf("COM%d", n)
}

func decimalDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// Recognize only VirtualCOM's current PID/sequence naming contract, never
// arbitrary pipes or any COM port whose serial configuration happened to fail.
func virtualCOMPipe(target string) string {
	const prefix = `\Device\NamedPipe\VirtualCOM-`
	if len(target) <= len(prefix) || !strings.EqualFold(target[:len(prefix)], prefix) {
		return ""
	}
	parts := strings.Split(target[len(prefix):], "-")
	if len(parts) != 2 || !decimalDigits(parts[0]) || !decimalDigits(parts[1]) {
		return ""
	}
	return target[len(`\Device\NamedPipe\`):]
}

func queryCOMDevices(name string) ([]string, error) {
	var ptr *uint16
	if name != "" {
		var err error
		ptr, err = windows.UTF16PtrFromString(name)
		if err != nil {
			return nil, err
		}
	}
	for size := 1024; size <= 1<<20; size *= 2 {
		buf := make([]uint16, size)
		n, err := windows.QueryDosDevice(ptr, &buf[0], uint32(len(buf)))
		if errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var names []string
		start := 0
		for i, c := range buf[:n] {
			if c == 0 {
				if i > start {
					names = append(names, windows.UTF16ToString(buf[start:i]))
				}
				start = i + 1
			}
		}
		return names, nil
	}
	return nil, errors.New("Windows 设备名称列表过大")
}

func appendVirtualCOMPorts(ports []string) ([]string, error) {
	names, err := queryCOMDevices("")
	if err != nil {
		return ports, fmt.Errorf("枚举 VirtualCOM 端口: %w", err)
	}
	entries, err := os.ReadDir(`\\.\pipe\`)
	if err != nil {
		return ports, fmt.Errorf("枚举 VirtualCOM 管道: %w", err)
	}
	live := make(map[string]bool, len(entries))
	for _, entry := range entries {
		live[strings.ToLower(entry.Name())] = true
	}
	seen := make(map[string]bool, len(ports))
	for _, port := range ports {
		seen[strings.ToUpper(port)] = true
	}
	for _, name := range names {
		com := virtualCOMName(name)
		if com == "" || seen[com] {
			continue
		}
		targets, err := queryCOMDevices(name)
		if err != nil || len(targets) == 0 {
			continue
		}
		// QueryDosDevice returns the current mapping first, then older mappings.
		pipe := virtualCOMPipe(targets[0])
		if pipe != "" && live[strings.ToLower(pipe)] {
			ports = append(ports, com)
			seen[com] = true
		}
	}
	sort.Slice(ports, func(i, j int) bool {
		a, b := strings.ToUpper(ports[i]), strings.ToUpper(ports[j])
		if len(a) != len(b) {
			return len(a) < len(b)
		}
		return a < b
	})
	return ports, nil
}

func openVirtualCOM(name string) (io.ReadWriteCloser, bool, error) {
	com := virtualCOMName(name)
	if com == "" {
		return nil, false, nil
	}
	targets, err := queryCOMDevices(com)
	if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("检查 %s 设备: %w", com, err)
	}
	if len(targets) == 0 {
		return nil, false, nil
	}
	pipe := virtualCOMPipe(targets[0])
	if pipe == "" {
		return nil, false, nil
	}
	// Open the resolved pipe directly. A concurrently changed DOS alias must
	// not redirect the byte-stream adapter onto an unrelated device.
	h, err := windows.CreateFile(windows.StringToUTF16Ptr(`\\.\pipe\`+pipe),
		windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_OVERLAPPED|windows.SECURITY_SQOS_PRESENT|windows.SECURITY_IDENTIFICATION, 0)
	if err != nil {
		switch {
		case errors.Is(err, windows.ERROR_PIPE_BUSY):
			return nil, true, fmt.Errorf("%s 已被其他程序占用: %w", com, err)
		case errors.Is(err, windows.ERROR_FILE_NOT_FOUND):
			return nil, true, fmt.Errorf("%s 的 VirtualCOM 已退出或端口已删除，请重新创建串口对: %w", com, err)
		default:
			return nil, true, fmt.Errorf("打开 VirtualCOM %s: %w", com, err)
		}
	}
	return &virtualCOMPort{handle: h}, true, nil
}

type virtualCOMPort struct {
	handle          windows.Handle
	mu              sync.Mutex // Serializes submission with Close, not completion.
	closed          bool
	pending         sync.WaitGroup
	closeOnce       sync.Once
	closeErr        error
	readMu, writeMu sync.Mutex
}

func (*virtualCOMPort) serialNotice() string {
	return "VirtualCOM 免驱动字节流：波特率、数据位、校验、停止位与控制信号不生效；请保持 VirtualCOM 运行"
}

func (p *virtualCOMPort) Read(b []byte) (int, error) {
	p.readMu.Lock()
	defer p.readMu.Unlock()
	return p.transfer(b, false)
}

func (p *virtualCOMPort) Write(b []byte) (int, error) {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	total := 0
	for total < len(b) {
		n, err := p.transfer(b[total:], true)
		total += n
		if err != nil {
			return total, err
		}
		if n == 0 {
			return total, io.ErrShortWrite
		}
	}
	return total, nil
}

func (p *virtualCOMPort) transfer(b []byte, write bool) (int, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return 0, os.ErrClosed
	}
	if len(b) == 0 {
		p.mu.Unlock()
		return 0, nil
	}
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		p.mu.Unlock()
		return 0, err
	}
	p.pending.Add(1)
	defer p.pending.Done()
	defer windows.CloseHandle(event)
	ov := &windows.Overlapped{HEvent: event}
	var n uint32
	if write {
		err = windows.WriteFile(p.handle, b, &n, ov)
	} else {
		err = windows.ReadFile(p.handle, b, &n, ov)
	}
	p.mu.Unlock()
	if errors.Is(err, windows.ERROR_IO_PENDING) {
		err = windows.GetOverlappedResult(p.handle, ov, &n, true)
	}
	// Both the OVERLAPPED and Go buffer must outlive kernel completion.
	runtime.KeepAlive(ov)
	runtime.KeepAlive(b)
	if errors.Is(err, windows.ERROR_OPERATION_ABORTED) {
		err = os.ErrClosed
	}
	if errors.Is(err, windows.ERROR_BROKEN_PIPE) || errors.Is(err, windows.ERROR_PIPE_NOT_CONNECTED) || errors.Is(err, windows.ERROR_NO_DATA) || (!write && n == 0 && err == nil) {
		err = io.EOF
	}
	return int(n), err
}

func (p *virtualCOMPort) Close() error {
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.closed = true
		_ = windows.CancelIoEx(p.handle, nil)
		p.mu.Unlock()
		// No submission can race after cancellation. Keep the handle and events
		// alive until every request has completed, then close exactly once.
		p.pending.Wait()
		p.closeErr = windows.CloseHandle(p.handle)
	})
	return p.closeErr
}
