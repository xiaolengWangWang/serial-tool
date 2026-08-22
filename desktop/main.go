package main

/*
#cgo darwin LDFLAGS: -framework Cocoa
#include <stdlib.h>
#include "app.h"
*/
import "C"

import (
	"encoding/hex"
	"fmt"
	"net"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	"go.bug.st/serial"
)

var state struct {
	sync.Mutex
	port         serial.Port
	listener     net.Listener
	clients      map[net.Conn]struct{}
	udp          *net.UDPConn
	udpPeer      *net.UDPAddr
	udpDialed    bool
	hex          atomic.Bool
	bridge       atomic.Bool
	serialWrite  sync.Mutex
	networkWrite sync.Mutex
}

func main() {
	runtime.LockOSThread()
	_ = openDatabase()
	C.RunApp()
	closeDatabase()
}

//export GoDatabaseInfo
func GoDatabaseInfo() *C.char {
	dir, err := databaseDirectory()
	if err != nil {
		return C.CString("错误: " + err.Error())
	}
	return C.CString(dir)
}

//export GoListPorts
func GoListPorts() *C.char {
	ports, err := serial.GetPortsList()
	if err != nil {
		return C.CString("错误: " + err.Error())
	}
	return C.CString(strings.Join(ports, "\n"))
}

//export GoConnect
func GoConnect(name *C.char, baud, dataBits, stopBits C.int, parity *C.char, hexView C.int) *C.char {
	GoDisconnect()
	mode := &serial.Mode{BaudRate: int(baud), DataBits: int(dataBits)}
	if stopBits == 2 {
		mode.StopBits = serial.TwoStopBits
	} else {
		mode.StopBits = serial.OneStopBit
	}
	switch C.GoString(parity) {
	case "奇校验":
		mode.Parity = serial.OddParity
	case "偶校验":
		mode.Parity = serial.EvenParity
	default:
		mode.Parity = serial.NoParity
	}
	p, err := serial.Open(C.GoString(name), mode)
	if err != nil {
		return C.CString(err.Error())
	}
	state.Lock()
	state.port = p
	state.hex.Store(hexView != 0)
	state.Unlock()
	beginStorageSession("串口", C.GoString(name), fmt.Sprintf("baud=%d,data=%d,parity=%s,stop=%d", baud, dataBits, C.GoString(parity), stopBits))
	go readLoop(p)
	return C.CString("")
}

//export GoListen
func GoListen(address *C.char, hexView C.int) *C.char {
	GoDisconnect()
	listener, err := net.Listen("tcp", C.GoString(address))
	if err != nil {
		return C.CString(err.Error())
	}
	state.Lock()
	state.listener = listener
	state.clients = make(map[net.Conn]struct{})
	state.hex.Store(hexView != 0)
	state.Unlock()
	beginStorageSession("TCP 服务端", C.GoString(address), "")
	go acceptLoop(listener)
	return C.CString("")
}

//export GoConnectTCP
func GoConnectTCP(address *C.char, hexView C.int) *C.char {
	if err := connectTCP(C.GoString(address), hexView != 0); err != nil {
		return C.CString(err.Error())
	}
	return C.CString("")
}

func connectTCP(address string, hexView bool) error {
	GoDisconnect()
	client, err := net.Dial("tcp", address)
	if err != nil {
		return err
	}
	state.Lock()
	state.clients = map[net.Conn]struct{}{client: {}}
	state.hex.Store(hexView)
	state.Unlock()
	beginStorageSession("TCP 客户端", address, "")
	go readClient(client)
	return nil
}

//export GoListenUDP
func GoListenUDP(address *C.char, hexView C.int) *C.char {
	GoDisconnect()
	addr, err := net.ResolveUDPAddr("udp", C.GoString(address))
	if err != nil {
		return C.CString(err.Error())
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return C.CString(err.Error())
	}
	state.Lock()
	state.udp = conn
	state.hex.Store(hexView != 0)
	state.Unlock()
	beginStorageSession("UDP 服务端", C.GoString(address), "")
	go readUDP(conn)
	return C.CString("")
}

