//go:build windows

package main

import (
	"errors"
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
	"serial-tool/internal/wincore"
)

var modes = []string{"串口", "TCP", "UDP", "串口服务器", "HTTP 客户端"}

type application struct {
	loadingRecent                        bool
	modeButtons                          [4]*walk.PushButton
	peerList                             *walk.ListBox
	peerTitle, footer                    *walk.Label
	displayMode                          *walk.ComboBox
	autoScroll, showTime, clearAfterSend *walk.CheckBox
	assistant                            *assistantPanel
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
	receiveEdit, sendEdit, logEdit        *walk.TextEdit
	hexView, hexSend                      *walk.CheckBox
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
	loopSend                              *walk.CheckBox
	loopCount                             *walk.LineEdit
	loopButton                            *walk.PushButton
	loopMu                                sync.Mutex
	loopCancel                            chan struct{}
	statsLabel                            *walk.Label
	recentConn                            *walk.ComboBox
	recentSessions                        []wincore.SessionInfo
	timeFilter                            *walk.ComboBox
	vsWindow                              *walk.MainWindow
	vsIP                                  *walk.ComboBox
	vsPort                                *walk.LineEdit
	vsTable                               *walk.TableView
	vsModel                               *vserialModel
	protocolFilter                        *walk.ComboBox
	connectionFilter                      *walk.LineEdit
	sendTarget                            *walk.ComboBox
	udpTarget                             *walk.LineEdit
	targets                               []wincore.ConnectionInfo
	detailsLabel, sendPreview             *walk.Label
	selectionLabel                        *walk.Label
	detailColumns                         *walk.Action
	connecting, sending                   bool
	closed                                atomic.Bool
}

// vserialEntry 与 vserialModel 是虚拟串口管理窗口的表格数据。
type vserialEntry struct {
	id   int
	addr string
	link string
}

type vserialModel struct {
	walk.TableModelBase
	items []vserialEntry
}

func (m *vserialModel) RowCount() int { return len(m.items) }

func (m *vserialModel) Value(row, col int) interface{} {
	it := m.items[row]
	if col == 0 {
		return it.addr
	}
	return it.link
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
	if dir != "" && dir != "全部" && p.Direction != dir {
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
	app.setupTray()
	app.refreshPorts()
	app.updateMode()
	app.hexDisplay.Store(true)
	app.refreshSendHistory()
	app.refreshFavorites()
	app.refreshRecentConn()
	app.appendLog("SQLite 数据目录: " + app.engine.DataDir())
	go app.statsLoop()
	app.mw.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		if !*canceled {
			app.assistant.stop()
			app.closed.Store(true)
			app.stopTimer(false)
			app.stopLoop()
		}
	})
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
	a.role.SetVisible(a.mode.Text() == "TCP" || a.mode.Text() == "UDP" || a.mode.Text() == "串口服务器")
	a.protocol.SetVisible(a.mode.Text() == "串口服务器")
	if a.udpTarget != nil {
		a.udpTarget.SetVisible(a.mode.Text() == "UDP")
	}
	labels := []string{"TCP 客户端", "TCP 服务端", "UDP", "串口"}
	active := -1
	switch a.uiMode() {
	case wincore.ModeTCPClient:
		active = 0
	case wincore.ModeTCPServer:
		active = 1
	case wincore.ModeUDPClient, wincore.ModeUDPServer:
		active = 2
	case wincore.ModeSerial:
		active = 3
	}
	for i, b := range a.modeButtons {
		if b != nil {
			text := labels[i]
			if i == active {
				text = "● " + text
			}
			b.SetText(text)
			b.SetEnabled(!a.connected && !a.connecting)
		}
	}
	if !a.connected {
		text := map[bool]string{true: "启动监听", false: "连接"}[a.isServer()]
		if a.uiMode() == wincore.ModeSerial {
			text = "打开串口"
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
	baud, err := strconv.Atoi(a.baud.Text())
	if err != nil {
		return wincore.Config{}, fmt.Errorf("波特率无效")
	}
	dataBits, err := strconv.Atoi(a.data.Text())
	if err != nil {
		return wincore.Config{}, fmt.Errorf("数据位无效")
	}
	stopBits, err := strconv.Atoi(a.stop.Text())
	if err != nil {
		return wincore.Config{}, fmt.Errorf("停止位无效")
	}
	ip := strings.TrimSpace(a.netIP.Text())
	port := strings.TrimSpace(a.netPort.Text())
	var address string
	switch {
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
		Mode: a.uiMode(), SerialName: strings.TrimSpace(a.ports.Text()), Address: address,
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
	for _, b := range a.modeButtons {
		if b != nil {
			b.SetEnabled(false)
		}
	}
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
			}
			if a.isServer() {
				a.setConnStatus(colorBlue, "监听中")
			} else {
				a.setConnStatus(colorGreen, "已连接")
			}
			a.refreshRecentConn()
		})
	}()
}

