package wincore

import (
	"net"
	"strings"
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

// TestOnClosedFiresOnRemoteDrop 验证:TCP 客户端模式下远端关闭连接时,
// SetOnClosed 注册的回调会被触发(用于 UI 同步回未连接状态)。
func TestOnClosedFiresOnRemoteDrop(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
	}()

	engine, err := New(t.TempDir(), nil, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()

	var closedCalls atomic.Int32
	fired := make(chan struct{}, 1)
	engine.SetOnClosed(func() {
		closedCalls.Add(1)
		select {
		case fired <- struct{}{}:
		default:
		}
	})

	if err := engine.Connect(Config{Mode: ModeTCPClient, Address: listener.Addr().String()}); err != nil {
		t.Fatal(err)
	}

	// 服务端主动关闭,模拟远端断开
	conn := <-accepted
	_ = conn.Close()

	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("远端断开后 onClosed 未触发")
	}

	// 断开报文应已入库(source=断开),此时会话尚未结束
	var text string
	if err := engine.store.db.QueryRow(
		`SELECT text_data FROM received_data WHERE source='断开' ORDER BY id DESC LIMIT 1`).Scan(&text); err != nil {
		t.Fatalf("未在数据库中查到断开报文: %v", err)
	}
	if !strings.Contains(text, "TCP 客户端已断开") {
		t.Fatalf("断开报文内容不符: %q", text)
	}

	// 用户主动 Disconnect 不应再触发 onClosed
	before := closedCalls.Load()
	engine.Disconnect()
	time.Sleep(100 * time.Millisecond)
	if closedCalls.Load() != before {
		t.Fatalf("用户主动断开不应触发 onClosed: 调用次数从 %d 变为 %d", before, closedCalls.Load())
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