//export GoConnectUDP
func GoConnectUDP(address *C.char, hexView C.int) *C.char {
	if err := connectUDP(C.GoString(address), hexView != 0); err != nil {
		return C.CString(err.Error())
	}
	return C.CString("")
}

func connectUDP(address string, hexView bool) error {
	GoDisconnect()
	peer, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		return err
	}
	conn, err := net.DialUDP("udp", nil, peer)
	if err != nil {
		return err
	}
	state.Lock()
	state.udp = conn
	state.udpPeer = peer
	state.udpDialed = true
	state.hex.Store(hexView)
	state.Unlock()
	beginStorageSession("UDP 客户端", address, "")
	go readUDP(conn)
	return nil
}

//export GoStartSerialServer
func GoStartSerialServer(serialName *C.char, baud, dataBits, stopBits C.int, parity, protocol, role, address *C.char, hexView C.int) *C.char {
	GoDisconnect()
	mode := &serial.Mode{BaudRate: int(baud), DataBits: int(dataBits), StopBits: serial.OneStopBit, Parity: serial.NoParity}
	if stopBits == 2 {
		mode.StopBits = serial.TwoStopBits
	}
	switch C.GoString(parity) {
	case "奇校验":
		mode.Parity = serial.OddParity
	case "偶校验":
		mode.Parity = serial.EvenParity
	}
	p, err := serial.Open(C.GoString(serialName), mode)
	if err != nil {
		return C.CString(err.Error())
	}

	proto, server, endpoint := C.GoString(protocol), C.GoString(role) == "服务端", C.GoString(address)
	var listener net.Listener
	var clients map[net.Conn]struct{}
	var udp *net.UDPConn
	var peer *net.UDPAddr
	if proto == "TCP" {
		if server {
			listener, err = net.Listen("tcp", endpoint)
			clients = make(map[net.Conn]struct{})
		} else {
			var client net.Conn
			client, err = net.Dial("tcp", endpoint)
			if err == nil {
				clients = map[net.Conn]struct{}{client: {}}
			}
		}
	} else {
		peer, err = net.ResolveUDPAddr("udp", endpoint)
		if err == nil {
			if server {
				udp, err = net.ListenUDP("udp", peer)
				peer = nil
			} else {
				udp, err = net.DialUDP("udp", nil, peer)
			}
		}
	}
	if err != nil {
		_ = p.Close()
		return C.CString(err.Error())
	}

	state.Lock()
	state.port, state.listener, state.clients = p, listener, clients
	state.udp, state.udpPeer, state.udpDialed = udp, peer, udp != nil && !server
	state.hex.Store(hexView != 0)
	state.bridge.Store(true)
	state.Unlock()
	beginStorageSession("串口服务器", endpoint, fmt.Sprintf("serial=%s,baud=%d,data=%d,parity=%s,stop=%d,protocol=%s,role=%s",
		C.GoString(serialName), baud, dataBits, C.GoString(parity), stopBits, proto, C.GoString(role)))
	go readLoop(p)
	if listener != nil {
		go acceptLoop(listener)
	}
	for client := range clients {
		go readClient(client)
	}
	if udp != nil {
		go readUDP(udp)
	}
	return C.CString("")
}

//export GoDisconnect
func GoDisconnect() {
	state.bridge.Store(false)
	endStorageSession()
	state.Lock()
	p := state.port
	listener := state.listener
	clients := state.clients
	udp := state.udp
	state.port = nil
	state.listener = nil
	state.clients = nil
	state.udp = nil
	state.udpPeer = nil
	state.udpDialed = false
	state.Unlock()
	if p != nil {
		_ = p.Close()
	}
	if listener != nil {
		_ = listener.Close()
	}
	for client := range clients {
		_ = client.Close()
	}
	if udp != nil {
		_ = udp.Close()
	}
}

