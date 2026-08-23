package wincore

import (
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestVirtualSerialReconnect 验证:被桥接的服务端断开后,虚拟串口设备常驻并自动重连。
func TestVirtualSerialReconnect(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var count int32
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			if atomic.AddInt32(&count, 1) == 1 {
				_ = conn.Close() // 第一次立即断开,模拟服务端空闲超时
				continue
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 64)
				for {
					n, err := c.Read(buf)
					if n > 0 {
						_, _ = c.Write(buf[:n])
					}
					if err != nil {
						return
					}
				}
			}(conn)
		}
	}()

	engine, err := New(t.TempDir(), nil, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	info, err := engine.AddVirtualSerial(listener.Addr().String())
	if err != nil {
		t.Skipf("虚拟串口不可用: %v", err)
	}

	time.Sleep(3 * time.Second) // 等待自动重连(重连间隔 2s)
	dev, err := os.OpenFile(info.Link, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("打开虚拟串口失败: %v", err)
	}
	defer dev.Close()
	if _, err := dev.Write([]byte("PING")); err != nil {
		t.Fatal(err)
	}
	_ = dev.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 4)
	n, err := dev.Read(buf)
	if err != nil || string(buf[:n]) != "PING" {
		t.Fatalf("重连后回显失败: %q, %v (accept 次数=%d)", buf[:n], err, atomic.LoadInt32(&count))
	}
}

func echoServer(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 64)
				for {
					n, err := c.Read(buf)
					if n > 0 {
						_, _ = c.Write(buf[:n])
					}
					if err != nil {
						return
					}
				}
			}(conn)
		}
	}()
	return listener
}

// TestVirtualSerialBridge 验证单个后台虚拟串口的双向透传。
func TestVirtualSerialBridge(t *testing.T) {
	listener := echoServer(t)
	defer listener.Close()

	engine, err := New(t.TempDir(), nil, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()

	info, err := engine.AddVirtualSerial(listener.Addr().String())
	if err != nil {
		t.Skipf("虚拟串口不可用(平台可能不支持 PTY): %v", err)
	}
	if info.Link == "" {
		t.Fatal("未创建虚拟串口设备")
	}
	waitVirtualConnected(t, engine, info.ID)

	dev, err := os.OpenFile(info.Link, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("打开虚拟串口失败: %v", err)
	}
	defer dev.Close()

	if _, err := dev.Write([]byte("PING")); err != nil {
		t.Fatal(err)
	}
	_ = dev.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 4)
	n, err := dev.Read(buf)
	if err != nil || string(buf[:n]) != "PING" {
		t.Fatalf("虚拟串口回显失败: %q, %v", buf[:n], err)
	}
}

// TestMultipleVirtualSerials 验证可同时存在多个互相独立的虚拟串口映射。
func TestMultipleVirtualSerials(t *testing.T) {
	l1, l2 := echoServer(t), echoServer(t)
	defer l1.Close()
	defer l2.Close()

	engine, err := New(t.TempDir(), nil, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()

	a, err := engine.AddVirtualSerial(l1.Addr().String())
	if err != nil {
		t.Skipf("虚拟串口不可用: %v", err)
	}
	b, err := engine.AddVirtualSerial(l2.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == b.ID || a.Link == b.Link {
		t.Fatalf("两个映射应互相独立: %+v vs %+v", a, b)
	}
	if got := engine.ListVirtualSerials(); len(got) != 2 {
		t.Fatalf("应有 2 个映射,实际 %d", len(got))
	}
	waitVirtualConnected(t, engine, a.ID)
	waitVirtualConnected(t, engine, b.ID)

	// 两个设备都可独立收发
	for _, info := range []VSerialInfo{a, b} {
		dev, err := os.OpenFile(info.Link, os.O_RDWR, 0)
		if err != nil {
			t.Fatalf("打开 %s 失败: %v", info.Link, err)
		}
		_, _ = dev.Write([]byte("HI"))
		_ = dev.SetReadDeadline(time.Now().Add(2 * time.Second))
		buf := make([]byte, 2)
		n, err := dev.Read(buf)
		_ = dev.Close()
		if err != nil || string(buf[:n]) != "HI" {
			t.Fatalf("设备 #%d 回显失败: %q, %v", info.ID, buf[:n], err)
		}
	}

	// 移除一个,另一个仍在
	engine.RemoveVirtualSerial(a.ID)
	if got := engine.ListVirtualSerials(); len(got) != 1 || got[0].ID != b.ID {
		t.Fatalf("移除后应只剩 #%d,实际 %+v", b.ID, got)
	}
}

// TestVirtualSerialDropCounted 验证:断线/重连期间写入虚拟串口的数据被丢弃时,
// 必须计数并产生告警日志,禁止静默丢失(本版本不缓存、不补发)。
func TestVirtualSerialDropCounted(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	var accepted int32
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			if atomic.AddInt32(&accepted, 1) == 1 {
				_ = conn.Close() // 第一个连接立即断开,模拟服务端空闲超时
				continue
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 64)
				for {
					n, err := c.Read(buf)
					if n > 0 {
						_, _ = c.Write(buf[:n])
					}
					if err != nil {
						return
					}
				}
			}(conn)
		}
	}()

	var mu sync.Mutex
	var logs []string
	engine, err := New(t.TempDir(), nil, func(s string) {
		mu.Lock()
		logs = append(logs, s)
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()

	info, err := engine.AddVirtualSerial(listener.Addr().String())
	if err != nil {
		t.Skipf("虚拟串口不可用: %v", err)
	}

	dev, err := os.OpenFile(info.Link, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("打开虚拟串口失败: %v", err)
	}
	defer dev.Close()

	// 在断线重连窗口(约 2s)内持续写入,期间数据应被丢弃并计数
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		_, _ = dev.Write([]byte("DROPME"))
		time.Sleep(10 * time.Millisecond)
	}

	engine.Lock()
	b := engine.vbridges[info.ID]
	engine.Unlock()
	if b == nil {
		t.Fatal("桥接不存在")
	}
	if dropped := atomic.LoadUint64(&b.dropped); dropped == 0 {
		t.Fatalf("断线期间应有数据被丢弃计数,实际 0")
	}

	mu.Lock()
	dropLogged := false
	for _, s := range logs {
		if strings.Contains(s, "丢弃") {
			dropLogged = true
			break
		}
	}
	mu.Unlock()
	if !dropLogged {
		t.Fatalf("应有丢弃告警日志,实际未捕获到")
	}
}

