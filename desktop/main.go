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
	"sync/atomic"
	"time"
	"unsafe"

	"serial-tool/internal/wincore"
)

var engine *wincore.Engine
var hexMode atomic.Bool

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

// formatData 按当前 HEX 显示开关格式化一段数据。
func formatData(data []byte) string {
	if hexMode.Load() {
		return fmt.Sprintf("% X", data)
	}
	return strings.ReplaceAll(strings.ToValidUTF8(string(data), "�"), "\x00", "␀")
}

func timestamp() string { return time.Now().Format("15:04:05.000") }

func onData(_ string, data []byte) {
	line := fmt.Sprintf("[%s 接收] %s\n", timestamp(), formatData(data))
	s := C.CString(line)
	C.UIAppend(s)
	C.UIMonitorAppend(s)
	C.free(unsafe.Pointer(s))
}

// logSent 把成功发送的数据带时间戳写入接收区(不进监控窗口)。
func logSent(input string, asHex bool, eol string) {
	data, err := wincore.ParseData(input, asHex, eol)
	if err != nil {
		return
	}
	line := fmt.Sprintf("[%s 发送] %s\n", timestamp(), formatData(data))
	s := C.CString(line)
	C.UIAppend(s)
	C.free(unsafe.Pointer(s))
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
	data, err := wincore.HexToBytes(C.GoString(input))
	if err != nil {
		return C.CString("输入不是有效的 HEX")
	}
	var result string
	switch C.GoString(kind) {
	case "modbus":
		c := wincore.CRC16Modbus(data)
		result = fmt.Sprintf("0x%04X (低字节在前: %02X %02X)", c, byte(c), byte(c>>8))
	case "crc16":
		result = fmt.Sprintf("0x%04X", wincore.CRC16CCITT(data))
	case "crc32":
		result = fmt.Sprintf("0x%08X", wincore.CRC32(data))
	case "xor":
		result = fmt.Sprintf("0x%02X", wincore.XORChecksum(data))
	case "sum":
		result = fmt.Sprintf("0x%02X", wincore.SUMChecksum(data))
	}
	return C.CString(result)
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
