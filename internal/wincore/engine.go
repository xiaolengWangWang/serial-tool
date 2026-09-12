package wincore

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	neturl "net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

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
	ModeHTTPClient   Mode = "HTTP 客户端"
)

type Config struct {
	Mode              Mode
	SerialName        string
	Baud              int
	DataBits          int
	StopBits          int
	Parity            string
	Protocol          string
	Role              string
	Address           string
	AutoReconnect     bool
	ReconnectInterval time.Duration
}

type Engine struct {
	sync.Mutex
	port              serial.Port
	listener          net.Listener
	clients           map[net.Conn]*trackedConnection
	udp               *net.UDPConn
	udpPeer           *net.UDPAddr
	udpPeers          map[string]*udpPeerState
	udpDialed         bool
	bridge            bool
	bridgeReplyLatest bool
	latestConnection  string
	maxConnections    int
	serialEndpoint    string
	serialWrite       sync.Mutex
	networkWrite      sync.Mutex
	store             *Store
	onData            func(string, []byte)
	onPacket          func(Packet)
	onLog             func(string)
	onClosed          func()
	mode              Mode
	httpURL           string
	httpClient        *http.Client
	vbridges          map[int]*vBridge // 后台运行的多个虚拟串口桥接
	vseq              int
	rxBytes           uint64
	txBytes           uint64
	rxCount           uint64
	txCount           uint64
	reconnects        uint64
	errCount          uint64
	state             int32
	startedAt         int64
	reconnectAddr     string
	reconnectStop     chan struct{}
	reconnectInterval time.Duration
	epoch             uint64
	idPrefix          string
	idSeq             uint64
	serialRXBytes     uint64
	serialTXBytes     uint64
	networkRXBytes    uint64
	networkTXBytes    uint64
	histMu            sync.Mutex
	favorites         map[string]string
	sendHistory       []string
}

// SetOnClosed 注册"连接被动断开"回调(远端关闭、串口拔出、监听出错等,
// 不含用户主动 Disconnect)。用于让 UI 同步回未连接状态。
func (e *Engine) SetOnClosed(fn func()) {
	e.Lock()
	e.onClosed = fn
	e.Unlock()
}

func (e *Engine) notifyClosed() {
	e.Lock()
	fn := e.onClosed
	e.Unlock()
	if fn != nil {
		fn()
	}
}

// recordEvent 把断开等事件报文持久化到数据库(source=断开),
// 此时会话尚未结束,写入的记录归属当前会话。
func (e *Engine) recordEvent(message string) {
	packet := e.newPacket("EVENT", "", "", "", "断开", "", []byte(message))
	if err := e.store.Record(packet); err != nil {
		e.emitLog("SQLite 事件写入失败: " + err.Error())
	}
}

// storeSent 把一条成功发送的报文入库(source=发送),一条报文一条记录。
func (e *Engine) storeSent(data []byte) {
	e.Lock()
	mode := e.mode
	var transport, connectionID, endpoint, leg string
	switch mode {
	case ModeSerial, ModeSerialServer:
		transport, leg, endpoint = "SERIAL", "serial", e.serialEndpoint
	case ModeTCPClient, ModeTCPServer:
		transport, leg = "TCP", "network"
		if len(e.clients) == 1 {
			for _, c := range e.clients {
				connectionID, endpoint = c.id, c.conn.RemoteAddr().String()
			}
		}
	case ModeUDPClient, ModeUDPServer:
		transport, leg = "UDP", "network"
		if e.udpPeer != nil {
			endpoint = e.udpPeer.String()
			if peer := e.udpPeers[endpoint]; peer != nil {
				connectionID = peer.id
			}
		}
	}
	e.Unlock()
	if transport == "TCP" {
		atomic.AddUint64(&e.txCount, 1)
		atomic.AddUint64(&e.txBytes, uint64(len(data)))
		return
	}
	e.storeSentPacket(e.newPacket("TX", transport, connectionID, endpoint, "发送", leg, data))
}

func (e *Engine) storeSentPacket(packet Packet) {
	atomic.AddUint64(&e.txCount, 1)
	atomic.AddUint64(&e.txBytes, uint64(len(packet.Data)))
	if err := e.emitPacket(packet); err != nil {
		e.emitLog("SQLite 发送记录写入失败: " + err.Error())
	}
}

