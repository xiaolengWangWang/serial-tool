package wincore

import (
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestSentDataStored 验证:发送的报文入库(source=发送),一条报文一条记录。
func TestSentDataStored(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		if c, err := listener.Accept(); err == nil {
			defer c.Close()
			_, _ = c.Read(make([]byte, 64))
		}
	}()

	engine, err := New(t.TempDir(), nil, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	if err := engine.Connect(Config{Mode: ModeTCPClient, Address: listener.Addr().String()}); err != nil {
		t.Fatal(err)
	}
	if err := engine.Send("hello", false, "无"); err != nil {
		t.Fatal(err)
	}
	var text string
	if err := engine.store.db.QueryRow(
		`SELECT text_data FROM received_data WHERE source='发送' ORDER BY id DESC LIMIT 1`).Scan(&text); err != nil {
		t.Fatalf("未查到发送记录: %v", err)
	}
	if text != "hello" {
		t.Fatalf("发送记录内容不符: %q", text)
	}
}

// TestTCPClientAutoReconnect 验证:TCP 客户端远端断开后自动重连,
// 状态先进入 Reconnecting,重连成功后回到 Connected,并计数。
func TestTCPClientAutoReconnect(t *testing.T) {
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
				_ = conn.Close() // 第一次立即断开,模拟远端断开
				continue
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 64)
				for {
					if _, err := c.Read(buf); err != nil {
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

	if err := engine.Connect(Config{Mode: ModeTCPClient, Address: listener.Addr().String(), AutoReconnect: true}); err != nil {
		t.Fatal(err)
	}

	// 远端断开后应进入 Reconnecting
	waitFor(t, 3*time.Second, func() bool { return engine.Stats().State == StateReconnecting })
	if engine.Stats().State != StateReconnecting {
		t.Fatalf("远端断开后应进入 Reconnecting,实际 %v", engine.Stats().State)
	}

	// 自动重连成功后回到 Connected,且重连计数 > 0
	waitFor(t, 6*time.Second, func() bool {
		return engine.Stats().State == StateConnected && atomic.LoadInt32(&accepted) >= 2
	})
	if engine.Stats().State != StateConnected {
		t.Fatalf("应自动重连成功回到 Connected,实际 %v", engine.Stats().State)
	}
	if engine.Stats().Reconnects == 0 {
		t.Fatal("重连计数应为 0 以上")
	}
}

// waitFor 轮询直到 cond 为真或超时。
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestTCPClientRoundTrip 演示 TCP 客户端完整往返:
// 起一个 echo 服务端 -> Engine 以 TCP 客户端连接 -> 发送 -> 通过 onData 收到回显。
func TestTCPClientRoundTrip(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	// echo 服务端:收到什么原样发回
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 256)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				_, _ = conn.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()

	var mu sync.Mutex
	var received []byte
	gotData := make(chan struct{}, 1)
	onData := func(source string, data []byte) {
		mu.Lock()
		received = append(received, data...)
		mu.Unlock()
		select {
		case gotData <- struct{}{}:
		default:
		}
	}

	engine, err := New(t.TempDir(), onData, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()

	if err := engine.Connect(Config{Mode: ModeTCPClient, Address: listener.Addr().String()}); err != nil {
		t.Fatalf("TCP 客户端连接失败: %v", err)
	}
	t.Logf("已连接到 echo 服务端 %s", listener.Addr())

	if err := engine.Send("Hello Modbus", false, ""); err != nil {
		t.Fatalf("发送失败: %v", err)
	}
	t.Log("已发送: Hello Modbus")

	select {
	case <-gotData:
	case <-time.After(2 * time.Second):
		t.Fatal("超时:未收到 echo 回显")
	}

	mu.Lock()
	got := string(received)
	mu.Unlock()
	if got != "Hello Modbus" {
		t.Fatalf("回显不匹配: 收到 %q", got)
	}
	t.Logf("收到回显: %q  ✓ 往返成功", got)
}
