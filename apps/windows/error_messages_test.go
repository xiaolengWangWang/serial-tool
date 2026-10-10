//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"

	"serial-tool/core"

	"go.bug.st/serial"
)

func TestChineseErrorsPreserveDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{fmt.Errorf("open device: %w", os.ErrPermission), "无法访问"},
		{fmt.Errorf("request: %w", context.DeadlineExceeded), "超时"},
		{&net.DNSError{Err: "no such host", Name: "invalid.example"}, "解析"},
		{errors.New("dial: connection refused"), "连接被拒绝"},
		{errors.New("unexpected EOF"), "操作失败"},
		{errors.New("请选择报文"), "请选择报文"},
	} {
		got := chineseError(tc.err)
		if !strings.Contains(got, tc.want) || !strings.Contains(got, tc.err.Error()) {
			t.Errorf("%v: %q", tc.err, got)
		}
	}
	if chineseError(nil) != "" {
		t.Fatal("nil error produced a message")
	}
}

// 打不开的串口要说出是哪个端口；不存在的端口提示检查转换线并刷新。
func TestChineseErrorNamesSerialPort(t *testing.T) {
	_, err := core.OpenSerialPort("COM250", &serial.Mode{BaudRate: 9600})
	got := chineseError(err)
	if !strings.Contains(got, "找不到 COM250") || !strings.Contains(got, "刷新") {
		t.Fatalf("message = %q", got)
	}
}
