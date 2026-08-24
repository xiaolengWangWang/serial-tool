package main

/*
#cgo darwin LDFLAGS: -framework Cocoa
#include <stdlib.h>
#include "app.h"
*/
import "C"

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"serial-tool/internal/wincore"
)

var engine *wincore.Engine
var hexMode atomic.Bool

var loopCancel chan struct{}
var loopMu sync.Mutex

func main() {
	runtime.LockOSThread()
	configDir, err := os.UserConfigDir()
	if err != nil {
		return
	}
	engine, err = wincore.New(filepath.Join(configDir, "CommBox", "data"), onData, onLog)
	if err != nil {
		return
	}
	engine.SetOnClosed(onClosed)
	defer engine.Close()
	C.RunApp()
}

func timestamp() string { return time.Now().Format("15:04:05.000") }

func toASCII(data []byte) string {
	b := make([]byte, len(data))
	for i, c := range data {
		if c >= 32 && c < 127 {
			b[i] = c
		} else {
			b[i] = '.'
		}
	}
	return string(b)
}

func addPacket(dir string, data []byte) {
	ts := C.CString(timestamp())
	cdir := C.CString(dir)
	hex := C.CString(fmt.Sprintf("% X", data))
	ascii := C.CString(toASCII(data))
	C.UIAddPacket(ts, cdir, hex, ascii, C.int(len(data)))
	C.free(unsafe.Pointer(ts))
	C.free(unsafe.Pointer(cdir))
	C.free(unsafe.Pointer(hex))
	C.free(unsafe.Pointer(ascii))
}

func onData(_ string, data []byte) {
	addPacket("RX", data)
	monLine := C.CString(fmt.Sprintf("[%s 接收] % X\n", timestamp(), data))
	C.UIMonitorAppend(monLine)
	C.free(unsafe.Pointer(monLine))
}

// logSent 把成功发送的数据加入报文表格。
func logSent(input string, asHex bool, eol string) {
	data, err := wincore.ParseData(input, asHex, eol)
	if err != nil {
		return
	}
	addPacket("TX", data)
}

func onClosed() { C.UIConnectionClosed() }

func onLog(text string) {
	s := C.CString("\n[" + text + "]\n")
	C.UIAppendLog(s)
	C.free(unsafe.Pointer(s))
}

//export GoRecentSessions
func GoRecentSessions() *C.char {
	sessions, err := engine.RecentSessions(5)
	if err != nil {
		return C.CString("")
	}
	lines := make([]string, 0, len(sessions))
	for _, s := range sessions {
		// 字段用 \x1f 分隔,记录用 \n 分隔
		lines = append(lines, strings.Join([]string{s.Mode, s.Endpoint, s.Parameters, s.StartedAt}, "\x1f"))
	}
	return C.CString(strings.Join(lines, "\n"))
}

//export GoLocalIP
func GoLocalIP() *C.char {
	return C.CString(wincore.LocalIP())
}

//export GoLocalIPs
func GoLocalIPs() *C.char {
	return C.CString(strings.Join(wincore.LocalIPs(), "\n"))
}

//export GoDatabaseInfo
func GoDatabaseInfo() *C.char {
	return C.CString(engine.DataDir())
}

//export GoVersion
func GoVersion() *C.char {
	return C.CString(wincore.Version)
}

//export GoStats
func GoStats() *C.char {
	st := engine.Stats()
	state := "● 未连接"
	switch st.State {
	case wincore.StateConnecting:
		state = "● 正在连接..."
	case wincore.StateConnected:
		state = "● 已连接"
	case wincore.StateReconnecting:
		state = "● 重连中..."
	case wincore.StateError:
		state = "● 错误"
	}
	if st.State == wincore.StateDisconnected {
		return C.CString(state)
	}
	return C.CString(fmt.Sprintf("%s | RX %s | TX %s | 运行 %s | 重连 %d | 错误 %d",
		state, wincore.FormatBytes(st.RXBytes), wincore.FormatBytes(st.TXBytes),
		wincore.FormatDuration(time.Since(st.StartedAt)), st.Reconnects, st.Errors))
}

