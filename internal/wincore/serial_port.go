package wincore

import (
	"io"

	"go.bug.st/serial"
)

// OpenSerialPort opens a physical/driver-backed serial port, or a supported
// VirtualCOM byte-stream endpoint on Windows. Callers only require byte I/O;
// the latter deliberately does not pretend to implement serial control APIs.
func OpenSerialPort(name string, mode *serial.Mode) (io.ReadWriteCloser, error) {
	if port, matched, err := openVirtualCOM(name); matched || err != nil {
		return port, err
	}
	return serial.Open(name, mode)
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
