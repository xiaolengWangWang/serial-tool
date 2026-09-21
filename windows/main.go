//go:build windows

package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
	"serial-tool/internal/wincore"
)

var modes = []string{"串口", "TCP", "UDP", "串口服务器", "HTTP 客户端"}

type application struct {
	loadingRecent                        bool
	peerList                             *walk.ListBox
	peerTitle, footer                    *walk.Label
	displayMode                          *walk.ComboBox
	autoScroll, showTime, clearAfterSend *walk.CheckBox
	assistant                            *assistantPanel
	connectionPane                       *walk.ScrollView
	arrangingPanes                       bool
	httpSendMode, hexBeforeHTTP          bool
	peerBaseline                         map[string]wincore.ConnectionInfo
	maxConnections                       int
	bridgeLatest                         bool
	lastStatsBytes                       uint64
	lastStatsAt                          time.Time

	mw                                    *walk.MainWindow
	mode, ports, baud, data, parity, stop *walk.ComboBox
	protocol, role, eol                   *walk.ComboBox
	sendHistory, favorites                *walk.ComboBox
	autoReconnect                         *walk.CheckBox
	reconnectInterval                     *walk.LineEdit
	netIP                                 *walk.ComboBox
	netPort, interval                     *walk.LineEdit
	serialGroup, networkGroup             *walk.GroupBox
	connectButton, timerButton            *walk.PushButton
	status, statusDot                     *walk.Label
	addressLabel, portLabel               *walk.Label
	roleLabel, protocolLabel              *walk.Label
	sendEdit, logEdit                     *walk.TextEdit
	hexSend                               *walk.CheckBox
	monitorWindow                         *walk.MainWindow
	monitorEdit                           *walk.TextEdit
	monitorPaused                         bool
	engine                                *wincore.Engine
	connected                             bool
	hexDisplay                            atomic.Bool
	timerMu                               sync.Mutex
	timerCancel                           chan struct{}
	notifyIcon                            *walk.NotifyIcon
	toolboxWindow                         *walk.MainWindow
	toolboxInput                          *walk.TextEdit
	toolboxOutput                         *walk.Label
	searchEdit                            *walk.LineEdit
	dirFilter                             *walk.ComboBox
	packetTable                           *walk.TableView
	packetModel                           *packetTableModel
	loopCount                             *walk.LineEdit
	loopButton                            *walk.PushButton
	loopMu                                sync.Mutex
	loopCancel                            chan struct{}
	statsLabel                            *walk.Label
	recentConn                            *walk.ComboBox
	recentSessions                        []wincore.SessionInfo
	timeFilter                            *walk.ComboBox
	protocolFilter                        *walk.ComboBox
	connectionFilter                      *walk.LineEdit
	sendTarget                            *walk.ComboBox
	udpTarget                             *walk.LineEdit
	targets                               []wincore.ConnectionInfo
	detailsLabel, sendPreview             *walk.Label
	selectionLabel                        *walk.Label
	analyzeSelectionButton                *walk.PushButton
	detailColumns                         *walk.Action
	autoUpdateAction                      *walk.Action
	checkingUpdate                        bool
	// 每秒刷新的上一次结果。只有内容真正变化时才写回控件：
	// 无条件 SetModel/SetText 会触发重排，而重排会把展开中的下拉列表强制收起。
	lastTargetLabels, lastPeerLabels []string
	lastFooter, lastPeerTitle        string
	lastDetails, lastStatusText      string
	connecting, sending              bool
	closed                           atomic.Bool
}

// Packet is a captured data frame shown in the packet table.
type Packet struct {
	Raw       wincore.Packet
	TS        time.Time
	Direction string // "RX" or "TX"
	Hex       string
	ASCII     string
	Length    int
}

type packetTableModel struct {
	walk.TableModelBase
	all                   []Packet
	visible               []Packet
	since                 time.Time
	transport, connection string
}

func (m *packetTableModel) RowCount() int { return len(m.visible) }

func (m *packetTableModel) Value(row, col int) interface{} {
	if row < 0 || row >= len(m.visible) {
		return ""
	}
	p := m.visible[row]
	switch col {
	case 0:
		return p.TS.Format("15:04:05.000")
	case 1:
		return p.Direction
	case 2:
		return p.Hex
	case 3:
		return p.ASCII
	case 4:
		return fmt.Sprintf("%d B", p.Length)
	case 5:
		return p.Raw.Transport
	case 6:
		return p.Raw.Endpoint
	case 7:
		return p.Raw.ConnectionID
	}
	return ""
}

func (m *packetTableModel) add(p Packet, kw, dir string) {
	m.all = append(m.all, p)
	if len(m.all) > 10000 {
		m.all = m.all[len(m.all)-8000:]
		m.refilter(kw, dir, m.since)
		return
	}
	if m.matches(p, kw, dir, m.since) {
		m.visible = append(m.visible, p)
		if len(m.visible) > 10000 {
			m.visible = m.visible[len(m.visible)-8000:]
		}
		m.PublishRowsInserted(len(m.visible)-1, len(m.visible)-1)
	}
}

func (m *packetTableModel) matches(p Packet, kw, dir string, since time.Time) bool {
	if m.transport != "" && m.transport != "全部协议" && p.Raw.Transport != m.transport {
		return false
	}
	if strings.HasPrefix(m.connection, "id:") && p.Raw.ConnectionID != strings.TrimPrefix(m.connection, "id:") {
		return false
	}
	if m.connection != "" && !strings.HasPrefix(m.connection, "id:") && !strings.Contains(strings.ToLower(p.Raw.ConnectionID+" "+p.Raw.Endpoint), strings.ToLower(m.connection)) {
		return false
	}
	if !since.IsZero() && p.TS.Before(since) {
		return false
	}
	if dir != "" && dir != dirAll && p.Direction != dir {
		return false
	}
	if kw != "" {
		kl := strings.ToLower(kw)
		if !strings.Contains(strings.ToLower(p.Hex), kl) &&
			!strings.Contains(strings.ToLower(p.ASCII+" "+p.Raw.ConnectionID+" "+p.Raw.Endpoint+" "+p.Raw.Source+" "+p.Raw.Transport), kl) {
			return false
		}
	}
	return true
}

