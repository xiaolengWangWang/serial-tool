package wincore

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"

	"go.bug.st/serial"
)

type Mode string

const (
	ModeSerial       Mode = "串口"
	ModeTCPServer    Mode = "TCP 服务端"
	ModeTCPClient    Mode = "TCP 客户端"
	ModeUDPServer    Mode = "UDP 服务端"
	ModeUDPClient    Mode = "UDP 客户端"
	ModeSerialServer Mode = "串口服务器"
)

type Config struct {
	Mode       Mode
	SerialName string
	Baud       int
	DataBits   int
	StopBits   int
	Parity     string
	Protocol   string
	Role       string
	Address    string
}

type Engine struct {
	sync.Mutex
	port         serial.Port
	listener     net.Listener
	clients      map[net.Conn]struct{}
	udp          *net.UDPConn
	udpPeer      *net.UDPAddr
	udpDialed    bool
	bridge       bool
	serialWrite  sync.Mutex
	networkWrite sync.Mutex
	store        *Store
	onData       func(string, []byte)
	onLog        func(string)
}

func New(dataDir string, onData func(string, []byte), onLog func(string)) (*Engine, error) {
	store, err := OpenStore(dataDir)
	if err != nil {
		return nil, err
	}
	return &Engine{store: store, onData: onData, onLog: onLog}, nil
}

func ListPorts() ([]string, error) { return serial.GetPortsList() }

func (e *Engine) DataDir() string { return e.store.Dir() }

func (e *Engine) Connect(cfg Config) error {
	e.Disconnect()
	if cfg.Baud <= 0 || cfg.DataBits < 5 || cfg.DataBits > 8 || (cfg.StopBits != 1 && cfg.StopBits != 2) {
		return errors.New("串口参数无效")
	}
	usesSerial := cfg.Mode == ModeSerial || cfg.Mode == ModeSerialServer
	usesNetwork := cfg.Mode != ModeSerial
	if usesSerial && cfg.SerialName == "" {
		return errors.New("请选择串口")
	}
	if usesNetwork && cfg.Address == "" {
		return errors.New("请输入 IP:端口")
	}

	var p serial.Port
	var listener net.Listener
	var clients map[net.Conn]struct{}
	var udp *net.UDPConn
	var peer *net.UDPAddr
	var udpDialed bool
	var err error
	if usesSerial {
		p, err = serial.Open(cfg.SerialName, serialMode(cfg))
		if err != nil {
			return err
		}
	}

	protocol, role := cfg.Protocol, cfg.Role
	switch cfg.Mode {
	case ModeTCPServer:
		protocol, role = "TCP", "服务端"
	case ModeTCPClient:
		protocol, role = "TCP", "客户端"
	case ModeUDPServer:
		protocol, role = "UDP", "服务端"
	case ModeUDPClient:
		protocol, role = "UDP", "客户端"
	}
	if usesNetwork {
		if protocol == "TCP" {
			if role == "服务端" {
				listener, err = net.Listen("tcp", cfg.Address)
				clients = make(map[net.Conn]struct{})
			} else {
				var client net.Conn
				client, err = net.Dial("tcp", cfg.Address)
				if err == nil {
					clients = map[net.Conn]struct{}{client: {}}
				}
			}
		} else {
			peer, err = net.ResolveUDPAddr("udp", cfg.Address)
			if err == nil {
				if role == "服务端" {
					udp, err = net.ListenUDP("udp", peer)
					peer = nil
				} else {
					udp, err = net.DialUDP("udp", nil, peer)
					udpDialed = err == nil
				}
			}
		}
		if err != nil {
			if p != nil {
				_ = p.Close()
			}
			return err
		}
	}

	e.Lock()
	e.port, e.listener, e.clients = p, listener, clients
	e.udp, e.udpPeer, e.udpDialed = udp, peer, udpDialed
	e.bridge = cfg.Mode == ModeSerialServer
	e.Unlock()
	endpoint := cfg.Address
	if cfg.Mode == ModeSerial {
		endpoint = cfg.SerialName
	}
	parameters := fmt.Sprintf("serial=%s,baud=%d,data=%d,parity=%s,stop=%d,protocol=%s,role=%s",
		cfg.SerialName, cfg.Baud, cfg.DataBits, cfg.Parity, cfg.StopBits, protocol, role)
	if err := e.store.StartSession(string(cfg.Mode), endpoint, parameters); err != nil {
		e.emitLog("SQLite 会话写入失败: " + err.Error())
	}
	if p != nil {
		go e.readSerial(p)
	}
	if listener != nil {
		go e.acceptLoop(listener)
	}
	for client := range clients {
		go e.readTCP(client)
	}
	if udp != nil {
		go e.readUDP(udp)
	}
	e.emitLog(fmt.Sprintf("已启动 %s %s", cfg.Mode, endpoint))
	return nil
}

