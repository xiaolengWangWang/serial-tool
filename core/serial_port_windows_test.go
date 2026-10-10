//go:build windows

package core

import (
	"errors"
	"strings"
	"testing"

	"go.bug.st/serial"
)

// 带说明的列表与 ListPorts 一一对应；说明里不再重复端口号。
func TestListPortDetailsMatchesListPorts(t *testing.T) {
	names, err := ListPorts()
	if err != nil {
		t.Skip("无法枚举串口:", err)
	}
	infos, err := ListPortDetails()
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != len(names) {
		t.Fatalf("ListPortDetails 返回 %d 个，ListPorts 返回 %d 个", len(infos), len(names))
	}
	for i, info := range infos {
		if info.Name != names[i] {
			t.Fatalf("第 %d 个端口 %q，应为 %q", i, info.Name, names[i])
		}
		if strings.Contains(strings.ToUpper(info.Description), "("+strings.ToUpper(info.Name)+")") {
			t.Fatalf("说明里重复了端口号: %+v", info)
		}
		t.Logf("%s  %s", info.Name, info.Description)
	}
}

func TestOpenSerialPortErrorNamesThePort(t *testing.T) {
	_, err := OpenSerialPort("COM250", &serial.Mode{BaudRate: 9600})
	var openErr *SerialOpenError
	if !errors.As(err, &openErr) || openErr.Port != "COM250" {
		t.Fatalf("打不开的串口应返回带端口名的 SerialOpenError，得到 %v", err)
	}
}