func (m *packetTableModel) refilter(kw, dir string, since time.Time) {
	m.since = since
	m.visible = m.visible[:0]
	for _, p := range m.all {
		if m.matches(p, kw, dir, since) {
			m.visible = append(m.visible, p)
		}
	}
	m.PublishRowsReset()
}

func (m *packetTableModel) exportText() string {
	var b strings.Builder
	for _, p := range m.visible {
		b.WriteString(fmt.Sprintf("[%s %s %s %s %s] %s\r\n", p.TS.Format("2006-01-02 15:04:05.000"), p.Direction, p.Raw.Transport, p.Raw.ConnectionID, p.Raw.Endpoint, p.Hex))
	}
	return b.String()
}

func (m *packetTableModel) clear() {
	m.all = nil
	m.visible = nil
	m.PublishRowsReset()
}

func main() {
	app := new(application)
	configDir, err := os.UserConfigDir()
	if err != nil {
		walk.MsgBox(nil, "CommBox", err.Error(), walk.MsgBoxOK|walk.MsgBoxIconError)
		return
	}
	dataDir := filepath.Join(configDir, "CommBox", "data")
	app.engine, err = wincore.New(dataDir, nil, app.onLog)
	if err != nil {
		walk.MsgBox(nil, "SQLite 初始化失败", err.Error(), walk.MsgBoxOK|walk.MsgBoxIconError)
		return
	}
	app.engine.SetOnClosed(app.onClosed)
	app.engine.SetOnPacket(app.onPacket)
	defer app.engine.Close()
	app.packetModel = new(packetTableModel)
	if err = app.createWindow(); err != nil {
		walk.MsgBox(nil, "界面初始化失败", err.Error(), walk.MsgBoxOK|walk.MsgBoxIconError)
		return
	}
	if icon := uiIcon("app"); icon != nil {
		app.mw.SetIcon(icon)
	}
	if err := app.setupTray(); err != nil {
		app.appendLog("托盘图标初始化失败: " + err.Error())
	} else {
		defer app.notifyIcon.Dispose()
	}
	app.refreshPorts()
	app.updateMode()
	app.hexDisplay.Store(true)
	app.refreshSendHistory()
	app.refreshFavorites()
	app.refreshRecentConn()
	app.appendLog("SQLite 数据目录: " + app.engine.DataDir())
	go app.statsLoop()
	go app.autoCheckUpdate()
	app.mw.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		if !*canceled {
			app.assistant.stop()
			app.closed.Store(true)
			app.stopTimer(false)
			app.stopLoop()
		}
	})
	app.restoreMainWindow()
	app.mw.Run()
	app.closed.Store(true)
	app.stopTimer(false)
	app.stopLoop()
}

func (a *application) refreshPorts() {
	if a.ports == nil {
		return
	}
	ports, err := wincore.ListPorts()
	if err != nil {
		a.showError(err)
		return
	}
	current := a.ports.Text()
	_ = a.ports.SetModel(ports)
	for i, port := range ports {
		if strings.EqualFold(current, port) {
			_ = a.ports.SetCurrentIndex(i)
			return
		}
	}
	if len(ports) > 0 {
		_ = a.ports.SetCurrentIndex(0)
	} else {
		_ = a.ports.SetText(current)
	}
}

func (a *application) updateMode() {
	if a.mode == nil || a.serialGroup == nil || a.role == nil || a.connectButton == nil || a.networkGroup == nil {
		return
	}
	spec := wincore.SpecOf(a.uiMode())
	// 按模式只显示需要的参数组(隐藏不相关的)
	a.serialGroup.SetVisible(spec.NeedsSerial)
	a.networkGroup.SetVisible(spec.NeedsNet)
	a.serialGroup.SetEnabled(!a.connected)
	a.networkGroup.SetEnabled(!a.connected)
	// 网络标签随模式变化
	isHTTP := a.mode.Text() == "HTTP 客户端"
	if a.addressLabel != nil {
		if isHTTP {
			a.addressLabel.SetText("URL")
		} else if a.isServer() {
			a.addressLabel.SetText("监听 IP")
		} else {
			a.addressLabel.SetText("服务器 IP")
		}
	}
	if a.portLabel != nil {
		a.portLabel.SetVisible(!isHTTP)
	}
	if a.netPort != nil {
		a.netPort.SetVisible(!isHTTP)
	}
	// 协议:TCP/UDP 模式由模式决定并禁用,仅串口服务器可改;角色:TCP/UDP/串口服务器可改
	net := a.mode.Text() == "TCP" || a.mode.Text() == "UDP"
	a.protocol.SetEnabled(spec.NeedsProto && !a.connected)
	a.role.SetEnabled((spec.NeedsRole || net) && !a.connected)
	if net {
		idx := 0
		if a.mode.Text() == "UDP" {
			idx = 1
		}
		_ = a.protocol.SetCurrentIndex(idx)
	}
	// 标签与控件成对显隐：网络参数用两列栅格,只隐藏控件会在行内留下空标签。
	showRole := a.mode.Text() == "TCP" || a.mode.Text() == "UDP" || a.mode.Text() == "串口服务器"
	showProto := a.mode.Text() == "串口服务器"
	a.role.SetVisible(showRole)
	a.protocol.SetVisible(showProto)
	if a.roleLabel != nil {
		a.roleLabel.SetVisible(showRole)
	}
	if a.protocolLabel != nil {
		a.protocolLabel.SetVisible(showProto)
	}
	if a.udpTarget != nil {
		a.udpTarget.SetVisible(a.mode.Text() == "UDP")
	}
	if a.hexSend != nil && a.eol != nil {
		if isHTTP && !a.httpSendMode {
			a.hexBeforeHTTP = a.hexSend.Checked()
			a.hexSend.SetChecked(false)
			a.setSendFeedback("HTTP 请求按文本发送，例如 GET /health；请求头与正文用空行分隔", false)
		} else if !isHTTP && a.httpSendMode {
			a.hexSend.SetChecked(a.hexBeforeHTTP)
			a.setSendFeedback("输入报文后可先验证，按 F5 发送", false)
		}
		a.httpSendMode = isHTTP
		a.hexSend.SetEnabled(!isHTTP)
		a.eol.SetEnabled(!isHTTP)
	}
	if !a.connected {
		text := map[bool]string{true: "启动监听", false: "连接"}[a.isServer()]
		if a.uiMode() == wincore.ModeSerial {
			text = "打开串口"
		} else if isHTTP {
			text = "准备 HTTP"
		}
		a.connectButton.SetText(text)
	}
}

