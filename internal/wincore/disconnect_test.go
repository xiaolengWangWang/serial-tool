package wincore

import (
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestClassifyDisconnect(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		manual bool
		side   DisconnectSide
		detail string
	}{
		{"手动断开优先于错误", io.EOF, true, SideLocal, "本端调用了断开"},
		{"本端关闭连接", &net.OpError{Err: net.ErrClosed}, false, SideLocal, "本端已关闭连接"},
		{"对端正常关闭", io.EOF, false, SideRemote, "收到 FIN"},
		{"对端重置(Windows)", &net.OpError{Err: wsaeConnReset}, false, SideRemote, "连接被重置"},
		{"对端重置(Unix)", &net.OpError{Err: syscall.ECONNRESET}, false, SideRemote, "连接被重置"},
		{"本机中止", &net.OpError{Err: wsaeConnAborted}, false, SideLink, "网络栈中止"},
		{"读取超时", os.ErrDeadlineExceeded, false, SideLink, "读取超时"},
		{"没有错误", nil, false, SideUnknown, "没有错误"},
		{"不认识的错误", errors.New("boom"), false, SideUnknown, "未识别"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := classifyDisconnect(c.err, c.manual)
			if got.Side != c.side {
				t.Errorf("Side = %d, 期望 %d", got.Side, c.side)
			}
			if !strings.Contains(got.Detail, c.detail) {
				t.Errorf("Detail = %q, 期望包含 %q", got.Detail, c.detail)
			}
		})
	}
}

// 断开来源必须说成具体角色:我们拨出去的连接,断的是服务端;
// 我们接进来的连接,断的是客户端。
func TestDisconnectWhoNamesTheRole(t *testing.T) {
	remote := classifyDisconnect(io.EOF, false)
	if got := remote.Who(false); got != "服务端断开" {
		t.Errorf("出向连接对端断开 = %q", got)
	}
	if got := remote.Who(true); got != "客户端断开" {
		t.Errorf("入向连接对端断开 = %q", got)
	}
	local := classifyDisconnect(nil, true)
	if got := local.Who(false); got != "本端(客户端)主动断开" {
		t.Errorf("出向连接本端断开 = %q", got)
	}
	if got := local.Who(true); got != "本端(服务端)主动断开" {
		t.Errorf("入向连接本端断开 = %q", got)
	}
}

func TestDisconnectLineCarriesDetail(t *testing.T) {
	line := disconnectLine("TCP 连接已断开", "10.0.0.5:502", false, 83*time.Second,
		disconnectStats{RXBytes: 2048, TXBytes: 100, RXCount: 3, TXCount: 5},
		classifyDisconnect(io.EOF, false))
	for _, want := range []string{"服务端断开", "收到 FIN", "服务端 10.0.0.5:502", "00:01:23", "收 2.0 KB/3 帧", "发 100 B/5 帧", "底层错误 EOF"} {
		if !strings.Contains(line, want) {
			t.Errorf("日志缺少 %q:\n%s", want, line)
		}
	}
}

func TestSerialDisconnectLine(t *testing.T) {
	pulled := serialDisconnectLine("COM7", 5*time.Second, 64, 16, classifySerialDisconnect(errors.New("The device does not exist."), false))
	if !strings.Contains(pulled, "串口已断开:串口设备断开") || !strings.Contains(pulled, "端口 COM7") {
		t.Errorf("设备断开日志不对: %s", pulled)
	}
	// 串口没有对端进程,不能把设备掉线说成"服务端断开"。
	if strings.Contains(pulled, "服务端") || strings.Contains(pulled, "客户端") {
		t.Errorf("串口日志不应出现客户端/服务端角色: %s", pulled)
	}
	manual := serialDisconnectLine("COM7", time.Second, 0, 0, classifySerialDisconnect(nil, true))
	if !strings.Contains(manual, "本端主动断开") {
		t.Errorf("手动断开日志不对: %s", manual)
	}
}

type logSink struct {
	mu    sync.Mutex
	lines []string
}

func (l *logSink) add(line string) {
	l.mu.Lock()
	l.lines = append(l.lines, line)
	l.mu.Unlock()
}

// wait 等到出现包含 want 的日志,返回整行。
func (l *logSink) wait(t *testing.T, want string) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		l.mu.Lock()
		for _, line := range l.lines {
			if strings.Contains(line, want) {
				l.mu.Unlock()
				return line
			}
		}
		l.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	t.Fatalf("没有等到包含 %q 的日志,实际日志:\n%s", want, strings.Join(l.lines, "\n"))
	return ""
}

