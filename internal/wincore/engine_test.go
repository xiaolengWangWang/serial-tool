package wincore

import (
	"bytes"
	"net"
	"testing"
	"time"
)

func TestParseData(t *testing.T) {
	hexData, err := ParseData("01 03 00 FF", true, "无")
	if err != nil || !bytes.Equal(hexData, []byte{1, 3, 0, 255}) {
		t.Fatalf("HEX 解析失败: % X, %v", hexData, err)
	}
	text, err := ParseData("AT", false, "CRLF")
	if err != nil || string(text) != "AT\r\n" {
		t.Fatalf("字符串解析失败: %q, %v", text, err)
	}
}

func TestTCPClientSend(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	received := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			received <- ""
			return
		}
		defer conn.Close()
		buf := make([]byte, 4)
		_, _ = conn.Read(buf)
		received <- string(buf)
	}()
	engine, err := New(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	if err = engine.Connect(Config{Mode: ModeTCPClient, Address: listener.Addr().String(), Baud: 115200, DataBits: 8, StopBits: 1}); err != nil {
		t.Fatal(err)
	}
	if err = engine.Send("ping", false, "无"); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-received:
		if got != "ping" {
			t.Fatalf("TCP 发送失败: %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("TCP 发送超时")
	}
}

func TestStoreRawAndText(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.StartSession("UDP 服务端", ":9000", "test"); err != nil {
		t.Fatal(err)
	}
	raw := []byte{0, 0xff, 'A'}
	if err = store.Received("UDP 127.0.0.1:1234", raw); err != nil {
		t.Fatal(err)
	}
	var saved []byte
	var text string
	var valid bool
	if err = store.db.QueryRow(`SELECT raw_data, text_data, is_utf8 FROM received_data ORDER BY id DESC LIMIT 1`).Scan(&saved, &text, &valid); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(saved, raw) || valid || text == "" {
		t.Fatalf("SQLite 数据错误: raw=% X text=%q utf8=%v", saved, text, valid)
	}
}