func (a *application) updateAddressDefault() {
	mode := a.mode.Text()
	if a.netIP == nil || a.mode == nil || a.connected || mode == "串口" || mode == "HTTP 客户端" {
		return
	}
	if a.isServer() {
		_ = a.netIP.SetText("")
	} else {
		_ = a.netIP.SetText("127.0.0.1")
	}
	_ = a.netPort.SetText("9000")
}

func (a *application) isServer() bool {
	mode := a.mode.Text()
	if mode == "TCP" || mode == "UDP" || mode == "串口服务器" {
		return a.role.Text() == "服务端"
	}
	return false
}

// uiMode 把 UI 的「5 模式 + 角色」映射到引擎的 Mode 枚举。
func (a *application) uiMode() wincore.Mode {
	switch a.mode.Text() {
	case "TCP":
		if a.isServer() {
			return wincore.ModeTCPServer
		}
		return wincore.ModeTCPClient
	case "UDP":
		if a.isServer() {
			return wincore.ModeUDPServer
		}
		return wincore.ModeUDPClient
	default:
		return wincore.Mode(a.mode.Text())
	}
}

func (a *application) config() (wincore.Config, error) {
	mode := a.uiMode()
	baud, dataBits, stopBits := 115200, 8, 1
	if wincore.SpecOf(mode).NeedsSerial {
		var err error
		if baud, err = strconv.Atoi(a.baud.Text()); err != nil {
			return wincore.Config{}, fmt.Errorf("波特率无效")
		}
		if dataBits, err = strconv.Atoi(a.data.Text()); err != nil {
			return wincore.Config{}, fmt.Errorf("数据位无效")
		}
		if stopBits, err = strconv.Atoi(a.stop.Text()); err != nil {
			return wincore.Config{}, fmt.Errorf("停止位无效")
		}
	}
	ip := strings.TrimSpace(a.netIP.Text())
	port := strings.TrimSpace(a.netPort.Text())
	var address string
	switch {
	case mode == wincore.ModeHTTPClient:
		address = ip // HTTP 的端口属于 URL，不能拼接隐藏的 TCP/UDP 端口。
	case port == "":
		address = ip // HTTP 模式：URL 直接填在 IP 栏
	case ip == "":
		address = ":" + port // 服务端：监听所有接口
	default:
		address = net.JoinHostPort(strings.Trim(ip, "[]"), port)
	}
	autoReconnect := a.autoReconnect.Checked()
	interval := 2
	if v, err := strconv.Atoi(a.reconnectInterval.Text()); err == nil && v > 0 {
		interval = v
	}
	return wincore.BuildConfig(wincore.ConnParams{
		Mode: mode, SerialName: strings.TrimSpace(a.ports.Text()), Address: address,
		Baud: baud, DataBits: dataBits, StopBits: stopBits, Parity: a.parity.Text(),
		Protocol: a.protocol.Text(), Role: a.role.Text(),
		AutoReconnect: autoReconnect, ReconnectInterval: time.Duration(interval) * time.Second,
	})
}

func (a *application) toggleConnection() {
	if a.connecting {
		return
	}
	if a.connected {
		a.stopTimer(true)
		a.stopLoop()
		a.engine.Disconnect()
		a.connected = false
		a.mode.SetEnabled(true)
		a.setConnStatus(colorGray, "未连接")
		a.updateMode()
		a.appendLog("已停止")
		return
	}
	cfg, err := a.config()
	if err != nil {
		a.showError(err)
		return
	}
	a.connecting = true
	a.mode.SetEnabled(false)
	a.serialGroup.SetEnabled(false)
	a.networkGroup.SetEnabled(false)
	a.connectButton.SetEnabled(false)
	a.setConnStatus(colorYellow, "正在连接...")
	go func() {
		err := a.engine.Connect(cfg)
		if a.closed.Load() {
			a.engine.Disconnect()
			return
		}
		a.mw.Synchronize(func() {
			if a.closed.Load() {
				return
			}
			a.connecting = false
			a.connectButton.SetEnabled(true)
			if err != nil {
				a.updateMode()
				a.mode.SetEnabled(true)
				a.setConnStatus(colorRed, "连接失败")
				a.showError(err)
				return
			}
			a.connected = true
			a.mode.SetEnabled(false)
			a.serialGroup.SetEnabled(false)
			a.networkGroup.SetEnabled(false)
			a.updateMode()
			a.connectButton.SetText(map[bool]string{true: "停止监听", false: "断开连接"}[a.isServer()])
			if cfg.Mode == wincore.ModeSerial {
				a.connectButton.SetText("关闭串口")
			} else if cfg.Mode == wincore.ModeHTTPClient {
				a.connectButton.SetText("结束 HTTP")
			}
			if a.isServer() {
				a.setConnStatus(colorBlue, "监听中")
			} else if cfg.Mode == wincore.ModeHTTPClient || cfg.Mode == wincore.ModeUDPClient {
				a.setConnStatus(colorGreen, "就绪")
			} else {
				a.setConnStatus(colorGreen, "已连接")
			}
			a.updateStatus(a.engine.Stats())
			a.refreshRecentConn()
		})
	}()
}