func TestServerCloseIsReportedAsServerSide(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			close(accepted)
			return
		}
		accepted <- conn
	}()

	sink := &logSink{}
	engine, err := New(t.TempDir(), nil, sink.add)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	if err := engine.Connect(Config{Mode: ModeTCPClient, Address: listener.Addr().String(), Baud: 115200, DataBits: 8, StopBits: 1}); err != nil {
		t.Fatal(err)
	}
	conn, ok := <-accepted
	if !ok {
		t.Fatal("服务端没有接受连接")
	}
	_ = conn.Close() // 服务端主动关闭

	line := sink.wait(t, "TCP 连接已断开")
	if !strings.Contains(line, "服务端断开") {
		t.Errorf("服务端关闭应报告为服务端断开: %s", line)
	}
	if strings.Contains(line, "本端") {
		t.Errorf("服务端关闭不应报告为本端断开: %s", line)
	}
}

func TestClientCloseIsReportedAsClientSide(t *testing.T) {
	sink := &logSink{}
	engine, err := New(t.TempDir(), nil, sink.add)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	if err := engine.Connect(Config{Mode: ModeTCPServer, Address: "127.0.0.1:0", Baud: 115200, DataBits: 8, StopBits: 1}); err != nil {
		t.Fatal(err)
	}
	addr := engine.Stats().Endpoint
	if addr == "" {
		t.Fatal("服务端没有监听地址")
	}
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	sink.wait(t, "TCP 客户端已连接")
	_ = conn.Close() // 客户端主动关闭

	line := sink.wait(t, "TCP 连接已断开")
	if !strings.Contains(line, "客户端断开") {
		t.Errorf("客户端关闭应报告为客户端断开: %s", line)
	}
}

func TestManualDisconnectIsReportedAsLocal(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		<-time.After(3 * time.Second)
		_ = conn.Close()
	}()

	sink := &logSink{}
	engine, err := New(t.TempDir(), nil, sink.add)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	if err := engine.Connect(Config{Mode: ModeTCPClient, Address: listener.Addr().String(), Baud: 115200, DataBits: 8, StopBits: 1}); err != nil {
		t.Fatal(err)
	}
	engine.Disconnect()

	line := sink.wait(t, "连接已断开")
	if !strings.Contains(line, "本端(客户端)主动断开") {
		t.Errorf("手动断开应报告为本端断开: %s", line)
	}
}

// 服务端没有任何客户端连进来时手动断开,本端也应算服务端,不能按"没有入向连接"当成客户端。
func TestManualDisconnectOfIdleServerIsReportedAsServer(t *testing.T) {
	for _, mode := range []Mode{ModeTCPServer, ModeUDPServer} {
		t.Run(string(mode), func(t *testing.T) {
			sink := &logSink{}
			engine, err := New(t.TempDir(), nil, sink.add)
			if err != nil {
				t.Fatal(err)
			}
			defer engine.Close()
			if err := engine.Connect(Config{Mode: mode, Address: "127.0.0.1:0", Baud: 115200, DataBits: 8, StopBits: 1}); err != nil {
				t.Fatal(err)
			}
			engine.Disconnect()

			line := sink.wait(t, "连接已断开")
			if !strings.Contains(line, "本端(服务端)主动断开") {
				t.Errorf("空闲服务端手动断开应报告为本端(服务端): %s", line)
			}
		})
	}
}

// 两端 UI 收到 onClosed 都会调 Disconnect 收尾;对端关闭后不能再补一条"用户在本程序上断开"。
func TestPassiveCloseThenUIDisconnectIsNotReportedAsUser(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	sink := &logSink{}
	engine, err := New(t.TempDir(), nil, sink.add)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	closed := make(chan struct{})
	engine.SetOnClosed(func() { engine.Disconnect(); close(closed) })
	if err := engine.Connect(Config{Mode: ModeTCPClient, Address: listener.Addr().String()}); err != nil {
		t.Fatal(err)
	}
	conn, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("onClosed not called")
	}
	sink.wait(t, "服务端断开")
	sink.mu.Lock()
	defer sink.mu.Unlock()
	for _, line := range sink.lines {
		if strings.Contains(line, "用户在本程序上断开") {
			t.Errorf("对端关闭后误报用户断开: %s", line)
		}
	}
}
