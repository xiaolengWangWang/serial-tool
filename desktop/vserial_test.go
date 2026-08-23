package main

import (
	"strings"
	"testing"

	"serial-tool/internal/wincore"
)

// TestVSerialInfoString 验证 CGo 导出层虚拟串口信息的序列化格式(id\x1f端点\x1f设备路径)。
// 注:Go 不允许在 _test.go 中使用 cgo,故直接测试被导出函数复用的纯 Go 序列化逻辑;
// 添加/删除/重连的核心逻辑已由 internal/wincore 的测试覆盖。
func TestVSerialInfoString(t *testing.T) {
	info := wincore.VSerialInfo{ID: 3, Addr: "127.0.0.1:7000", Link: "/tmp/GoSerialTool-vserial-123-3"}
	got := vserialInfoString(info)
	want := "3\x1f127.0.0.1:7000\x1f/tmp/GoSerialTool-vserial-123-3"
	if got != want {
		t.Fatalf("序列化结果错误: 得到 %q, 期望 %q", got, want)
	}
	if parts := strings.Split(got, "\x1f"); len(parts) != 3 {
		t.Fatalf("应包含 3 个字段,实际 %d", len(parts))
	}
}