func (a *application) sendOnce(fromTimer bool) {
	if a.sending {
		return
	}
	if !a.connected {
		a.setSendFeedback("请先连接或启动监听，再发送数据", true)
		return
	}
	input, asHex, eol := a.sendEdit.Text(), a.hexSend.Checked(), a.eol.Text()
	id, address := a.selectedSendTarget()
	a.sending = true
	a.setSendFeedback("正在发送…", false)
	go func() {
		err := a.sendCaptured(input, asHex, eol, id, address)
		if a.closed.Load() {
			return
		}
		a.mw.Synchronize(func() {
			a.sending = false
			if err != nil {
				if fromTimer {
					a.stopTimer(true)
				}
				a.setSendFeedback("发送失败："+err.Error(), true)
				return
			}
			a.setSendFeedback("发送完成 · "+time.Now().Format("15:04:05"), false)
			a.refreshSendHistory()
			if a.clearAfterSend != nil && a.clearAfterSend.Checked() && a.sendEdit.Text() == input {
				a.sendEdit.SetText("")
			}
		})
	}()
}
func (a *application) toggleTimer() {
	a.timerMu.Lock()
	running := a.timerCancel != nil
	a.timerMu.Unlock()
	if running {
		a.stopTimer(true)
		return
	}
	if !a.connected {
		a.showError(fmt.Errorf("请先连接或开始监听"))
		return
	}
	milliseconds, err := strconv.Atoi(a.interval.Text())
	if err != nil || milliseconds < 10 {
		a.showError(fmt.Errorf("定时间隔不能小于 10 ms"))
		return
	}
	input, asHex, eol := a.sendEdit.Text(), a.hexSend.Checked(), a.eol.Text()
	id, address := a.selectedSendTarget()
	if _, err := wincore.ParseData(input, asHex, eol); err != nil {
		a.showError(err)
		return
	}
	if input == "" && (asHex || eol == "无") {
		a.showError(fmt.Errorf("请输入要发送的数据"))
		return
	}
	cancel := make(chan struct{})
	a.timerMu.Lock()
	a.timerCancel = cancel
	a.timerMu.Unlock()
	a.timerButton.SetText("停止定时")
	a.interval.SetEnabled(false)
	a.appendLog(fmt.Sprintf("已开始定时发送: %d ms", milliseconds))
	go func() {
		ticker := time.NewTicker(time.Duration(milliseconds) * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-cancel:
				return
			case <-ticker.C:
				if err := a.sendCaptured(input, asHex, eol, id, address); err != nil {
					a.mw.Synchronize(func() {
						a.stopTimer(true)
						a.showError(fmt.Errorf("定时发送已停止: %w", err))
					})
					return
				}
			}
		}
	}()
}

func (a *application) stopTimer(logIt bool) {
	a.timerMu.Lock()
	if a.timerCancel == nil {
		a.timerMu.Unlock()
		return
	}
	close(a.timerCancel)
	a.timerCancel = nil
	a.timerMu.Unlock()
	if a.timerButton != nil {
		a.timerButton.SetText("开始定时")
		a.interval.SetEnabled(true)
	}
	if logIt {
		a.appendLog("已停止定时发送")
	}
}

func (a *application) stopLoop() {
	a.loopMu.Lock()
	if a.loopCancel != nil {
		close(a.loopCancel)
		a.loopCancel = nil
	}
	a.loopMu.Unlock()
	if a.loopButton != nil && !a.loopButton.IsDisposed() {
		a.loopButton.SetText("循环发送")
	}
}

func (a *application) toggleLoopSend() {
	a.loopMu.Lock()
	running := a.loopCancel != nil
	a.loopMu.Unlock()
	if running {
		a.stopLoop()
		return
	}
	if !a.connected {
		a.showError(fmt.Errorf("请先连接"))
		return
	}
	n, err := strconv.Atoi(strings.TrimSpace(a.loopCount.Text()))
	if err != nil || n < 0 {
		a.showError(fmt.Errorf("循环次数应为非负整数"))
		return
	}
	ms, err := strconv.Atoi(strings.TrimSpace(a.interval.Text()))
	if err != nil || ms < 10 {
		a.showError(fmt.Errorf("发送间隔不能小于 10 ms"))
		return
	}
	input, asHex, eol := a.sendEdit.Text(), a.hexSend.Checked(), a.eol.Text()
	id, address := a.selectedSendTarget()
	if _, err := wincore.ParseData(input, asHex, eol); err != nil {
		a.showError(err)
		return
	}
	cancel := make(chan struct{})
	a.loopMu.Lock()
	a.loopCancel = cancel
	a.loopMu.Unlock()
	a.loopButton.SetText("停止循环")
	go func() {
		var sendErr error
		defer func() {
			if !a.closed.Load() {
				a.mw.Synchronize(func() {
					a.loopMu.Lock()
					current := a.loopCancel == cancel
					if current {
						a.loopCancel = nil
					}
					a.loopMu.Unlock()
					if current {
						a.loopButton.SetText("循环发送")
						if sendErr != nil {
							a.showError(sendErr)
						} else {
							a.appendLog("循环发送完成")
						}
					}
				})
			}
		}()
		for count := 0; n == 0 || count < n; count++ {
			select {
			case <-cancel:
				return
			default:
			}
			if sendErr = a.sendCaptured(input, asHex, eol, id, address); sendErr != nil {
				return
			}
			select {
			case <-cancel:
				return
			case <-time.After(time.Duration(ms) * time.Millisecond):
			}
		}
	}()
}
func packetFromCore(raw wincore.Packet) Packet {
	raw.Data = append([]byte(nil), raw.Data...)
	var ascii strings.Builder
	for _, b := range raw.Data {
		if b >= 32 && b < 127 {
			ascii.WriteByte(b)
		} else {
			ascii.WriteByte('.')
		}
	}
	return Packet{Raw: raw, TS: raw.Timestamp, Direction: raw.Direction, Hex: fmt.Sprintf("% X", raw.Data), ASCII: ascii.String(), Length: len(raw.Data)}
}