func serialMode(cfg Config) *serial.Mode {
	mode := &serial.Mode{BaudRate: cfg.Baud, DataBits: cfg.DataBits, StopBits: serial.OneStopBit, Parity: serial.NoParity}
	if cfg.StopBits == 2 {
		mode.StopBits = serial.TwoStopBits
	}
	switch cfg.Parity {
	case "奇校验":
		mode.Parity = serial.OddParity
	case "偶校验":
		mode.Parity = serial.EvenParity
	}
	return mode
}

func (e *Engine) Disconnect() {
	e.Lock()
	p, listener, clients, udp := e.port, e.listener, e.clients, e.udp
	e.port, e.listener, e.clients, e.udp, e.udpPeer = nil, nil, nil, nil, nil
	e.udpDialed, e.bridge = false, false
	e.Unlock()
	e.store.EndSession()
	if p != nil {
		_ = p.Close()
	}
	if listener != nil {
		_ = listener.Close()
	}
	for client := range clients {
		_ = client.Close()
	}
	if udp != nil {
		_ = udp.Close()
	}
}

func (e *Engine) Close() {
	e.Disconnect()
	e.store.Close()
}

func ParseData(input string, asHex bool, eol string) ([]byte, error) {
	if asHex {
		data, err := hex.DecodeString(strings.Join(strings.Fields(input), ""))
		if err != nil {
			return nil, fmt.Errorf("HEX 格式错误: %w", err)
		}
		return data, nil
	}
	return []byte(input + map[string]string{"LF": "\n", "CR": "\r", "CRLF": "\r\n"}[eol]), nil
}

func (e *Engine) Send(input string, asHex bool, eol string) error {
	data, err := ParseData(input, asHex, eol)
	if err != nil {
		return err
	}
	e.Lock()
	p, listener, udp := e.port, e.listener, e.udp
	e.Unlock()
	if p != nil {
		return e.writeSerial(data)
	}
	if listener != nil || e.hasTCPClients() {
		return e.broadcast(data, true)
	}
	if udp != nil {
		return e.sendUDP(data, true)
	}
	return errors.New("尚未连接")
}

func (e *Engine) hasTCPClients() bool {
	e.Lock()
	defer e.Unlock()
	return len(e.clients) > 0
}

func (e *Engine) acceptLoop(listener net.Listener) {
	for {
		client, err := listener.Accept()
		if err != nil {
			e.Lock()
			active := e.listener == listener
			e.Unlock()
			if active {
				e.emitLog("TCP 监听错误: " + err.Error())
			}
			return
		}
		e.Lock()
		if e.clients == nil {
			e.Unlock()
			_ = client.Close()
			return
		}
		e.clients[client] = struct{}{}
		e.Unlock()
		e.emitLog("TCP 客户端已连接: " + client.RemoteAddr().String())
		go e.readTCP(client)
	}
}

func (e *Engine) readTCP(client net.Conn) {
	defer func() {
		_ = client.Close()
		e.Lock()
		_, active := e.clients[client]
		delete(e.clients, client)
		e.Unlock()
		if active {
			e.emitLog("TCP 客户端已断开: " + client.RemoteAddr().String())
		}
	}()
	buf := make([]byte, 4096)
	for {
		n, err := client.Read(buf)
		if n > 0 {
			e.receive("TCP "+client.RemoteAddr().String(), buf[:n])
			e.Lock()
			bridge := e.bridge
			e.Unlock()
			if bridge {
				if writeErr := e.writeSerial(buf[:n]); writeErr != nil {
					e.emitLog("串口写入错误: " + writeErr.Error())
				}
			}
		}
		if err != nil {
			return
		}
	}
}