//export GoSetHexView
func GoSetHexView(enabled C.int) {
	state.hex.Store(enabled != 0)
}

//export GoSend
func GoSend(text *C.char, hexMode C.int, eol *C.char) *C.char {
	data, err := parseData(C.GoString(text), hexMode != 0, C.GoString(eol))
	if err != nil {
		return C.CString("HEX 格式错误: " + err.Error())
	}
	if err = writeSerial(data); err != nil {
		return C.CString(err.Error())
	}
	return C.CString("")
}

//export GoNetworkSend
func GoNetworkSend(text *C.char, hexMode C.int, eol *C.char) *C.char {
	data, err := parseData(C.GoString(text), hexMode != 0, C.GoString(eol))
	if err != nil {
		return C.CString("HEX 格式错误: " + err.Error())
	}
	if err := broadcast(data); err != nil {
		return C.CString(err.Error())
	}
	return C.CString("")
}

//export GoUDPSend
func GoUDPSend(text *C.char, hexMode C.int, eol *C.char) *C.char {
	data, err := parseData(C.GoString(text), hexMode != 0, C.GoString(eol))
	if err != nil {
		return C.CString("HEX 格式错误: " + err.Error())
	}
	if err := sendUDP(data); err != nil {
		return C.CString(err.Error())
	}
	return C.CString("")
}

func broadcast(data []byte) error {
	state.networkWrite.Lock()
	defer state.networkWrite.Unlock()
	state.Lock()
	clients := make([]net.Conn, 0, len(state.clients))
	for client := range state.clients {
		clients = append(clients, client)
	}
	state.Unlock()
	if len(clients) == 0 {
		return fmt.Errorf("暂无 TCP 客户端连接")
	}
	for _, client := range clients {
		if _, err := client.Write(data); err != nil {
			return err
		}
	}
	return nil
}

func sendUDP(data []byte) error {
	state.networkWrite.Lock()
	defer state.networkWrite.Unlock()
	state.Lock()
	conn, peer, dialed := state.udp, state.udpPeer, state.udpDialed
	state.Unlock()
	if conn == nil {
		return fmt.Errorf("UDP 未监听")
	}
	if peer == nil {
		return fmt.Errorf("尚未收到 UDP 数据，无法确定回复目标")
	}
	if dialed {
		_, err := conn.Write(data)
		return err
	}
	_, err := conn.WriteToUDP(data, peer)
	return err
}

func parseData(input string, hexMode bool, eol string) ([]byte, error) {
	if hexMode {
		return hex.DecodeString(strings.Join(strings.Fields(input), ""))
	}
	return []byte(input + map[string]string{"LF": "\n", "CR": "\r", "CRLF": "\r\n"}[eol]), nil
}

func acceptLoop(listener net.Listener) {
	for {
		client, err := listener.Accept()
		if err != nil {
			state.Lock()
			active := state.listener == listener
			state.Unlock()
			if active {
				appendUI("\n[监听错误: " + err.Error() + "]\n")
			}
			return
		}
		state.Lock()
		state.clients[client] = struct{}{}
		state.Unlock()
		appendUI("\n[TCP 客户端已连接: " + client.RemoteAddr().String() + "]\n")
		go readClient(client)
	}
}