func (a *application) sendOnce(fromTimer bool) {
	if a.sending {
		return
	}
	if !a.connected {
		a.sendPreview.SetText("请先连接或启动监听")
		return
	}
	input, asHex, eol := a.sendEdit.Text(), a.hexSend.Checked(), a.eol.Text()
	id, address := a.selectedSendTarget()
	a.sending = true
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
				a.showError(err)
				return
			}
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
		kw, dir := "", "全部"
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
		a.updatePacketStats()
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
	dir := "全部"
	if a.dirFilter != nil {
		dir = a.dirFilter.Text()
	}
	a.packetModel.refilter(kw, dir, a.sinceTime())
	a.updatePacketStats()
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
		a.packetModel.refilter("", "全部", time.Time{})
	}
}

func (a *application) openMonitor() {
	if a.monitorWindow == nil {
		var pause *walk.PushButton
		err := (MainWindow{
			AssignTo: &a.monitorWindow, Title: "实时数据监控", MinSize: Size{Width: 760, Height: 520}, Size: Size{Width: 900, Height: 620}, Layout: VBox{},
			Children: []Widget{
				Composite{Layout: HBox{}, Children: []Widget{
					Label{Text: "仅显示实时接收数据"}, HSpacer{},
					PushButton{Text: "数据库位置", OnClicked: a.openDataDir},
					PushButton{AssignTo: &pause, Text: "暂停滚动", OnClicked: func() {
						a.monitorPaused = !a.monitorPaused
						pause.SetText(map[bool]string{true: "继续滚动", false: "暂停滚动"}[a.monitorPaused])
					}},
					PushButton{Text: "清空", OnClicked: func() { _ = a.monitorEdit.SetText("") }},
					PushButton{Text: "导出", OnClicked: func() { a.exportText(a.monitorEdit.Text(), "monitor-data", a.monitorWindow) }},
				}},
				TextEdit{AssignTo: &a.monitorEdit, ReadOnly: true, VScroll: true, HScroll: true, MaxLength: 5000000, Font: Font{Family: "Consolas", PointSize: 10}},
			},
		}).Create()
		if err != nil {
			a.showError(err)
			return
		}
		a.monitorWindow.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
			*canceled = true
			a.monitorWindow.Hide()
		})
	}
	a.monitorWindow.Show()
}

var (
	colorGray   = walk.RGB(140, 140, 140)
	colorGreen  = walk.RGB(40, 180, 60)
	colorYellow = walk.RGB(210, 150, 0)
	colorRed    = walk.RGB(210, 50, 50)
	colorBlue   = walk.RGB(30, 120, 220)
)