// TestVirtualSerialLinkUnique 验证:设备名包含进程 PID,保证多实例(多进程)不会撞名;
// 同一实例内多次创建路径也唯一。
func TestVirtualSerialLinkUnique(t *testing.T) {
	listener := echoServer(t)
	defer listener.Close()

	engine, err := New(t.TempDir(), nil, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()

	info, err := engine.AddVirtualSerial(listener.Addr().String())
	if err != nil {
		t.Skipf("虚拟串口不可用: %v", err)
	}
	if !strings.Contains(info.Link, strconv.Itoa(os.Getpid())) {
		t.Fatalf("设备名应包含 PID 以保证多实例唯一,实际 %q", info.Link)
	}

	info2, err := engine.AddVirtualSerial(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if info.Link == info2.Link {
		t.Fatalf("同一实例内设备名应唯一: %q", info.Link)
	}
}

// TestVirtualSerialOfflineCreate 验证:TCP 服务未启动时也能创建虚拟串口,
// 服务上线后无需重建映射即可自动连接并透传。
func TestVirtualSerialOfflineCreate(t *testing.T) {
	// 先拿一个空闲端口,再关闭监听,得到"无服务监听"的地址
	tmp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := tmp.Addr().String()
	_ = tmp.Close()

	engine, err := New(t.TempDir(), nil, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()

	info, err := engine.AddVirtualSerial(addr)
	if err != nil {
		t.Skipf("虚拟串口不可用: %v", err)
	}
	if info.Link == "" {
		t.Fatal("服务未启动时创建虚拟串口也应返回设备路径")
	}

	// 服务上线,等待自动连接(重连间隔 2s)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 64)
				for {
					n, err := c.Read(buf)
					if n > 0 {
						_, _ = c.Write(buf[:n])
					}
					if err != nil {
						return
					}
				}
			}(conn)
		}
	}()

	time.Sleep(4 * time.Second)

	dev, err := os.OpenFile(info.Link, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("打开虚拟串口失败: %v", err)
	}
	defer dev.Close()
	if _, err := dev.Write([]byte("PING")); err != nil {
		t.Fatal(err)
	}
	_ = dev.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 4)
	n, err := dev.Read(buf)
	if err != nil || string(buf[:n]) != "PING" {
		t.Fatalf("服务上线后应自动连接并透传: %q, %v", buf[:n], err)
	}
}

// waitVirtualConnected 等待指定虚拟串口桥接建立 TCP 连接(异步连接建立后再收发)。
func waitVirtualConnected(t *testing.T, e *Engine, id int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		e.Lock()
		b := e.vbridges[id]
		e.Unlock()
		if b != nil {
			b.mu.Lock()
			c := b.conn
			b.mu.Unlock()
			if c != nil {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("等待虚拟串口 #%d 建立连接超时", id)
}
