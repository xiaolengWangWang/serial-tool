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
	onClosed     func()
	mode         Mode
	httpURL      string
	httpClient   *http.Client
	vbridges     map[int]*vBridge // 后台运行的多个虚拟串口桥接
	vseq         int
	rxBytes      uint64
	txBytes      uint64
	rxCount      uint64
	txCount      uint64
	reconnects   uint64
	errCount     uint64
	state        int32
	startedAt    int64
	reconnectAddr string
	reconnectStop chan struct{}
	histMu       sync.Mutex
	favorites    map[string]string
	sendHistory  []string
}

// SetOnClosed 注册"连接被动断开"回调(远端关闭、串口拔出、监听出错等,
// 不含用户主动 Disconnect)。用于让 UI 同步回未连接状态。
func (e *Engine) SetOnClosed(fn func()) { e.onClosed = fn }

func (e *Engine) notifyClosed() {
	if e.onClosed != nil {
		e.onClosed()
	}
}

// recordEvent 把断开等事件报文持久化到数据库(source=断开),
// 此时会话尚未结束,写入的记录归属当前会话。
func (e *Engine) recordEvent(message string) {
	if err := e.store.Received("断开", []byte(message)); err != nil {
		e.emitLog("SQLite 事件写入失败: " + err.Error())
	}
}

// storeSent 把一条成功发送的报文入库(source=发送),一条报文一条记录。
func (e *Engine) storeSent(data []byte) {
	atomic.AddUint64(&e.txCount, 1)
	atomic.AddUint64(&e.txBytes, uint64(len(data)))
	if err := e.store.Received("发送", data); err != nil {
		e.emitLog("SQLite 发送记录写入失败: " + err.Error())
	}
}

func New(dataDir string, onData func(string, []byte), onLog func(string)) (*Engine, error) {
	store, err := OpenStore(dataDir)
	if err != nil {
		return nil, err
	}
	e := &Engine{store: store, onData: onData, onLog: onLog, vbridges: map[int]*vBridge{}, favorites: map[string]string{}}
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

func (e *Engine) Connect(cfg Config) error {
	e.Disconnect()
	atomic.StoreInt32(&e.state, int32(StateConnecting))
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
	e.mode = cfg.Mode
	if cfg.Mode == ModeTCPClient {
		e.reconnectAddr = cfg.Address
		e.reconnectStop = make(chan struct{})
	} else {
		e.reconnectAddr = ""
		e.reconnectStop = nil
	}
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
	atomic.StoreInt64(&e.startedAt, time.Now().UnixNano())
	atomic.StoreInt32(&e.state, int32(StateConnected))
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
	stop := e.reconnectStop
	e.reconnectStop = nil
	e.reconnectAddr = ""
	p, listener, clients, udp := e.port, e.listener, e.clients, e.udp
	e.port, e.listener, e.clients, e.udp, e.udpPeer = nil, nil, nil, nil, nil
	e.udpDialed, e.bridge = false, false
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
	e.storeSent(data)
	return nil
}

func (e *Engine) SendUDP(input string, asHex bool, eol string) error {
	data, err := ParseData(input, asHex, eol)
	if err != nil {
		return err
	}
	if err = e.sendUDP(data, true); err != nil {
		return err
	}
	e.rememberSend(input)
	e.storeSent(data)
	return nil
}

func (e *Engine) acceptLoop(listener net.Listener) {
	for {
		client, err := listener.Accept()
		if err != nil {
			e.Lock()
			active := e.listener == listener
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
		mode := e.mode
		e.Unlock()
		if active {
			msg := "TCP 客户端已断开: " + client.RemoteAddr().String()
			e.emitLog(msg)
			e.recordEvent(msg)
			if mode == ModeTCPClient {
				if e.reconnectAddr != "" {
					// 被动断开,自动重连(指数退避);UI 通过状态栏观察状态
					atomic.StoreInt32(&e.state, int32(StateReconnecting))
					e.emitLog("TCP 连接断开,自动重连中...")
					go e.reconnectTCP()
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
				msg := "UDP 监听错误: " + err.Error()
				e.emitLog(msg)
				e.recordEvent(msg)
				e.notifyClosed()
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
	atomic.AddUint64(&e.rxCount, 1)
	atomic.AddUint64(&e.rxBytes, uint64(len(data)))
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
