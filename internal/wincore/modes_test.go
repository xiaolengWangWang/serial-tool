package wincore

import "testing"

func TestSpecOf(t *testing.T) {
	cases := []struct {
		m    Mode
		want ModeSpec
	}{
		{ModeSerial, ModeSpec{NeedsSerial: true}},
		{ModeTCPServer, ModeSpec{NeedsNet: true, IsServer: true}},
		{ModeTCPClient, ModeSpec{NeedsNet: true}},
		{ModeUDPServer, ModeSpec{NeedsNet: true, IsServer: true}},
		{ModeUDPClient, ModeSpec{NeedsNet: true}},
		{ModeSerialServer, ModeSpec{NeedsSerial: true, NeedsNet: true, NeedsRole: true, NeedsProto: true, IsServer: true}},
		{ModeHTTPClient, ModeSpec{NeedsNet: true}},
	}
	for _, c := range cases {
		if got := SpecOf(c.m); got != c.want {
			t.Errorf("SpecOf(%q) = %+v, want %+v", c.m, got, c.want)
		}
	}
}

func TestBuildConfig(t *testing.T) {
	// 串口模式缺串口名
	if _, err := BuildConfig(ConnParams{Mode: ModeSerial, Baud: 115200, DataBits: 8, StopBits: 1}); err == nil {
		t.Error("串口模式缺串口名应报错")
	}
	// 串口参数无效
	if _, err := BuildConfig(ConnParams{Mode: ModeSerial, SerialName: "/dev/tty", Baud: 0, DataBits: 8, StopBits: 1}); err == nil {
		t.Error("波特率无效应报错")
	}
	// TCP 客户端缺地址
	if _, err := BuildConfig(ConnParams{Mode: ModeTCPClient}); err == nil {
		t.Error("TCP 客户端缺地址应报错")
	}
	// HTTP 缺 URL
	if _, err := BuildConfig(ConnParams{Mode: ModeHTTPClient}); err == nil {
		t.Error("HTTP 缺 URL 应报错")
	}
	// 正常构建
	cfg, err := BuildConfig(ConnParams{Mode: ModeTCPClient, Address: "127.0.0.1:9000"})
	if err != nil || cfg.Mode != ModeTCPClient || cfg.Address != "127.0.0.1:9000" {
		t.Errorf("正常构建失败: %+v, %v", cfg, err)
	}
	// 串口服务器需要串口名 + 地址 + 协议 + 角色
	cfg, err = BuildConfig(ConnParams{Mode: ModeSerialServer, SerialName: "/dev/tty", Address: "127.0.0.1:9000", Baud: 9600, DataBits: 8, StopBits: 1, Protocol: "TCP", Role: "服务端"})
	if err != nil || cfg.Protocol != "TCP" || cfg.Role != "服务端" {
		t.Errorf("串口服务器构建失败: %+v, %v", cfg, err)
	}
}