func (a *application) onPacket(raw wincore.Packet) {
	if a.mw == nil || a.closed.Load() {
		return
	}
	p := packetFromCore(raw)
	a.mw.Synchronize(func() {
		if a.closed.Load() || a.mw.IsDisposed() {
			return
		}
		kw, dir := "", dirAll
		if a.searchEdit != nil {
			kw = a.searchEdit.Text()
		}
		if a.dirFilter != nil {
			dir = a.dirFilter.Text()
		}
		a.packetModel.add(p, kw, dir)
		if a.packetTable != nil && a.autoScroll.Checked() && a.packetModel.RowCount() > 0 {
			a.packetTable.EnsureItemVisible(a.packetModel.RowCount() - 1)
		}
		// 统计文字由 statsLoop 每秒刷新，避免高速收发时持续重排而延迟按钮点击。
		if a.monitorEdit != nil && !a.monitorEdit.IsDisposed() {
			a.appendDisplay(a.monitorEdit, fmt.Sprintf("[%s %s %s %s] %s\r\n", p.TS.Format("15:04:05.000"), p.Direction, p.Raw.Transport, p.Raw.Endpoint, p.Hex))
			if !a.monitorPaused {
				a.monitorEdit.ScrollToCaret()
			}
		}
	})
}

// onClosed 在被动断开(远端关闭等)时把界面同步回未连接状态。
func (a *application) onClosed() {
	if a.mw == nil || a.closed.Load() {
		return
	}
	a.mw.Synchronize(func() {
		if a.closed.Load() {
			return
		}
		if !a.connected {
			return
		}
		a.stopTimer(false)
		a.stopLoop()
		a.engine.Disconnect()
		a.connected = false
		a.mode.SetEnabled(true)
		a.setConnStatus(colorGray, "未连接")
		a.updateMode()
		a.appendLog("连接已断开")
	})
}

func (a *application) onLog(text string) { a.appendLog(text) }

func (a *application) appendLog(text string) {
	if a.mw == nil || a.closed.Load() {
		return
	}
	a.mw.Synchronize(func() {
		if a.closed.Load() {
			return
		}
		a.appendDisplay(a.logEdit, "\r\n["+text+"]\r\n")
	})
}

func (a *application) appendDisplay(edit *walk.TextEdit, text string) {
	if edit == nil || edit.IsDisposed() {
		return
	}
	if edit.TextLength()+len(text) > 4500000 {
		_ = edit.SetText("[显示缓存已清理，完整数据仍保存在 SQLite]\r\n")
	}
	edit.AppendText(text)
}

func (a *application) sinceTime() time.Time {
	if a.timeFilter == nil {
		return time.Time{}
	}
	switch a.timeFilter.Text() {
	case "1分钟":
		return time.Now().Add(-time.Minute)
	case "5分钟":
		return time.Now().Add(-5 * time.Minute)
	case "30分钟":
		return time.Now().Add(-30 * time.Minute)
	}
	return time.Time{}
}

func (a *application) applyFilter() {
	if a.packetModel == nil || a.searchEdit == nil {
		return
	}
	if a.protocolFilter != nil {
		a.packetModel.transport = a.protocolFilter.Text()
	}
	if a.connectionFilter != nil {
		a.packetModel.connection = strings.TrimSpace(a.connectionFilter.Text())
	}
	kw := strings.TrimSpace(a.searchEdit.Text())
	dir := dirAll
	if a.dirFilter != nil {
		dir = a.dirFilter.Text()
	}
	a.packetModel.refilter(kw, dir, a.sinceTime())
	a.updatePacketStats()
	a.updateSelectionLabel()
}

func (a *application) clearFilter() {
	a.protocolFilter.SetCurrentIndex(0)
	a.connectionFilter.SetText("")
	_ = a.searchEdit.SetText("")
	if a.dirFilter != nil {
		_ = a.dirFilter.SetCurrentIndex(0)
	}
	if a.timeFilter != nil {
		_ = a.timeFilter.SetCurrentIndex(0)
	}
	if a.packetModel != nil {
		a.packetModel.refilter("", dirAll, time.Time{})
	}
	a.updatePacketStats()
	a.updateSelectionLabel()
}

func (a *application) openMonitor() {
	if a.monitorWindow == nil {
		var pause *walk.PushButton
		err := (MainWindow{
			AssignTo: &a.monitorWindow, Title: "实时数据监控", MinSize: Size{Width: 700, Height: 460}, Size: Size{Width: 900, Height: 620}, Font: Font{Family: fontUI, PointSize: sizeBody}, Layout: VBox{Alignment: AlignHNearVNear, Margins: Margins{Left: 10, Top: 8, Right: 10, Bottom: 8}, Spacing: 8},
			Children: []Widget{
				Composite{Layout: HBox{Alignment: AlignHNearVCenter}, Children: []Widget{
					Label{Text: "仅显示实时接收数据"}, HSpacer{},
					PushButton{Text: "数据库位置", OnClicked: a.openDataDir},
					PushButton{AssignTo: &pause, Text: "暂停滚动", OnClicked: func() {
						a.monitorPaused = !a.monitorPaused
						pause.SetText(map[bool]string{true: "继续滚动", false: "暂停滚动"}[a.monitorPaused])
					}},
					PushButton{Text: "清空", OnClicked: func() { _ = a.monitorEdit.SetText("") }},
					PushButton{Text: "导出", OnClicked: func() { a.exportText(a.monitorEdit.Text(), "monitor-data", a.monitorWindow) }},
				}},
				TextEdit{AssignTo: &a.monitorEdit, ReadOnly: true, VScroll: true, HScroll: true, MaxLength: 5000000, Font: Font{Family: fontMono, PointSize: sizeMono}},
			},
		}).Create()
		if err != nil {
			a.showError(err)
			return
		}
		if icon := uiIcon("app"); icon != nil {
			a.monitorWindow.SetIcon(icon)
		}
		a.monitorWindow.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
			*canceled = true
			a.monitorWindow.Hide()
		})
	}
	a.monitorWindow.Show()
}

// 字体阶梯：正文与标题只差 1pt,避免分区标题过分抢眼;HEX 一律同字号,
// 便于把数据表里的报文和发送框内容直接对照。
const (
	fontUI    = "Microsoft YaHei UI"
	fontMono  = "Consolas"
	sizeBody  = 10
	sizeTitle = 12
	sizeMono  = 10
)