//export GoFavoriteNames
func GoFavoriteNames() *C.char {
	return C.CString(strings.Join(engine.FavoriteNames(), "\n"))
}

//export GoSaveFavorite
func GoSaveFavorite(name, value *C.char) *C.char {
	if err := engine.SaveFavorite(C.GoString(name), C.GoString(value)); err != nil {
		return C.CString("错误:" + err.Error())
	}
	return C.CString("")
}

//export GoDeleteFavorite
func GoDeleteFavorite(name *C.char) {
	_ = engine.DeleteFavorite(C.GoString(name))
}

//export GoFavorite
func GoFavorite(name *C.char) *C.char {
	return C.CString(engine.Favorite(C.GoString(name)))
}

//export GoRecentSends
func GoRecentSends() *C.char {
	return C.CString(strings.Join(engine.RecentSends(), "\n"))
}

//export GoChecksum
func GoChecksum(kind, input *C.char) *C.char {
	return C.CString(wincore.ParseToolbox(C.GoString(kind), C.GoString(input)))
}

//export GoListPorts
func GoListPorts() *C.char {
	ports, err := wincore.ListPorts()
	if err != nil {
		return C.CString("错误: " + err.Error())
	}
	return C.CString(strings.Join(ports, "\n"))
}

//export GoConnect
func GoConnect(name *C.char, baud, dataBits, stopBits C.int, parity *C.char, hex C.int) *C.char {
	hexMode.Store(hex != 0)
	if err := engine.Connect(wincore.Config{
		Mode: wincore.ModeSerial, SerialName: C.GoString(name),
		Baud: int(baud), DataBits: int(dataBits), StopBits: int(stopBits),
		Parity: C.GoString(parity),
	}); err != nil {
		return C.CString(err.Error())
	}
	return C.CString("")
}

//export GoListen
func GoListen(address *C.char, hex C.int) *C.char {
	hexMode.Store(hex != 0)
	if err := engine.Connect(wincore.Config{Mode: wincore.ModeTCPServer, Address: C.GoString(address)}); err != nil {
		return C.CString(err.Error())
	}
	return C.CString("")
}

//export GoConnectTCP
func GoConnectTCP(address *C.char, hex C.int) *C.char {
	hexMode.Store(hex != 0)
	if err := engine.Connect(wincore.Config{Mode: wincore.ModeTCPClient, Address: C.GoString(address), AutoReconnect: true}); err != nil {
		return C.CString(err.Error())
	}
	return C.CString("")
}

//export GoListenUDP
func GoListenUDP(address *C.char, hex C.int) *C.char {
	hexMode.Store(hex != 0)
	if err := engine.Connect(wincore.Config{Mode: wincore.ModeUDPServer, Address: C.GoString(address)}); err != nil {
		return C.CString(err.Error())
	}
	return C.CString("")
}

//export GoConnectUDP
func GoConnectUDP(address *C.char, hex C.int) *C.char {
	hexMode.Store(hex != 0)
	if err := engine.Connect(wincore.Config{Mode: wincore.ModeUDPClient, Address: C.GoString(address)}); err != nil {
		return C.CString(err.Error())
	}
	return C.CString("")
}

//export GoStartSerialServer
func GoStartSerialServer(serialName *C.char, baud, dataBits, stopBits C.int, parity, protocol, role, address *C.char, hex C.int) *C.char {
	hexMode.Store(hex != 0)
	if err := engine.Connect(wincore.Config{
		Mode: wincore.ModeSerialServer, SerialName: C.GoString(serialName),
		Baud: int(baud), DataBits: int(dataBits), StopBits: int(stopBits),
		Parity: C.GoString(parity), Protocol: C.GoString(protocol),
		Role: C.GoString(role), Address: C.GoString(address),
	}); err != nil {
		return C.CString(err.Error())
	}
	return C.CString("")
}

//export GoConnectHTTP
func GoConnectHTTP(url *C.char) *C.char {
	if err := engine.Connect(wincore.Config{Mode: wincore.ModeHTTPClient, Address: C.GoString(url)}); err != nil {
		return C.CString(err.Error())
	}
	return C.CString("")
}

