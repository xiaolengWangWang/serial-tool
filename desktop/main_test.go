package main

import (
	"bytes"
	"net"
	"testing"
	"time"
)

func TestParseData(t *testing.T) {
	hexData, err := parseData("01 03 00 FF", true, "无")
	if err != nil || !bytes.Equal(hexData, []byte{1, 3, 0, 255}) {
		t.Fatalf("HEX 解析失败: % X, %v", hexData, err)
	}
	text, err := parseData("AT", false, "CRLF")
	if err != nil || string(text) != "AT\r\n" {
		t.Fatalf("文本解析失败: %q, %v", text, err)
	}
}

func TestBroadcast(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	state.Lock()
	state.clients = map[net.Conn]struct{}{server: {}}
	state.Unlock()
	defer func() {
		state.Lock()
		state.clients = nil
		state.Unlock()
	}()

	done := make(chan error, 1)
	go func() { done <- broadcast([]byte("ping")) }()
	_ = client.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 4)
	if _, err := client.Read(buf); err != nil || string(buf) != "ping" {
		t.Fatalf("TCP 广播失败: %q, %v", buf, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSerialServerForwardsToTCP(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	state.Lock()
	state.clients = map[net.Conn]struct{}{server: {}}
	state.Unlock()
	defer func() {
		state.Lock()
		state.clients = nil
		state.Unlock()
	}()

	done := make(chan error, 1)
	go func() { done <- forwardToNetwork([]byte("serial-data")) }()
	_ = client.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, len("serial-data"))
	if _, err := client.Read(buf); err != nil || string(buf) != "serial-data" {
		t.Fatalf("串口服务器 TCP 透传失败: %q, %v", buf, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestUDPSend(t *testing.T) {
	server, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	client, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	state.Lock()
	state.udp = server
	state.udpPeer = client.LocalAddr().(*net.UDPAddr)
	state.Unlock()
	defer func() {
		state.Lock()
		state.udp, state.udpPeer = nil, nil
		state.Unlock()
	}()

	if err := sendUDP([]byte("pong")); err != nil {
		t.Fatal(err)
	}
	_ = client.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 4)
	if _, _, err := client.ReadFromUDP(buf); err != nil || string(buf) != "pong" {
		t.Fatalf("UDP 发送失败: %q, %v", buf, err)
	}
}

func TestTCPClient(t *testing.T) {
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
	if err := connectTCP(listener.Addr().String(), false); err != nil {
		t.Fatal(err)
	}
	defer GoDisconnect()
	if err := broadcast([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	if got := <-received; got != "ping" {
		t.Fatalf("TCP 客户端发送失败: %q", got)
	}
}

func TestUDPClient(t *testing.T) {
	server, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if err := connectUDP(server.LocalAddr().String(), false); err != nil {
		t.Fatal(err)
	}
	defer GoDisconnect()
	if err := sendUDP([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	_ = server.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 4)
	if _, _, err := server.ReadFromUDP(buf); err != nil || string(buf) != "ping" {
		t.Fatalf("UDP 客户端发送失败: %q, %v", buf, err)
	}
}