// setConnStatus 同时更新灯颜色与文字。
func (a *application) setConnStatus(color walk.Color, text string) {
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
	if a.closed.Load() || a.status == nil {
		return
	}
	a.refreshConnections()
	a.showPeerDetails()
	labels := map[wincore.ConnState]string{wincore.StateDisconnected: "未连接", wincore.StateConnecting: "连接中", wincore.StateConnected: "已连接", wincore.StateReconnecting: "重连中", wincore.StateError: "错误"}
	label := labels[st.State]
	color := colorGray
	if st.State == wincore.StateConnected {
		color = colorGreen
		if st.Listening {
			label = fmt.Sprintf("监听中 · %d 客户端", st.PeerCount)
		}
	}
	if st.State == wincore.StateConnecting || st.State == wincore.StateReconnecting {
		color = colorYellow
	}
	if st.State == wincore.StateError {
		color = colorRed
	}
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
	a.footer.SetText(fmt.Sprintf("%s %s  |  %s  |  RX %s  TX %s  |  %s/s  |  %s", a.uiMode(), label, address, wincore.FormatBytes(st.RXBytes), wincore.FormatBytes(st.TXBytes), wincore.FormatBytes(rate), elapsed))
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
	if a.statsLabel == nil || a.packetModel == nil {
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
	_ = a.statsLabel.SetText(fmt.Sprintf("· 共 %d 条 · RX %d/%s · TX %d/%s",
		rxCnt+txCnt,
		rxCnt, wincore.FormatBytes(uint64(rxB)),
		txCnt, wincore.FormatBytes(uint64(txB))))
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

func (a *application) toggleHexView() {
	if a.hexView == nil {
		return
	}
	if a.hexView != nil {
		a.hexView.SetChecked(!a.hexView.Checked())
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

// setupTray 让应用关闭窗口后仍在后台运行(托盘图标),点击图标恢复,右键可退出。
func (a *application) setupTray() {
	ni, err := walk.NewNotifyIcon(a.mw)
	if err != nil {
		return
	}
	a.notifyIcon = ni
	if icon := uiIcon("app"); icon != nil {
		ni.SetIcon(icon)
	}
	_ = ni.SetToolTip("CommBox")
	_ = ni.SetVisible(true)

	ni.MouseDown().Attach(func(x, y int, button walk.MouseButton) {
		if button == walk.LeftButton {
			a.mw.Show()
		}
	})

	showAction := walk.NewAction()
	showAction.SetText("显示主界面")
	showAction.Triggered().Attach(func() {
		a.mw.Show()
	})
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
}

func (a *application) openToolbox() {
	if a.toolboxWindow == nil {
		if err := (MainWindow{
			AssignTo: &a.toolboxWindow, Title: "工具箱", MinSize: Size{Width: 520, Height: 340}, Size: Size{Width: 560, Height: 380}, Layout: VBox{Margins: Margins{Left: 12, Top: 12, Right: 12, Bottom: 12}},
			Children: []Widget{
				Label{Text: "输入(HEX 校验用 01 03 00 0A；Base64/Unix 时间戳直接输文本或数字)"},
				TextEdit{AssignTo: &a.toolboxInput, MinSize: Size{Height: 60}},
				Composite{Layout: HBox{}, Children: []Widget{
					PushButton{Text: "CRC16 Modbus", OnClicked: func() { a.runToolbox("modbus") }},
					PushButton{Text: "CRC16", OnClicked: func() { a.runToolbox("crc16") }},
					PushButton{Text: "CRC32", OnClicked: func() { a.runToolbox("crc32") }},
					PushButton{Text: "XOR", OnClicked: func() { a.runToolbox("xor") }},
					PushButton{Text: "SUM", OnClicked: func() { a.runToolbox("sum") }},
				}},
				Composite{Layout: HBox{}, Children: []Widget{
					PushButton{Text: "Base64 编码", OnClicked: func() { a.runToolbox("base64enc") }},
					PushButton{Text: "Base64 解码", OnClicked: func() { a.runToolbox("base64dec") }},
					PushButton{Text: "Unix 时间戳", OnClicked: func() { a.runToolbox("unixtime") }},
					HSpacer{},
				}},
				Label{AssignTo: &a.toolboxOutput, Text: "结果", MinSize: Size{Height: 40}},
			},
		}).Create(); err != nil {
			a.showError(err)
			return
		}
	}
	a.toolboxWindow.Show()
}

func (a *application) runToolbox(kind string) {
	result := wincore.ParseToolbox(kind, a.toolboxInput.Text())
	a.toolboxOutput.SetText(result)
}

// showChecksum 保留兼容旧调用路径。
func (a *application) showChecksum(kind string) { a.runToolbox(kind) }

func (a *application) openVSerial() {
	if a.vsWindow == nil {
		a.vsModel = new(vserialModel)
		if err := (MainWindow{
			AssignTo: &a.vsWindow, Title: "虚拟串口映射(后台运行,可多个)", MinSize: Size{Width: 640, Height: 420}, Size: Size{Width: 680, Height: 480}, Layout: VBox{Margins: Margins{Left: 12, Top: 12, Right: 12, Bottom: 12}},
			Children: []Widget{
				Label{Text: "需要 com0com 驱动；Windows 端口打开与驱动命令超时问题仍待实机验证。"},
				Composite{Layout: HBox{}, Children: []Widget{
					Label{Text: "IP"},
					ComboBox{AssignTo: &a.vsIP, Editable: true, MinSize: Size{Width: 180}},
					Label{Text: "端口"},
					LineEdit{AssignTo: &a.vsPort, Text: "1502", MinSize: Size{Width: 80}},
					PushButton{Text: "添加映射", OnClicked: a.addVSerial},
				}},
				TableView{AssignTo: &a.vsTable, Model: a.vsModel, StretchFactor: 1, Columns: []TableViewColumn{
					{Title: "TCP 端点", Width: 200},
					{Title: "虚拟串口设备", Width: 380},
				}},
				Composite{Layout: HBox{}, Children: []Widget{
					PushButton{Text: "安装驱动", OnClicked: a.installDriver},
					PushButton{Text: "停止选中", OnClicked: a.removeVSerial},
					PushButton{Text: "复制设备路径", OnClicked: a.copyVSerialPath},
				}},
			},
		}).Create(); err != nil {
			a.showError(err)
			return
		}
		_ = a.vsIP.SetModel(wincore.LocalIPs())
	}
	a.vsWindow.Show()
}

func (a *application) installDriver() {
	if err := wincore.InstallCom0comDriver(); err != nil {
		// 内嵌驱动未包含安装程序时,回退到打开下载页
		_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", "https://com0com.sourceforge.net/").Start()
		a.showError(fmt.Errorf("%v\n\n已打开 com0com 下载页,请手动下载安装。", err))
		return
	}
	a.appendLog("已请求安装 com0com 驱动,请在弹出的 UAC 窗口中确认")
}

func (a *application) addVSerial() {
	ip := strings.TrimSpace(a.vsIP.Text())
	port := strings.TrimSpace(a.vsPort.Text())
	if ip == "" || port == "" {
		a.showError(fmt.Errorf("请输入 IP 和端口"))
		return
	}
	p, err := strconv.Atoi(port)
	if err != nil || p <= 0 {
		a.showError(fmt.Errorf("端口无效"))
		return
	}
	info, err := a.engine.AddVirtualSerial(fmt.Sprintf("%s:%d", ip, p))
	if err != nil {
		if errors.Is(err, wincore.ErrVSerialNeedsDriver) {
			a.showError(fmt.Errorf("虚拟串口需要 com0com 驱动。\n请先安装:https://com0com.sourceforge.net/\n安装后重启 CommBox 再试。"))
			return
		}
		a.showError(err)
		return
	}
	a.vsModel.items = append(a.vsModel.items, vserialEntry{id: info.ID, addr: info.Addr, link: info.Link})
	a.vsModel.PublishRowsReset()
	a.appendLog(fmt.Sprintf("虚拟串口 #%d 已创建: %s → %s", info.ID, info.Addr, info.Link))
}

func (a *application) removeVSerial() {
	idx := a.vsTable.CurrentIndex()
	if idx < 0 || idx >= len(a.vsModel.items) {
		a.showError(fmt.Errorf("请先选中一行"))
		return
	}
	it := a.vsModel.items[idx]
	a.engine.RemoveVirtualSerial(it.id)
	a.vsModel.items = append(a.vsModel.items[:idx], a.vsModel.items[idx+1:]...)
	a.vsModel.PublishRowsReset()
	a.appendLog(fmt.Sprintf("虚拟串口 #%d 已停止", it.id))
}

func (a *application) copyVSerialPath() {
	idx := a.vsTable.CurrentIndex()
	if idx < 0 || idx >= len(a.vsModel.items) {
		a.showError(fmt.Errorf("请先选中一行"))
		return
	}
	_ = walk.Clipboard().SetText(a.vsModel.items[idx].link)
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
