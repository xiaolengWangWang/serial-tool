//go:build windows

package vcom

import (
	"fmt"
	"syscall"
)

// PortClient 以「串口软件」的身份打开一个虚拟端口,用于自检与联调。
//
// 同样使用重叠 I/O:串口软件经常在一个线程收、另一个线程发,
// 同步句柄会让这两件事互相堵死(参见 asyncIO 的注释)。
//
// 注意这里走的是裸 CreateFile + ReadFile/WriteFile,也就是把端口当字节流用。
// 真正的串口库(.NET SerialPort、pyserial 等)还会调用 GetCommState 之类的
// 串口专用 API,那些在用户态实现上会失败,详见 README 兼容性表。
type PortClient struct {
	com string
	io  *asyncIO
}

// OpenPort 打开一个虚拟串口端口。
func OpenPort(com string) (*PortClient, error) {
	p, err := syscall.UTF16PtrFromString(dosPath(com))
	if err != nil {
		return nil, err
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		0, nil, syscall.OPEN_EXISTING, fileFlagOverlapped, 0)
	if err != nil {
		if errno, ok := err.(syscall.Errno); ok && errno == errPipeBusy {
			return nil, fmt.Errorf("打开 %s 失败:端口已被其它程序占用", com)
		}
		return nil, fmt.Errorf("打开 %s 失败: %w", com, err)
	}
	aio, err := newAsyncIO(h)
	if err != nil {
		syscall.CloseHandle(h)
		return nil, err
	}
	return &PortClient{com: com, io: aio}, nil
}

func (c *PortClient) Write(p []byte) (int, error) {
	total := 0
	for total < len(p) {
		n, err := c.io.write(p[total:])
		total += n
		if err != nil {
			return total, err
		}
		if n == 0 {
			return total, fmt.Errorf("%s 写入返回 0 字节", c.com)
		}
	}
	return total, nil
}

func (c *PortClient) Read(p []byte) (int, error) { return c.io.read(p) }

// ReadFull 读满 len(p) 字节,用于校验定长数据。
func (c *PortClient) ReadFull(p []byte) error {
	got := 0
	for got < len(p) {
		n, err := c.Read(p[got:])
		got += n
		if err != nil {
			return fmt.Errorf("%s 读到 %d/%d 字节后失败: %w", c.com, got, len(p), err)
		}
		if n == 0 {
			return fmt.Errorf("%s 读到 %d/%d 字节后对端关闭", c.com, got, len(p))
		}
	}
	return nil
}

func (c *PortClient) Close() error {
	c.io.close()
	return nil
}