// vserialInfoString 把虚拟串口信息序列化成返回给 Objective-C 的 "id\x1f端点\x1f设备路径" 字符串。
func vserialInfoString(info wincore.VSerialInfo) string {
	return fmt.Sprintf("%d\x1f%s\x1f%s", info.ID, info.Addr, info.Link)
}

//export GoAddVSerial
func GoAddVSerial(address *C.char) *C.char {
	info, err := engine.AddVirtualSerial(C.GoString(address))
	if err != nil {
		return C.CString("错误:" + err.Error())
	}
	return C.CString(vserialInfoString(info))
}

//export GoRemoveVSerial
func GoRemoveVSerial(id C.int) {
	engine.RemoveVirtualSerial(int(id))
}

//export GoListVSerialLinks
func GoListVSerialLinks() *C.char {
	infos := engine.ListVirtualSerials()
	links := make([]string, 0, len(infos))
	for _, i := range infos {
		links = append(links, i.Link)
	}
	return C.CString(strings.Join(links, "\n"))
}

//export GoHTTPRequest
func GoHTTPRequest(spec *C.char) *C.char {
	if err := engine.HTTPRequest(C.GoString(spec)); err != nil {
		return C.CString(err.Error())
	}
	return C.CString("")
}

//export GoDisconnect
func GoDisconnect() { engine.Disconnect() }

//export GoSetHexView
func GoSetHexView(enabled C.int) { hexMode.Store(enabled != 0) }

//export GoToggleLoop
func GoToggleLoop(input *C.char, asHex C.int, eol *C.char, count C.int, intervalMs C.int) *C.char {
	loopMu.Lock()
	if loopCancel != nil {
		close(loopCancel)
		loopCancel = nil
		loopMu.Unlock()
		return C.CString("stopped")
	}
	if engine == nil {
		loopMu.Unlock()
		return C.CString("error:未连接")
	}
	cancel := make(chan struct{})
	loopCancel = cancel
	loopMu.Unlock()
	inp := C.GoString(input)
	hex := asHex != 0
	e := C.GoString(eol)
	n := int(count)
	ms := int(intervalMs)
	go func() {
		cnt := 0
		for {
			select {
			case <-cancel:
				return
			default:
			}
			if n > 0 && cnt >= n {
				loopMu.Lock()
				if loopCancel == cancel {
					loopCancel = nil
				}
				loopMu.Unlock()
				C.UILoopDone()
				return
			}
			if err := engine.Send(inp, hex, e); err != nil {
				loopMu.Lock()
				if loopCancel == cancel {
					loopCancel = nil
				}
				loopMu.Unlock()
				C.UILoopDone()
				return
			}
			logSent(inp, hex, e)
			cnt++
			if ms > 0 {
				select {
				case <-cancel:
					return
				case <-time.After(time.Duration(ms) * time.Millisecond):
				}
			}
		}
	}()
	return C.CString("started")
}

//export GoSend
func GoSend(text *C.char, hex C.int, eol *C.char) *C.char {
	input, asHex, e := C.GoString(text), hex != 0, C.GoString(eol)
	if err := engine.Send(input, asHex, e); err != nil {
		return C.CString(err.Error())
	}
	logSent(input, asHex, e)
	return C.CString("")
}

//export GoNetworkSend
func GoNetworkSend(text *C.char, hex C.int, eol *C.char) *C.char {
	input, asHex, e := C.GoString(text), hex != 0, C.GoString(eol)
	if err := engine.SendNetwork(input, asHex, e); err != nil {
		return C.CString(err.Error())
	}
	logSent(input, asHex, e)
	return C.CString("")
}

//export GoUDPSend
func GoUDPSend(text *C.char, hex C.int, eol *C.char) *C.char {
	input, asHex, e := C.GoString(text), hex != 0, C.GoString(eol)
	if err := engine.SendUDP(input, asHex, e); err != nil {
		return C.CString(err.Error())
	}
	logSent(input, asHex, e)
	return C.CString("")
}