func readClient(client net.Conn) {
	defer func() {
		_ = client.Close()
		state.Lock()
		_, active := state.clients[client]
		delete(state.clients, client)
		state.Unlock()
		if active {
			appendUI("\n[TCP 客户端已断开: " + client.RemoteAddr().String() + "]\n")
		}
	}()
	buf := make([]byte, 2048)
	for {
		n, err := client.Read(buf)
		if n > 0 {
			persistReceived("TCP "+client.RemoteAddr().String(), buf[:n])
			if state.bridge.Load() {
				appendUI("\n[网络 → 串口]\n")
				if writeErr := writeSerial(buf[:n]); writeErr != nil {
					appendUI("[串口写入错误: " + writeErr.Error() + "]\n")
				}
			}
			appendReceived(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

func readUDP(conn *net.UDPConn) {
	buf := make([]byte, 65535)
	for {
		n, peer, err := conn.ReadFromUDP(buf)
		if n > 0 {
			persistReceived("UDP "+peer.String(), buf[:n])
			state.Lock()
			state.udpPeer = peer
			state.Unlock()
			appendUI("\n[UDP 来自 " + peer.String() + "]\n")
			if state.bridge.Load() {
				appendUI("[网络 → 串口]\n")
				if writeErr := writeSerial(buf[:n]); writeErr != nil {
					appendUI("[串口写入错误: " + writeErr.Error() + "]\n")
				}
			}
			appendReceived(buf[:n])
		}
		if err != nil {
			state.Lock()
			active := state.udp == conn
			if active {
				state.udp = nil
				state.udpPeer = nil
				state.udpDialed = false
			}
			state.Unlock()
			if active {
				appendUI("\n[UDP 监听错误: " + err.Error() + "]\n")
			}
			return
		}
	}
}

func readLoop(p serial.Port) {
	buf := make([]byte, 2048)
	for {
		n, err := p.Read(buf)
		if n > 0 {
			persistReceived("串口", buf[:n])
			if state.bridge.Load() {
				appendUI("\n[串口 → 网络]\n")
				if writeErr := forwardToNetwork(buf[:n]); writeErr != nil {
					appendUI("[网络写入错误: " + writeErr.Error() + "]\n")
				}
			}
			appendReceived(buf[:n])
		}
		if err != nil {
			state.Lock()
			active := state.port == p
			if active {
				state.port = nil
			}
			state.Unlock()
			if active {
				appendUI("\n[连接已断开: " + err.Error() + "]\n")
			}
			return
		}
	}
}

func writeSerial(data []byte) error {
	state.Lock()
	p := state.port
	state.Unlock()
	if p == nil {
		return fmt.Errorf("串口未连接")
	}
	state.serialWrite.Lock()
	defer state.serialWrite.Unlock()
	_, err := p.Write(data)
	return err
}

func forwardToNetwork(data []byte) error {
	state.Lock()
	clients := make([]net.Conn, 0, len(state.clients))
	for client := range state.clients {
		clients = append(clients, client)
	}
	udp, peer, dialed := state.udp, state.udpPeer, state.udpDialed
	state.Unlock()
	state.networkWrite.Lock()
	defer state.networkWrite.Unlock()
	for _, client := range clients {
		if _, err := client.Write(data); err != nil {
			return err
		}
	}
	if udp != nil && peer != nil {
		if dialed {
			_, err := udp.Write(data)
			return err
		}
		_, err := udp.WriteToUDP(data, peer)
		return err
	}
	return nil
}

func appendReceived(data []byte) {
	var text string
	if state.hex.Load() {
		text = fmt.Sprintf("% X\n", data)
	} else {
		text = strings.ReplaceAll(strings.ToValidUTF8(string(data), "�"), "\x00", "␀")
	}
	appendUI(text)
	appendMonitorUI(text)
}

func appendUI(text string) {
	s := C.CString(text)
	C.UIAppend(s)
	C.free(unsafe.Pointer(s))
}

func appendMonitorUI(text string) {
	s := C.CString(text)
	C.UIMonitorAppend(s)
	C.free(unsafe.Pointer(s))
}

func beginStorageSession(mode, endpoint, parameters string) {
	if err := startStorageSession(mode, endpoint, parameters); err != nil {
		appendUI("\n[SQLite 会话写入失败: " + err.Error() + "]\n")
	}
}

func persistReceived(source string, data []byte) {
	if err := storeReceived(source, data); err != nil {
		appendUI("\n[SQLite 原始数据写入失败: " + err.Error() + "]\n")
	}
}