// 分区标题(左栏「连接配置」、中栏「数据监控」、AI 面板)统一走这一个字体,
// 避免同级标题在三处各写一遍、改一处漏两处。
var fontSection = Font{Family: fontUI, PointSize: sizeTitle, Bold: true}

// 行高基线：输入类控件 26px、按钮 28px。不给下限时 walk 按各控件的理想高度排布,
// 同一行里 LineEdit 会比 ComboBox 矮一截,中文字形上下也几乎贴边。
const (
	rowH = 28
	btnH = 30
)

// stretchFill 给一行里唯一该被拉伸的控件。walk 把富余宽度按 权重/组内权重和 分配,
// 定了宽度上限的控件拿不走自己那一份,剩下的会变成控件之间的空隙(标签与下拉框
// 中间空出一大段就是这么来的)。权重拉到 100 相当于让该控件吃掉整行富余。
// 可编辑下拉框、单行/多行输入框在 walk 里本就是 greedy,会先吃掉富余,无需再设。
const stretchFill = 100

// dirAll 同时是方向筛选下拉框的首项文案和“不过滤”的判定值,
// 两者必须一致,否则筛选会把每一条报文都排除掉。
const dirAll = "全部方向"

// 状态色降饱和,蓝色只保留一种作主色,避免此前深蓝/灰蓝/亮蓝三种并存。
var (
	colorCanvas = walk.RGB(243, 243, 243)
	colorPanel  = walk.RGB(249, 249, 249)
	colorGray   = walk.RGB(140, 140, 140)
	colorGreen  = walk.RGB(34, 140, 58)
	colorYellow = walk.RGB(186, 132, 8)
	colorRed    = walk.RGB(190, 48, 48)
	colorBlue   = walk.RGB(28, 78, 140)
	colorMuted  = walk.RGB(96, 108, 120)
)

// setConnStatus 同时更新灯颜色与文字。
func (a *application) setConnStatus(color walk.Color, text string) {
	if text == a.lastStatusText {
		return
	}
	a.lastStatusText = text
	if a.statusDot != nil {
		a.statusDot.SetTextColor(color)
	}
	if a.status != nil {
		_ = a.status.SetText(text)
	}
}

// statsLoop 每秒刷新一次状态栏(连接状态 + RX/TX/运行时间/重连/错误)。
func (a *application) statsLoop() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for range ticker.C {
		if a.closed.Load() {
			return
		}
		st := a.engine.Stats()
		a.mw.Synchronize(func() { a.updateStatus(st) })
	}
}

// updateStatus 根据统计快照刷新状态栏文本与灯色。
func (a *application) updateStatus(st wincore.Stats) {
	if a.closed.Load() || a.status == nil || a.comboDropDownOpen() {
		return
	}
	a.refreshConnections()
	a.updatePacketStats()
	a.showPeerDetails()
	labels := map[wincore.ConnState]string{wincore.StateDisconnected: "未连接", wincore.StateConnecting: "连接中", wincore.StateConnected: "已连接", wincore.StateReconnecting: "重连中", wincore.StateError: "错误"}
	label := labels[st.State]
	color := colorGray
	if st.State == wincore.StateConnected {
		color = colorGreen
		if st.Listening {
			label = fmt.Sprintf("监听中 · %d 客户端", st.PeerCount)
		} else if a.uiMode() == wincore.ModeHTTPClient || a.uiMode() == wincore.ModeUDPClient {
			label = "就绪"
		}
	}
	if st.State == wincore.StateConnecting || st.State == wincore.StateReconnecting {
		color = colorYellow
	}
	if st.State == wincore.StateError {
		color = colorRed
	}
	if st.State == wincore.StateConnected && st.SerialNote != "" {
		label += " · 免驱动"
	}
	a.status.SetToolTipText(st.SerialNote)
	a.setConnStatus(color, label)
	now := time.Now()
	total := st.RXBytes + st.TXBytes
	rate := uint64(0)
	if !a.lastStatsAt.IsZero() && total >= a.lastStatsBytes {
		rate = uint64(float64(total-a.lastStatsBytes) / now.Sub(a.lastStatsAt).Seconds())
	}
	a.lastStatsAt = now
	a.lastStatsBytes = total
	elapsed := "00:00:00"
	if st.StartedAt.Unix() > 0 && a.connected {
		elapsed = wincore.FormatDuration(now.Sub(st.StartedAt))
	}
	address := st.Endpoint
	if len(a.targets) > 0 && a.targets[0].Active {
		address = "本地 " + a.targets[0].LocalAddress + " → " + a.targets[0].RemoteAddress
	}
	// 未连接时 Endpoint 为空,直接拼接会在状态栏留下"|  |"这样的空档位。
	parts := []string{fmt.Sprintf("%s %s", a.uiMode(), label)}
	if address != "" {
		parts = append(parts, address)
	}
	if st.State == wincore.StateConnected && st.SerialNote != "" {
		parts = append(parts, "VirtualCOM 字节流（串口参数不生效）")
	}
	parts = append(parts,
		fmt.Sprintf("RX %s  TX %s", wincore.FormatBytes(st.RXBytes), wincore.FormatBytes(st.TXBytes)),
		wincore.FormatBytes(rate)+"/s", elapsed)
	if footer := strings.Join(parts, "  |  "); footer != a.lastFooter {
		a.lastFooter = footer
		a.footer.SetText(footer)
	}
	if a.timeFilter.CurrentIndex() > 0 {
		a.applyFilter()
	}
}

func (a *application) refreshSendHistory() {
	if a.sendHistory == nil {
		return
	}
	cur := a.sendHistory.Text()
	_ = a.sendHistory.SetModel(a.engine.RecentSends())
	if cur != "" {
		_ = a.sendHistory.SetText(cur)
	}
}

func (a *application) refreshFavorites() {
	if a.favorites == nil {
		return
	}
	cur := a.favorites.Text()
	_ = a.favorites.SetModel(a.engine.FavoriteNames())
	if cur != "" {
		_ = a.favorites.SetText(cur)
	}
}