func (e *Engine) readUDP(conn *net.UDPConn) {
	buf := make([]byte, 65535)
	for {
		n, peer, err := conn.ReadFromUDP(buf)
		if n > 0 {
			e.Lock()
			e.udpPeer = peer
			bridge := e.bridge
			e.Unlock()
			e.receive("UDP "+peer.String(), buf[:n])
			if bridge {
				if writeErr := e.writeSerial(buf[:n]); writeErr != nil {
					e.emitLog("串口写入错误: " + writeErr.Error())
				}
			}
		}
		if err != nil {
			e.Lock()
			active := e.udp == conn
			if active {
				e.udp, e.udpPeer, e.udpDialed = nil, nil, false
			}
			e.Unlock()
			if active {
				e.emitLog("UDP 监听错误: " + err.Error())
			}
			return
		}
	}
}

func (e *Engine) readSerial(p serial.Port) {
	buf := make([]byte, 4096)
	for {
		n, err := p.Read(buf)
		if n > 0 {
			e.receive("串口", buf[:n])
			e.Lock()
			bridge := e.bridge
			e.Unlock()
			if bridge {
				if writeErr := e.forwardNetwork(buf[:n]); writeErr != nil {
					e.emitLog("网络写入错误: " + writeErr.Error())
				}
			}
		}
		if err != nil {
			e.Lock()
			active := e.port == p
			if active {
				e.port = nil
			}
			e.Unlock()
			if active {
				e.emitLog("串口已断开: " + err.Error())
			}
			return
		}
	}
}

func (e *Engine) receive(source string, data []byte) {
	copyOfData := append([]byte(nil), data...)
	if err := e.store.Received(source, copyOfData); err != nil {
		e.emitLog("SQLite 写入失败: " + err.Error())
	}
	if e.onData != nil {
		e.onData(source, copyOfData)
	}
}

func (e *Engine) writeSerial(data []byte) error {
	e.Lock()
	p := e.port
	e.Unlock()
	if p == nil {
		return errors.New("串口未连接")
	}
	e.serialWrite.Lock()
	defer e.serialWrite.Unlock()
	_, err := p.Write(data)
	return err
}

func (e *Engine) forwardNetwork(data []byte) error {
	e.Lock()
	hasTCP, udp := len(e.clients) > 0, e.udp
	e.Unlock()
	if hasTCP {
		return e.broadcast(data, false)
	}
	if udp != nil {
		return e.sendUDP(data, false)
	}
	return nil
}

func (e *Engine) broadcast(data []byte, requireClient bool) error {
	e.networkWrite.Lock()
	defer e.networkWrite.Unlock()
	e.Lock()
	clients := make([]net.Conn, 0, len(e.clients))
	for client := range e.clients {
		clients = append(clients, client)
	}
	e.Unlock()
	if len(clients) == 0 && requireClient {
		return errors.New("暂无 TCP 客户端连接")
	}
	for _, client := range clients {
		if _, err := client.Write(data); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) sendUDP(data []byte, requirePeer bool) error {
	e.networkWrite.Lock()
	defer e.networkWrite.Unlock()
	e.Lock()
	conn, peer, dialed := e.udp, e.udpPeer, e.udpDialed
	e.Unlock()
	if conn == nil {
		return errors.New("UDP 未连接")
	}
	if peer == nil {
		if requirePeer {
			return errors.New("尚未收到 UDP 数据，无法确定回复目标")
		}
		return nil
	}
	if dialed {
		_, err := conn.Write(data)
		return err
	}
	_, err := conn.WriteToUDP(data, peer)
	return err
}

func (e *Engine) emitLog(text string) {
	if e.onLog != nil {
		e.onLog(text)
	}
}
