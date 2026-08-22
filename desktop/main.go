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
	engine, err = wincore.New(filepath.Join(configDir, "GoSerialTool", "data"), onData, onLog)
	if err != nil {
		return
	}
	defer engine.Close()
	C.RunApp()
}

func onData(_ string, data []byte) {
	var text string
	if hexMode.Load() {
		text = fmt.Sprintf("% X\n", data)
	} else {
		text = strings.ReplaceAll(strings.ToValidUTF8(string(data), "�"), "\x00", "␀")
	}
	s := C.CString(text)
	C.UIAppend(s)
	C.UIMonitorAppend(s)
	C.free(unsafe.Pointer(s))
}

func onLog(text string) {
	s := C.CString("\n[" + text + "]\n")
	C.UIAppend(s)
	C.free(unsafe.Pointer(s))
}

//export GoDatabaseInfo
func GoDatabaseInfo() *C.char {
	return C.CString(engine.DataDir())
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
	if err := engine.Connect(wincore.Config{Mode: wincore.ModeTCPClient, Address: C.GoString(address)}); err != nil {
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

//export GoDisconnect
func GoDisconnect() { engine.Disconnect() }

//export GoSetHexView
func GoSetHexView(enabled C.int) { hexMode.Store(enabled != 0) }

//export GoSend
func GoSend(text *C.char, hex C.int, eol *C.char) *C.char {
	if err := engine.Send(C.GoString(text), hex != 0, C.GoString(eol)); err != nil {
		return C.CString(err.Error())
	}
	return C.CString("")
}

//export GoNetworkSend
func GoNetworkSend(text *C.char, hex C.int, eol *C.char) *C.char {
	if err := engine.SendNetwork(C.GoString(text), hex != 0, C.GoString(eol)); err != nil {
		return C.CString(err.Error())
	}
	return C.CString("")
}

//export GoUDPSend
func GoUDPSend(text *C.char, hex C.int, eol *C.char) *C.char {
	if err := engine.SendUDP(C.GoString(text), hex != 0, C.GoString(eol)); err != nil {
		return C.CString(err.Error())
	}
	return C.CString("")
}
