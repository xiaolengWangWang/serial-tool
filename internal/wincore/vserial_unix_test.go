//go:build !windows

package wincore

import (
	"errors"
	"testing"

	"golang.org/x/term"
)

// TestVirtualSerialMakeRawFailure 验证:raw 模式设置失败时终止创建、返回错误,不残留映射。
// 仅适用于 PTY 后端(macOS/Linux);Windows 的 com0com 后端不走 makeRaw。
func TestVirtualSerialMakeRawFailure(t *testing.T) {
	engine, err := New(t.TempDir(), nil, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()

	old := makeRaw
	makeRaw = func(fd int) (*term.State, error) {
		return nil, errors.New("模拟 raw 模式失败")
	}
	defer func() { makeRaw = old }()

	info, err := engine.AddVirtualSerial("127.0.0.1:1")
	if err == nil {
		t.Fatal("MakeRaw 失败时 AddVirtualSerial 应返回错误")
	}
	if info.Link != "" {
		t.Fatalf("失败时不应返回设备路径,实际 %q", info.Link)
	}
	if got := engine.ListVirtualSerials(); len(got) != 0 {
		t.Fatalf("失败后不应残留映射,实际 %d", len(got))
	}
}