func New(dataDir string, onData func(string, []byte), onLog func(string)) (*Engine, error) {
	store, err := OpenStore(dataDir)
	if err != nil {
		return nil, err
	}
	e := &Engine{store: store, onData: onData, onLog: onLog, vbridges: map[int]*vBridge{}, favorites: map[string]string{}, idPrefix: randomIDPrefix()}
	e.LoadFavorites()
	return e, nil
}

func ListPorts() ([]string, error) { return serial.GetPortsList() }

// LocalIP 返回本机对外通信使用的主 IPv4 地址(不实际发包),失败回退 127.0.0.1。
func LocalIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "127.0.0.1"
	}
	defer conn.Close()
	if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok {
		return addr.IP.String()
	}
	return "127.0.0.1"
}

// LocalIPs 返回本机所有非回环 IPv4 地址,主出口地址排在最前,末尾附回环 127.0.0.1。
func LocalIPs() []string {
	var ips []string
	seen := map[string]bool{}
	add := func(ip string) {
		if ip != "" && !seen[ip] {
			seen[ip] = true
			ips = append(ips, ip)
		}
	}
	add(LocalIP())
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
				if v4 := ipnet.IP.To4(); v4 != nil {
					add(v4.String())
				}
			}
		}
	}
	add("127.0.0.1")
	return ips
}

// RecentSessions 返回去重后最近使用的连接配置,用于"历史连接"选择。
func (e *Engine) RecentSessions(limit int) ([]SessionInfo, error) {
	return e.store.RecentSessions(limit)
}

func (e *Engine) DataDir() string { return e.store.Dir() }

func (e *Engine) GetSetting(key string) string       { return e.store.GetSetting(key) }
func (e *Engine) SetSetting(key, value string) error { return e.store.SetSetting(key, value) }

func normalizeHTTPURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		raw = "http://" + raw
	}
	if u, err := neturl.Parse(raw); err != nil || u.Host == "" {
		return ""
	}
	return raw
}

