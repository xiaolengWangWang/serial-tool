package wincore

import (
	"errors"
	"strings"
	"time"
)

// Version 是当前版本号,CLI 与两端桌面版共用(CLI 构建时可用 -ldflags "-X main.version=..." 覆盖)。
const Version = "0.6.2"

// ModeSpec 描述某工作模式的参数需求,供两端 UI 统一决定控件显隐/启用,
// 替代 Mac(app.m)与 Windows(windows/main.go)各自维护的 if/else 映射。
type ModeSpec struct {
	NeedsSerial bool // 需要串口参数(串口名 + 波特率/数据位/校验/停止位)
	NeedsNet    bool // 需要网络地址(IP:端口;HTTP 模式为 URL)
	NeedsRole   bool // 需要角色(服务端/客户端),仅串口服务器
	NeedsProto  bool // 需要协议(TCP/UDP),仅串口服务器
	IsServer    bool // 默认角色为服务端
}

// AllModes 是全部工作模式(统一 7 模式,消除两端 5/7 模式不一致)。
var AllModes = []Mode{
	ModeSerial,
	ModeTCPServer,
	ModeTCPClient,
	ModeUDPServer,
	ModeUDPClient,
	ModeSerialServer,
	ModeHTTPClient,
}

// SpecOf 返回某模式的参数需求。
func SpecOf(m Mode) ModeSpec {
	switch m {
	case ModeSerial:
		return ModeSpec{NeedsSerial: true}
	case ModeTCPServer, ModeUDPServer:
		return ModeSpec{NeedsNet: true, IsServer: true}
	case ModeTCPClient, ModeUDPClient:
		return ModeSpec{NeedsNet: true}
	case ModeSerialServer:
		return ModeSpec{NeedsSerial: true, NeedsNet: true, NeedsRole: true, NeedsProto: true, IsServer: true}
	case ModeHTTPClient:
		return ModeSpec{NeedsNet: true}
	default:
		return ModeSpec{}
	}
}

// ConnParams 是一次连接所需的扁平参数(两端 UI 收集后统一构建 Config)。
type ConnParams struct {
	Mode              Mode
	SerialName        string
	Address           string
	Baud              int
	DataBits          int
	StopBits          int
	Parity            string
	Protocol          string
	Role              string
	AutoReconnect     bool
	ReconnectInterval time.Duration
}

// BuildConfig 按模式校验参数并构建 Config(串口参数有效性、必填地址等)。
func BuildConfig(p ConnParams) (Config, error) {
	spec := SpecOf(p.Mode)
	if spec.NeedsSerial {
		if p.SerialName == "" {
			return Config{}, errors.New("请选择串口")
		}
		if p.Baud <= 0 || p.DataBits < 5 || p.DataBits > 8 || (p.StopBits != 1 && p.StopBits != 2) {
			return Config{}, errors.New("串口参数无效")
		}
	}
	if spec.NeedsNet && strings.TrimSpace(p.Address) == "" {
		if p.Mode == ModeHTTPClient {
			return Config{}, errors.New("请输入 URL")
		}
		return Config{}, errors.New("请输入 IP:端口")
	}
	return Config{
		Mode:              p.Mode,
		SerialName:        p.SerialName,
		Address:           p.Address,
		Baud:              p.Baud,
		DataBits:          p.DataBits,
		StopBits:          p.StopBits,
		Parity:            p.Parity,
		Protocol:          p.Protocol,
		Role:              p.Role,
		AutoReconnect:     p.AutoReconnect,
		ReconnectInterval: p.ReconnectInterval,
	}, nil
}
