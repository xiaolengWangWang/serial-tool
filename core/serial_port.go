package core

import (
	"io"
	"strings"

	"go.bug.st/serial"
)

// SerialOpenError 记下打不开的串口名，界面据此给出带端口名的提示，如「COM5 正被其他程序占用」。
type SerialOpenError struct {
	Port string
	Err  error
}

func (e *SerialOpenError) Error() string { return e.Port + ": " + e.Err.Error() }
func (e *SerialOpenError) Unwrap() error { return e.Err }

// OpenSerialPort opens a physical/driver-backed serial port, or a supported
// VirtualCOM byte-stream endpoint on Windows. Callers only require byte I/O;
// the latter deliberately does not pretend to implement serial control APIs.
func OpenSerialPort(name string, mode *serial.Mode) (io.ReadWriteCloser, error) {
	if port, matched, err := openVirtualCOM(name); matched || err != nil {
		return port, err
	}
	port, err := serial.Open(name, mode)
	if err != nil {
		return nil, &SerialOpenError{Port: name, Err: err}
	}
	return port, nil
}

// SerialPortNotice describes settings that do not apply to the open endpoint.
func SerialPortNotice(port io.ReadWriteCloser) string {
	if p, ok := port.(interface{ serialNotice() string }); ok {
		return p.serialNotice()
	}
	return ""
}

func ListPorts() ([]string, error) {
	ports, err := serial.GetPortsList()
	if err != nil {
		return nil, err
	}
	return appendVirtualCOMPorts(ports)
}

// SerialPortInfo 是一个串口及其设备说明（如 USB-SERIAL CH340），说明取不到时为空。
type SerialPortInfo struct {
	Name        string
	Description string
}

// ListPortDetails 与 ListPorts 列出同样的端口，另附设备说明，方便在多个端口里认出
// 转换线。说明只是辅助，取不到不影响端口列表。
func ListPortDetails() ([]SerialPortInfo, error) {
	ports, err := ListPorts()
	if err != nil {
		return nil, err
	}
	descriptions := portDescriptions(ports)
	infos := make([]SerialPortInfo, len(ports))
	for i, name := range ports {
		infos[i] = SerialPortInfo{Name: name, Description: descriptions[strings.ToUpper(name)]}
	}
	return infos, nil
}