func (e *Engine) connectHTTP(cfg Config) error {
	url := normalizeHTTPURL(cfg.Address)
	if url == "" {
		return errors.New("请输入 URL,例如 http://39.107.191.77:8080/api/data")
	}
	jar, _ := cookiejar.New(nil)
	e.Lock()
	e.httpURL = url
	e.mode = cfg.Mode
	e.httpClient = &http.Client{
		Timeout: 10 * time.Second,
		Jar:     jar,
		// 不自动跟随重定向,直接呈现 3xx + Set-Cookie(Cookie 仍会存入 jar)
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	e.Unlock()
	if err := e.store.StartSession(string(cfg.Mode), url, ""); err != nil {
		e.emitLog("SQLite 会话写入失败: " + err.Error())
	}
	atomic.StoreInt64(&e.startedAt, time.Now().UnixNano())
	atomic.StoreInt32(&e.state, int32(StateConnected))
	e.emitLog("HTTP 就绪: " + url + "(发送框:第一行\"[方法] 路径\",可跟请求头行,空行后为 body)")
	return nil
}

func isHeaderName(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	for _, r := range s {
		if r != '-' && !(r >= 'A' && r <= 'Z') && !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// prettyJSON 对 JSON 响应体做缩进美化,非 JSON 原样返回。
func prettyJSON(contentType string, data []byte) []byte {
	t := strings.TrimSpace(string(data))
	if !strings.Contains(contentType, "json") && !strings.HasPrefix(t, "{") && !strings.HasPrefix(t, "[") {
		return data
	}
	var buf bytes.Buffer
	if json.Indent(&buf, data, "", "  ") == nil {
		return buf.Bytes()
	}
	return data
}

func isHTTPMethod(s string) bool {
	switch strings.ToUpper(s) {
	case "GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS":
		return true
	}
	return false
}

// resolveHTTPTarget 把发送框里的路径/URL 解析成完整请求地址。
func resolveHTTPTarget(base, p string) string {
	if p == "" {
		return base
	}
	if strings.HasPrefix(p, "http://") || strings.HasPrefix(p, "https://") {
		return p
	}
	u, err := neturl.Parse(base)
	if err != nil {
		return base
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	if q := strings.IndexByte(p, '?'); q >= 0 {
		u.Path, u.RawQuery = p[:q], p[q+1:]
	} else {
		u.Path, u.RawQuery = p, ""
	}
	return u.String()
}

// HTTPRequest 按发送框内容对 base URL 发起请求,复用同一 Cookie jar(登录后会话自动保持)。
// 格式:第一行 "[METHOD] [path]"(METHOD 省略则 GET);其后各行作为表单 body。
// 发送框为空则 GET base URL。请求与响应报文写入接收区并入库。
func (e *Engine) HTTPRequest(spec string) error {
	e.Lock()
	base, client := e.httpURL, e.httpClient
	e.Unlock()
	if base == "" || client == nil {
		return errors.New("尚未连接 HTTP")
	}

	method, target := "GET", base
	headers := map[string]string{}
	body := ""
	if spec = strings.TrimRight(spec, "\n"); strings.TrimSpace(spec) != "" {
		lines := strings.Split(spec, "\n")
		tokens := strings.Fields(lines[0])
		idx := 0
		if len(tokens) > 0 && isHTTPMethod(tokens[0]) {
			method, idx = strings.ToUpper(tokens[0]), 1
		}
		if len(tokens) > idx {
			target = resolveHTTPTarget(base, tokens[idx])
		}
		// 请求头行(直到空行);空行或非头部行之后是 body
		i := 1
		for ; i < len(lines); i++ {
			if strings.TrimSpace(lines[i]) == "" {
				i++
				break
			}
			colon := strings.IndexByte(lines[i], ':')
			if colon > 0 && isHeaderName(lines[i][:colon]) {
				headers[strings.TrimSpace(lines[i][:colon])] = strings.TrimSpace(lines[i][colon+1:])
			} else {
				break
			}
		}
		if i < len(lines) {
			body = strings.TrimSpace(strings.Join(lines[i:], "\n"))
		}
	}

	var reqBody io.Reader
	if body != "" {
		reqBody = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, target, reqBody)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != "" { // JSON body 自动识别,否则按表单
		if strings.HasPrefix(body, "{") || strings.HasPrefix(body, "[") {
			req.Header.Set("Content-Type", "application/json")
		} else {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	}
	for k, v := range headers { // 用户自定义头覆盖默认
		req.Header.Set(k, v)
	}

	e.emitLog(fmt.Sprintf("HTTP %s %s", method, target))
	// 请求报文入库(一条记录)
	var reqLog strings.Builder
	fmt.Fprintf(&reqLog, "%s %s\n", method, target)
	_ = req.Header.Write(&reqLog)
	if body != "" {
		reqLog.WriteString("\n")
		reqLog.WriteString(body)
	}
	e.storeSent([]byte(reqLog.String()))
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	elapsed := time.Since(start).Round(time.Millisecond)

	var b strings.Builder
	fmt.Fprintf(&b, "%s %s  (耗时 %s, %d 字节)\n", resp.Proto, resp.Status, elapsed, len(data))
	_ = resp.Header.Write(&b)
	b.WriteString("\n")
	b.Write(prettyJSON(resp.Header.Get("Content-Type"), data))
	e.receive("HTTP "+target, []byte(b.String()))
	return nil
}

func (e *Engine) Connect(cfg Config) (connectErr error) {
	e.Disconnect()
	e.Lock()
	captureKey := fmt.Sprintf("%s-%d", e.idPrefix, e.epoch)
	e.Unlock()
	e.store.SetCaptureKey(captureKey)
	atomic.StoreInt32(&e.state, int32(StateConnecting))
	defer func() {
		if connectErr != nil {
			atomic.StoreInt32(&e.state, int32(StateError))
			atomic.AddUint64(&e.errCount, 1)
		}
	}()
	if cfg.Mode == ModeHTTPClient {
		return e.connectHTTP(cfg)
	}
	usesSerial := cfg.Mode == ModeSerial || cfg.Mode == ModeSerialServer
	usesNetwork := cfg.Mode != ModeSerial
	if usesSerial {
		if cfg.Baud <= 0 || cfg.DataBits < 5 || cfg.DataBits > 8 || (cfg.StopBits != 1 && cfg.StopBits != 2) {
			return errors.New("串口参数无效")
		}
		if cfg.SerialName == "" {
			return errors.New("请选择串口")
		}
	}
	if usesNetwork && cfg.Address == "" {
		return errors.New("请输入 IP:端口")
	}

	var p serial.Port
	var listener net.Listener
	var clients []net.Conn
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
			} else {
				var client net.Conn
				client, err = net.Dial("tcp", cfg.Address)
				if err == nil {
					clients = append(clients, client)
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
	e.port, e.listener, e.clients = p, listener, make(map[net.Conn]*trackedConnection)
	e.udp, e.udpPeer, e.udpDialed = udp, peer, udpDialed
	e.udpPeers = make(map[string]*udpPeerState)
	e.bridge = cfg.Mode == ModeSerialServer
	e.serialEndpoint = cfg.SerialName
	e.latestConnection = ""
	e.mode = cfg.Mode
	epoch := e.epoch
	initialClients := make([]*trackedConnection, 0, len(clients))
	for _, client := range clients {
		initialClients = append(initialClients, e.addTCPConnection(client, epoch))
	}
	if cfg.Mode == ModeTCPClient && cfg.AutoReconnect {
		e.reconnectAddr = cfg.Address
		e.reconnectStop = make(chan struct{})
		e.reconnectInterval = cfg.ReconnectInterval
	} else {
		e.reconnectAddr = ""
		e.reconnectStop = nil
	}
	e.Unlock()
	if udp != nil && peer != nil {
		e.udpPeerFor(peer.String(), udp.LocalAddr().String())
	}
	endpoint := cfg.Address
	if cfg.Mode == ModeSerial {
		endpoint = cfg.SerialName
	}
	parameters := fmt.Sprintf("serial=%s,baud=%d,data=%d,parity=%s,stop=%d,protocol=%s,role=%s",
		cfg.SerialName, cfg.Baud, cfg.DataBits, cfg.Parity, cfg.StopBits, protocol, role)
	if err := e.store.StartSession(string(cfg.Mode), endpoint, parameters); err != nil {
		e.emitLog("SQLite 会话写入失败: " + err.Error())
	}
	atomic.StoreInt64(&e.startedAt, time.Now().UnixNano())
	atomic.StoreInt32(&e.state, int32(StateConnected))
	if p != nil {
		go e.readSerial(p, epoch)
	}
	if listener != nil {
		go e.acceptLoop(listener, epoch)
	}
	for _, client := range initialClients {
		go e.readTCP(client)
	}
	if udp != nil {
		go e.readUDP(udp, epoch)
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
	atomic.StoreInt32(&e.state, int32(StateDisconnected))
	e.Lock()
	e.epoch++
	stop := e.reconnectStop
	e.reconnectStop = nil
	e.reconnectAddr = ""
	p, listener, clients, udp := e.port, e.listener, e.clients, e.udp
	e.port, e.listener, e.clients, e.udp, e.udpPeer, e.udpPeers = nil, nil, nil, nil, nil, nil
	e.udpDialed, e.bridge = false, false
	e.serialEndpoint = ""
	e.latestConnection = ""
	e.httpURL, e.httpClient = "", nil
	e.Unlock()
	if stop != nil {
		close(stop)
	}
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
	e.removeAllVirtualSerials()
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
	e.Lock()
	mode := e.mode
	e.Unlock()
	if mode == ModeHTTPClient {
		return e.HTTPRequest(input)
	}
	data, err := ParseData(input, asHex, eol)
	if err != nil {
		return err
	}
	e.Lock()
	p, listener, udp, n := e.port, e.listener, e.udp, len(e.clients)
	e.Unlock()
	switch {
	case p != nil:
		err = e.writeSerial(data)
	case listener != nil || n > 0:
		err = e.broadcast(data, true)
	case udp != nil:
		err = e.sendUDP(data, true)
	default:
		return errors.New("尚未连接")
	}
	if err == nil {
		e.rememberSend(input)
		e.storeSent(data)
	}
	return err
}

func (e *Engine) SendNetwork(input string, asHex bool, eol string) error {
	data, err := ParseData(input, asHex, eol)
	if err != nil {
		return err
	}
	if err = e.broadcast(data, true); err != nil {
		return err
	}
	e.rememberSend(input)
	atomic.AddUint64(&e.txCount, 1)
	atomic.AddUint64(&e.txBytes, uint64(len(data)))
	return nil
}
func (e *Engine) SendUDP(input string, asHex bool, eol string) error {
	data, err := ParseData(input, asHex, eol)
	if err != nil {
		return err
	}
	e.Lock()
	peer := e.udpPeer
	e.Unlock()
	if peer == nil {
		return errors.New("暂无 UDP 对端")
	}
	if err = e.SendToUDP(peer.String(), data); err != nil {
		return err
	}
	e.rememberSend(input)
	return nil
}

func (e *Engine) acceptLoop(listener net.Listener, epoch uint64) {
	for {
		client, err := listener.Accept()
		if err != nil {
			e.Lock()
			active := e.listener == listener && e.epoch == epoch
			e.Unlock()
			if active {
				msg := "TCP 监听错误: " + err.Error()
				e.emitLog(msg)
				e.recordEvent(msg)
				e.notifyClosed()
			}
			return
		}
		e.Lock()
		if e.clients == nil || e.listener != listener || e.epoch != epoch {
			e.Unlock()
			_ = client.Close()
			return
		}
		if e.maxConnections > 0 && len(e.clients) >= e.maxConnections {
			e.Unlock()
			_ = client.Close()
			e.emitLog("TCP 客户端已拒绝(达到最大连接数): " + client.RemoteAddr().String())
			continue
		}
		connection := e.addTCPConnection(client, epoch)
		e.Unlock()
		e.emitLog("TCP 客户端已连接: " + client.RemoteAddr().String())
		go e.readTCP(connection)
	}
}

func (e *Engine) readTCP(connection *trackedConnection) {
	client := connection.conn
	defer func() {
		_ = client.Close()
		e.Lock()
		active := e.clients != nil && e.clients[client] == connection && e.epoch == connection.epoch
		if active {
			delete(e.clients, client)
			if e.latestConnection == connection.id {
				e.latestConnection = ""
			}
		}
		mode := e.mode
		reconnect := e.reconnectAddr != "" && atomic.LoadUint32(&connection.manualClose) == 0
		stop := e.reconnectStop
		e.Unlock()
		if active {
			msg := "TCP 客户端已断开: " + client.RemoteAddr().String()
			e.emitLog(msg)
			e.recordEvent(msg)
			if mode == ModeTCPClient {
				if reconnect {
					// 被动断开,自动重连(指数退避);UI 通过状态栏观察状态
					atomic.StoreInt32(&e.state, int32(StateReconnecting))
					e.emitLog("TCP 连接断开,自动重连中...")
					go e.reconnectTCP(connection.epoch, stop)
				} else {
					e.notifyClosed()
				}
			}
		}
	}()
	buf := make([]byte, 4096)
	for {
		n, err := client.Read(buf)
		if n > 0 {
			e.Lock()
			active := e.clients != nil && e.clients[client] == connection && e.epoch == connection.epoch
			bridge := active && e.bridge
			if bridge {
				e.latestConnection = connection.id
			}
			e.Unlock()
			if active {
				atomic.AddUint64(&connection.rxCount, 1)
				atomic.AddUint64(&connection.rxBytes, uint64(n))
				atomic.AddUint64(&e.networkRXBytes, uint64(n))
				e.receivePacket(connection.epoch, e.newPacket("RX", "TCP", connection.id, client.RemoteAddr().String(), "TCP "+client.RemoteAddr().String(), "network", buf[:n]))
			}
			if bridge && active {
				if writeErr := e.writeSerialAt(buf[:n], connection.epoch); writeErr != nil {
					e.emitLog("串口写入错误: " + writeErr.Error())
				} else {
					e.recordPacketAt(connection.epoch, e.newPacket("TX", "SERIAL", connection.id, e.serialAddress(), "NET→SERIAL", "serial", buf[:n]))
				}
			}
		}
		if err != nil {
			return
		}
	}
}

func (e *Engine) readUDP(conn *net.UDPConn, epoch uint64) {
	buf := make([]byte, 65535)
	for {
		n, peer, err := conn.ReadFromUDP(buf)
		if n > 0 {
			e.Lock()
			active := e.udp == conn && e.epoch == epoch
			if active {
				e.udpPeer = peer
			}
			bridge := active && e.bridge
			e.Unlock()
			if active {
				state := e.udpPeerFor(peer.String(), conn.LocalAddr().String())
				atomic.AddUint64(&state.rxCount, 1)
				atomic.AddUint64(&state.rxBytes, uint64(n))
				atomic.AddUint64(&e.networkRXBytes, uint64(n))
				e.receivePacket(epoch, e.newPacket("RX", "UDP", state.id, peer.String(), "UDP "+peer.String(), "network", buf[:n]))
			}
			if bridge && active {
				if writeErr := e.writeSerialAt(buf[:n], epoch); writeErr != nil {
					e.emitLog("串口写入错误: " + writeErr.Error())
				} else {
					state := e.udpPeerFor(peer.String(), conn.LocalAddr().String())
					e.recordPacketAt(epoch, e.newPacket("TX", "SERIAL", state.id, e.serialAddress(), "NET→SERIAL", "serial", buf[:n]))
				}
			}
		}
		if err != nil {
			e.Lock()
			active := e.udp == conn && e.epoch == epoch
			if active {
				e.udp, e.udpPeer, e.udpDialed = nil, nil, false
			}
			e.Unlock()
			if active {
				msg := "UDP 监听错误: " + err.Error()
				e.emitLog(msg)
				e.recordEvent(msg)
				e.notifyClosed()
			}
			return
		}
	}
}

func (e *Engine) readSerial(p serial.Port, epoch uint64) {
	buf := make([]byte, 4096)
	for {
		n, err := p.Read(buf)
		if n > 0 {
			e.Lock()
			active := e.port == p && e.epoch == epoch
			bridge := active && e.bridge
			e.Unlock()
			if active {
				atomic.AddUint64(&e.serialRXBytes, uint64(n))
				e.receivePacket(epoch, e.newPacket("RX", "SERIAL", "", e.serialAddress(), "串口", "serial", buf[:n]))
			}
			if bridge && active {
				if writeErr := e.forwardNetwork(buf[:n], epoch); writeErr != nil {
					e.emitLog("网络写入错误: " + writeErr.Error())
				}
			}
		}
		if err != nil {
			e.Lock()
			active := e.port == p && e.epoch == epoch
			if active {
				e.port = nil
			}
			e.Unlock()
			if active {
				msg := "串口已断开: " + err.Error()
				e.emitLog(msg)
				e.recordEvent(msg)
				e.notifyClosed()
			}
			return
		}
	}
}

func (e *Engine) receive(source string, data []byte) {
	e.Lock()
	epoch := e.epoch
	e.Unlock()
	e.receivePacket(epoch, e.newPacket("RX", "", "", "", source, "", data))
}

func (e *Engine) receivePacket(epoch uint64, packet Packet) {
	packet.epoch = epoch
	packet.SessionKey = fmt.Sprintf("%s-%d", e.idPrefix, epoch)
	e.Lock()
	if e.epoch != epoch {
		e.Unlock()
		return
	}
	onData := e.onData
	e.Unlock()
	atomic.AddUint64(&e.rxCount, 1)
	atomic.AddUint64(&e.rxBytes, uint64(len(packet.Data)))
	if err := e.emitPacket(packet); err != nil {
		e.emitLog("SQLite 写入失败: " + err.Error())
	}
	if onData != nil {
		onData(packet.Source, append([]byte(nil), packet.Data...))
	}
}

func (e *Engine) recordPacketAt(epoch uint64, packet Packet) {
	packet.epoch = epoch
	packet.SessionKey = fmt.Sprintf("%s-%d", e.idPrefix, epoch)
	e.recordPacket(packet)
}

func (e *Engine) recordPacket(packet Packet) {
	if err := e.emitPacket(packet); err != nil {
		e.emitLog("SQLite 写入失败: " + err.Error())
	}
}

func (e *Engine) emitPacket(packet Packet) error {
	e.Lock()
	active := packet.epoch == e.epoch
	e.Unlock()
	if !active {
		return nil
	}
	stored := packet
	stored.Data = append([]byte(nil), packet.Data...)
	err := e.store.Record(stored)
	e.Lock()
	fn := e.onPacket
	e.Unlock()
	if fn != nil {
		callback := packet
		callback.Data = append([]byte(nil), packet.Data...)
		fn(callback)
	}
	return err
}

func (e *Engine) serialAddress() string {
	e.Lock()
	address := e.serialEndpoint
	e.Unlock()
	return address
}

func (e *Engine) writeSerial(data []byte) error {
	e.Lock()
	epoch := e.epoch
	e.Unlock()
	return e.writeSerialAt(data, epoch)
}
func (e *Engine) writeSerialAt(data []byte, epoch uint64) error {
	e.serialWrite.Lock()
	defer e.serialWrite.Unlock()
	e.Lock()
	p, active := e.port, e.epoch == epoch
	e.Unlock()
	if p == nil || !active {
		return errors.New("串口未连接")
	}
	n, err := p.Write(data)
	if n > 0 {
		atomic.AddUint64(&e.serialTXBytes, uint64(n))
	}
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	return err
}
func (e *Engine) forwardNetwork(data []byte, epoch uint64) error {
	e.Lock()
	if e.epoch != epoch {
		e.Unlock()
		return nil
	}
	hasTCP, udp, latest, latestOnly := len(e.clients) > 0, e.udp, e.latestConnection, e.bridgeReplyLatest
	e.Unlock()
	if hasTCP {
		if latestOnly {
			c := e.connectionByID(latest)
			if c == nil || c.epoch != epoch {
				return nil
			}
			if err := e.writeTCP(c, data); err != nil {
				return err
			}
			e.recordPacketAt(epoch, e.newPacket("TX", "TCP", c.id, c.conn.RemoteAddr().String(), "SERIAL→NET", "network", data))
			return nil
		}
		return e.broadcastPackets(data, false, "SERIAL→NET", epoch)
	}
	if udp != nil {
		return e.sendUDPBridge(data, epoch)
	}
	return nil
}
func (e *Engine) broadcast(data []byte, requireClient bool) error {
	e.Lock()
	epoch := e.epoch
	e.Unlock()
	return e.broadcastPackets(data, requireClient, "发送", epoch)
}
func (e *Engine) broadcastPackets(data []byte, requireClient bool, source string, epoch uint64) error {
	e.Lock()
	clients := make([]*trackedConnection, 0, len(e.clients))
	if e.epoch == epoch {
		for _, c := range e.clients {
			clients = append(clients, c)
		}
	}
	e.Unlock()
	if requireClient && len(clients) == 0 {
		return errors.New("暂无 TCP 客户端连接")
	}
	var result error
	for _, c := range clients {
		if err := e.writeTCP(c, data); err != nil {
			result = errors.Join(result, err)
			continue
		}
		e.recordPacketAt(epoch, e.newPacket("TX", "TCP", c.id, c.conn.RemoteAddr().String(), source, "network", data))
	}
	return result
}

func (e *Engine) writeTCP(connection *trackedConnection, data []byte) error {
	if !e.isActiveConnection(connection) {
		return errors.New("TCP Connection 已断开")
	}
	connection.writeMu.Lock()
	defer connection.writeMu.Unlock()
	_ = connection.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	n, err := connection.conn.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if n > 0 {
		atomic.AddUint64(&e.networkTXBytes, uint64(n))
	}
	if err == nil {
		atomic.AddUint64(&connection.txCount, 1)
		atomic.AddUint64(&connection.txBytes, uint64(n))
	}
	return err
}

func (e *Engine) sendUDP(data []byte, requirePeer bool) error {
	e.Lock()
	conn, peer, epoch := e.udp, e.udpPeer, e.epoch
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
	return e.writeUDP(conn, epoch, peer, data)
}

func (e *Engine) sendUDPBridge(data []byte, epoch uint64) error {
	e.Lock()
	conn, peer := e.udp, e.udpPeer
	if e.epoch != epoch {
		e.Unlock()
		return nil
	}
	e.Unlock()
	if conn == nil || peer == nil {
		return nil
	}
	if err := e.writeUDP(conn, epoch, peer, data); err != nil {
		return err
	}
	state := e.udpPeerFor(peer.String(), conn.LocalAddr().String())
	e.recordPacket(e.newPacket("TX", "UDP", state.id, peer.String(), "SERIAL→NET", "network", data))
	return nil
}

func (e *Engine) writeUDP(conn *net.UDPConn, epoch uint64, peer *net.UDPAddr, data []byte) error {
	e.networkWrite.Lock()
	defer e.networkWrite.Unlock()
	e.Lock()
	active, dialed := e.udp == conn && e.epoch == epoch, e.udpDialed
	e.Unlock()
	if !active {
		return errors.New("UDP 已断开")
	}
	var n int
	var err error
	if dialed {
		if conn.RemoteAddr().String() != peer.String() {
			return errors.New("UDP 客户端只能发送到配置的目标；选择服务端模式可发送到任意对端")
		}
		n, err = conn.Write(data)
	} else {
		n, err = conn.WriteToUDP(data, peer)
	}
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if n > 0 {
		atomic.AddUint64(&e.networkTXBytes, uint64(n))
	}
	if err == nil {
		state := e.udpPeerFor(peer.String(), conn.LocalAddr().String())
		atomic.AddUint64(&state.txCount, 1)
		atomic.AddUint64(&state.txBytes, uint64(n))
	}
	return err
}

func (e *Engine) emitLog(text string) {
	e.Lock()
	fn := e.onLog
	e.Unlock()
	if fn != nil {
		fn(text)
	}
}