func (a *application) refreshRecentConn() {
	if a.recentConn == nil {
		return
	}
	sessions, _ := a.engine.RecentSessions(5)
	a.recentSessions = sessions
	items := make([]string, len(sessions))
	for i, s := range sessions {
		ep := s.Endpoint
		if len(ep) > 24 {
			ep = ep[:21] + "..."
		}
		items[i] = fmt.Sprintf("%s %s", s.Mode, ep)
	}
	a.loadingRecent = true
	_ = a.recentConn.SetModel(items)
	_ = a.recentConn.SetCurrentIndex(-1)
	a.loadingRecent = false
}

func parseKV(s string) map[string]string {
	m := make(map[string]string)
	for _, pair := range strings.Split(s, ",") {
		if i := strings.IndexByte(pair, '='); i >= 0 {
			m[pair[:i]] = pair[i+1:]
		}
	}
	return m
}

func setIPPort(endpoint string, ipEdit interface{ SetText(string) error }, portEdit *walk.LineEdit) {
	if host, port, err := net.SplitHostPort(endpoint); err == nil {
		_ = ipEdit.SetText(host)
		_ = portEdit.SetText(port)
	} else {
		_ = ipEdit.SetText(endpoint)
	}
}

func (a *application) onRecentConnSelected() {
	if a.connected || a.connecting || a.loadingRecent || a.recentConn == nil {
		return
	}
	idx := a.recentConn.CurrentIndex()
	if idx < 0 || idx >= len(a.recentSessions) {
		return
	}
	s := a.recentSessions[idx]
	p := parseKV(s.Parameters)
	if p["backend"] == "VirtualCOM" {
		// Byte-stream sessions have no effective serial settings. Restore valid
		// form defaults so their absence cannot block reopening the port.
		p["baud"], p["data"], p["parity"], p["stop"] = "115200", "8", "无校验", "1"
	}
	switch wincore.Mode(s.Mode) {
	case wincore.ModeSerial:
		_ = a.mode.SetCurrentIndex(0)
		_ = a.ports.SetText(p["serial"])
		_ = a.baud.SetText(p["baud"])
		_ = a.data.SetText(p["data"])
		_ = a.parity.SetText(p["parity"])
		_ = a.stop.SetText(p["stop"])
	case wincore.ModeTCPServer:
		_ = a.mode.SetCurrentIndex(1)
		_ = a.role.SetCurrentIndex(0)
		setIPPort(s.Endpoint, a.netIP, a.netPort)
	case wincore.ModeTCPClient:
		_ = a.mode.SetCurrentIndex(1)
		_ = a.role.SetCurrentIndex(1)
		setIPPort(s.Endpoint, a.netIP, a.netPort)
	case wincore.ModeUDPServer:
		_ = a.mode.SetCurrentIndex(2)
		_ = a.role.SetCurrentIndex(0)
		setIPPort(s.Endpoint, a.netIP, a.netPort)
	case wincore.ModeUDPClient:
		_ = a.mode.SetCurrentIndex(2)
		_ = a.role.SetCurrentIndex(1)
		setIPPort(s.Endpoint, a.netIP, a.netPort)
	case wincore.ModeSerialServer:
		_ = a.mode.SetCurrentIndex(3)
		_ = a.ports.SetText(p["serial"])
		_ = a.protocol.SetText(p["protocol"])
		_ = a.role.SetText(p["role"])
		setIPPort(s.Endpoint, a.netIP, a.netPort)
	case wincore.ModeHTTPClient:
		_ = a.mode.SetCurrentIndex(4)
		_ = a.netIP.SetText(s.Endpoint)
	}
	a.updateMode()
}

func (a *application) updatePacketStats() {
	if a.statsLabel == nil || a.packetModel == nil || a.comboDropDownOpen() {
		return
	}
	var rxCnt, txCnt, rxB, txB int
	for _, p := range a.packetModel.all {
		if p.Direction == "RX" {
			rxCnt++
			rxB += p.Length
		} else {
			txCnt++
			txB += p.Length
		}
	}
	text := fmt.Sprintf("· 共 %d 条 · RX %d/%s · TX %d/%s",
		rxCnt+txCnt,
		rxCnt, wincore.FormatBytes(uint64(rxB)),
		txCnt, wincore.FormatBytes(uint64(txB)))
	if a.statsLabel.Text() != text {
		_ = a.statsLabel.SetText(text)
	}
}

func (a *application) onSendHistorySelected() {
	if txt := a.sendHistory.Text(); txt != "" {
		_ = a.sendEdit.SetText(txt)
	}
}

func (a *application) onFavoriteSelected() {
	if name := a.favorites.Text(); name != "" {
		if v := a.engine.Favorite(name); v != "" {
			_ = a.sendEdit.SetText(v)
		}
	}
}

func (a *application) saveFavorite() {
	name := strings.TrimSpace(a.favorites.Text())
	if name == "" {
		a.showError(fmt.Errorf("请先在收藏框输入名称"))
		return
	}
	if err := a.engine.SaveFavorite(name, a.sendEdit.Text()); err != nil {
		a.showError(err)
		return
	}
	a.refreshFavorites()
	a.appendLog(fmt.Sprintf("已收藏报文: %s", name))
}

func (a *application) deleteFavorite() {
	name := a.favorites.Text()
	if name == "" {
		a.showError(fmt.Errorf("请先选择要删除的收藏"))
		return
	}
	if err := a.engine.DeleteFavorite(name); err != nil {
		a.showError(err)
		return
	}
	a.refreshFavorites()
	a.appendLog(fmt.Sprintf("已删除收藏: %s", name))
}

// newInstance 启动一个新实例(多开)。
func (a *application) newInstance() {
	exe, err := os.Executable()
	if err != nil {
		a.showError(err)
		return
	}
	if err := exec.Command(exe).Start(); err != nil {
		a.showError(err)
	}
}

func (a *application) copyPacketField(field string) {
	if a.packetTable == nil || a.packetModel == nil {
		return
	}
	idx := a.packetTable.CurrentIndex()
	if idx < 0 || idx >= len(a.packetModel.visible) {
		return
	}
	p := a.packetModel.visible[idx]
	var text string
	switch field {
	case "hex":
		text = p.Hex
	case "ascii":
		text = p.ASCII
	default:
		text = fmt.Sprintf("[%s %s] %s | %s | %d B", p.TS.Format("15:04:05.000"), p.Direction, p.Hex, p.ASCII, p.Length)
	}
	_ = walk.Clipboard().SetText(text)
}

// restoreMainWindow restores a minimized window and brings it to the foreground.
func (a *application) restoreMainWindow() {
	if win.IsIconic(a.mw.Handle()) {
		win.ShowWindow(a.mw.Handle(), win.SW_RESTORE)
	} else {
		a.mw.Show()
	}
	win.SetForegroundWindow(a.mw.Handle())
}

// setupTray 提供点击恢复主窗口和右键退出的通知区域入口。
func (a *application) setupTray() (err error) {
	ni, err := walk.NewNotifyIcon(a.mw)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			ni.Dispose()
		}
	}()
	if err = ni.SetIcon(uiIcon("app")); err != nil {
		return fmt.Errorf("设置图标: %w", err)
	}
	if err = ni.SetToolTip("CommBox"); err != nil {
		return fmt.Errorf("设置提示: %w", err)
	}

	ni.MouseDown().Attach(func(x, y int, button walk.MouseButton) {
		if button == walk.LeftButton {
			a.restoreMainWindow()
		}
	})

	showAction := walk.NewAction()
	showAction.SetText("显示主界面")
	showAction.Triggered().Attach(a.restoreMainWindow)
	quitAction := walk.NewAction()
	quitAction.SetText("退出")
	quitAction.Triggered().Attach(func() {
		a.stopTimer(false)
		a.assistant.stop()
		a.closed.Store(true)
		a.stopLoop()
		walk.App().Exit(0)
	})
	_ = ni.ContextMenu().Actions().Add(showAction)
	_ = ni.ContextMenu().Actions().Add(walk.NewSeparatorAction())
	_ = ni.ContextMenu().Actions().Add(quitAction)
	if err = ni.SetVisible(true); err != nil {
		return fmt.Errorf("显示图标: %w", err)
	}
	a.notifyIcon = ni
	return nil
}

func (a *application) openToolbox() {
	if a.toolboxWindow == nil || a.toolboxWindow.IsDisposed() {
		if err := (MainWindow{
			AssignTo: &a.toolboxWindow, Title: "校验与转换", MinSize: Size{Width: 520, Height: 340}, Size: Size{Width: 580, Height: 400}, Font: Font{Family: fontUI, PointSize: sizeBody}, Layout: VBox{Alignment: AlignHNearVNear, Margins: Margins{Left: 12, Top: 10, Right: 12, Bottom: 12}, Spacing: 8},
			Children: []Widget{
				Label{Text: "输入(HEX 校验用 01 03 00 0A；Base64/Unix 时间戳直接输文本或数字)"},
				TextEdit{AssignTo: &a.toolboxInput, MinSize: Size{Height: 60}},
				Composite{Layout: HBox{Alignment: AlignHNearVCenter}, Children: []Widget{
					PushButton{Text: "CRC16 Modbus", MinSize: Size{Height: btnH}, OnClicked: func() { a.runToolbox("modbus") }},
					PushButton{Text: "CRC16", MinSize: Size{Height: btnH}, OnClicked: func() { a.runToolbox("crc16") }},
					PushButton{Text: "CRC32", MinSize: Size{Height: btnH}, OnClicked: func() { a.runToolbox("crc32") }},
					PushButton{Text: "XOR", MinSize: Size{Height: btnH}, OnClicked: func() { a.runToolbox("xor") }},
					PushButton{Text: "SUM", MinSize: Size{Height: btnH}, OnClicked: func() { a.runToolbox("sum") }},
					HSpacer{},
				}},
				Composite{Layout: HBox{Alignment: AlignHNearVCenter}, Children: []Widget{
					PushButton{Text: "Base64 编码", MinSize: Size{Height: btnH}, OnClicked: func() { a.runToolbox("base64enc") }},
					PushButton{Text: "Base64 解码", MinSize: Size{Height: btnH}, OnClicked: func() { a.runToolbox("base64dec") }},
					PushButton{Text: "Unix 时间戳", MinSize: Size{Height: btnH}, OnClicked: func() { a.runToolbox("unixtime") }},
					HSpacer{},
				}},
				Label{AssignTo: &a.toolboxOutput, Text: "结果", MinSize: Size{Height: 44}},
			},
		}).Create(); err != nil {
			a.showError(err)
			return
		}
	}
	if icon := uiIcon("app"); icon != nil {
		a.toolboxWindow.SetIcon(icon)
	}
	a.toolboxWindow.Show()
}

func (a *application) runToolbox(kind string) {
	result := wincore.ParseToolbox(kind, a.toolboxInput.Text())
	a.toolboxOutput.SetText(result)
}

func (a *application) exportText(text, prefix string, owner walk.Form) {
	dialog := new(walk.FileDialog)
	dialog.Title = "导出接收数据"
	dialog.Filter = "文本文件 (*.txt)|*.txt|所有文件 (*.*)|*.*"
	dialog.FilePath = fmt.Sprintf("%s-%s.txt", prefix, time.Now().Format("20060102-150405"))
	ok, err := dialog.ShowSave(owner)
	if err != nil {
		a.showError(err)
		return
	}
	if ok {
		if err = os.WriteFile(dialog.FilePath, []byte(text), 0o644); err != nil {
			a.showError(err)
		}
	}
}

func (a *application) openDataDir() {
	if err := exec.Command("explorer.exe", a.engine.DataDir()).Start(); err != nil {
		a.showError(err)
	}
}

func (a *application) showError(err error) {
	owner := walk.Form(nil)
	if a.mw != nil {
		owner = a.mw
	}
	walk.MsgBox(owner, "CommBox", err.Error(), walk.MsgBoxOK|walk.MsgBoxIconError)
}
