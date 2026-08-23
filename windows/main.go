//go:build windows

package main

import (
	"fmt"
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

var modes = []string{"串口", "TCP 服务端", "TCP 客户端", "UDP 服务端", "UDP 客户端", "串口服务器", "HTTP 客户端"}

type application struct {
	mw                                    *walk.MainWindow
	mode, ports, baud, data, parity, stop *walk.ComboBox
	protocol, role, eol                   *walk.ComboBox
	address, interval                     *walk.LineEdit
	serialGroup, networkGroup             *walk.GroupBox
	connectButton, timerButton            *walk.PushButton
	status                                *walk.Label
	receiveEdit, sendEdit                 *walk.TextEdit
	hexView, hexSend                      *walk.CheckBox
	monitorWindow                         *walk.MainWindow
	monitorEdit                           *walk.TextEdit
	monitorPaused                         bool
	engine                                *wincore.Engine
	connected                             bool
	hexDisplay                            atomic.Bool
	timerMu                               sync.Mutex
	timerCancel                           chan struct{}
	vsWindow                              *walk.MainWindow
	vsIP                                  *walk.ComboBox
	vsPort                                *walk.LineEdit
	vsTable                               *walk.TableView
	vsModel                               *vserialModel
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

func main() {
	app := new(application)
	configDir, err := os.UserConfigDir()
	if err != nil {
		walk.MsgBox(nil, "Go 网络与串口工具", err.Error(), walk.MsgBoxOK|walk.MsgBoxIconError)
		return
	}
	dataDir := filepath.Join(configDir, "GoSerialTool", "data")
	app.engine, err = wincore.New(dataDir, app.onData, app.onLog)
	if err != nil {
		walk.MsgBox(nil, "SQLite 初始化失败", err.Error(), walk.MsgBoxOK|walk.MsgBoxIconError)
		return
	}
	app.engine.SetOnClosed(app.onClosed)
	defer app.engine.Close()
	if err = app.createWindow(); err != nil {
		walk.MsgBox(nil, "界面初始化失败", err.Error(), walk.MsgBoxOK|walk.MsgBoxIconError)
		return
	}
	app.refreshPorts()
	app.updateMode()
	app.appendLog("SQLite 数据目录: " + app.engine.DataDir())
	app.mw.Run()
	app.stopTimer(false)
}

func (a *application) createWindow() error {
	return (MainWindow{
		AssignTo: &a.mw,
		Title:    "Go 网络与串口工具 - Windows",
		MinSize:  Size{Width: 1000, Height: 680},
		Size:     Size{Width: 1180, Height: 760},
		Layout:   HBox{Margins: Margins{Left: 12, Top: 12, Right: 12, Bottom: 12}},
		Children: []Widget{
			Composite{
				MinSize: Size{Width: 300}, MaxSize: Size{Width: 330}, Layout: VBox{},
				Children: []Widget{
					Label{Text: "工作模式"},
					ComboBox{AssignTo: &a.mode, Model: modes, CurrentIndex: 0, OnCurrentIndexChanged: a.updateMode},
					GroupBox{
						AssignTo: &a.serialGroup, Title: "串口参数", Layout: VBox{},
						Children: []Widget{
							Composite{Layout: HBox{}, Children: []Widget{
								Label{Text: "端口", MinSize: Size{Width: 45}},
								ComboBox{AssignTo: &a.ports, Editable: true, StretchFactor: 1},
								PushButton{Text: "刷新", OnClicked: a.refreshPorts},
							}},
							Composite{Layout: HBox{}, Children: []Widget{
								Label{Text: "波特率", MinSize: Size{Width: 55}},
								ComboBox{AssignTo: &a.baud, Editable: true, Model: []string{"1200", "2400", "4800", "9600", "19200", "38400", "57600", "115200", "230400", "460800", "921600"}, CurrentIndex: 7},
								Label{Text: "数据位"}, ComboBox{AssignTo: &a.data, Model: []string{"5", "6", "7", "8"}, CurrentIndex: 3},
							}},
							Composite{Layout: HBox{}, Children: []Widget{
								Label{Text: "校验", MinSize: Size{Width: 55}}, ComboBox{AssignTo: &a.parity, Model: []string{"无校验", "奇校验", "偶校验"}, CurrentIndex: 0},
								Label{Text: "停止位"}, ComboBox{AssignTo: &a.stop, Model: []string{"1", "2"}, CurrentIndex: 0},
							}},
						},
					},
					GroupBox{
						AssignTo: &a.networkGroup, Title: "网络参数", Layout: VBox{},
						Children: []Widget{
							Label{Text: "监听地址 / 远程 IP:端口"},
							LineEdit{AssignTo: &a.address, Text: ":9000", CueBanner: "例如 192.168.1.100:9000"},
							Composite{Layout: HBox{}, Children: []Widget{
								Label{Text: "协议", MinSize: Size{Width: 45}}, ComboBox{AssignTo: &a.protocol, Model: []string{"TCP", "UDP"}, CurrentIndex: 0},
								Label{Text: "角色"}, ComboBox{AssignTo: &a.role, Model: []string{"服务端", "客户端"}, CurrentIndex: 0, OnCurrentIndexChanged: a.updateAddressDefault},
							}},
						},
					},
					VSpacer{},
					Label{AssignTo: &a.status, Text: "● 未连接"},
					PushButton{AssignTo: &a.connectButton, Text: "连接", MinSize: Size{Height: 38}, OnClicked: a.toggleConnection},
					Label{Text: "数据库按日期和 100 MiB 自动分文件"},
				},
			},
			Composite{
				StretchFactor: 1, Layout: VBox{},
				Children: []Widget{
					Composite{Layout: HBox{}, Children: []Widget{
						Label{Text: "接收数据"}, HSpacer{},
						CheckBox{AssignTo: &a.hexView, Text: "HEX 显示", OnCheckedChanged: func() { a.hexDisplay.Store(a.hexView.Checked()) }},
						PushButton{Text: "监控窗口", OnClicked: a.openMonitor},
						PushButton{Text: "虚拟串口", OnClicked: a.openVSerial},
						PushButton{Text: "导出", OnClicked: func() { a.exportText(a.receiveEdit.Text(), "serial-log", a.mw) }},
						PushButton{Text: "清空", OnClicked: func() { _ = a.receiveEdit.SetText("") }},
					}},
					TextEdit{AssignTo: &a.receiveEdit, ReadOnly: true, VScroll: true, HScroll: true, StretchFactor: 3, MaxLength: 5000000, Font: Font{Family: "Consolas", PointSize: 10}},
					Composite{Layout: HBox{}, Children: []Widget{
						Label{Text: "发送数据"}, HSpacer{}, CheckBox{AssignTo: &a.hexSend, Text: "HEX 发送", Checked: true},
						Label{Text: "行尾"}, ComboBox{AssignTo: &a.eol, Model: []string{"无", "LF", "CR", "CRLF"}, CurrentIndex: 0, MinSize: Size{Width: 75}},
						Label{Text: "间隔(ms)"}, LineEdit{AssignTo: &a.interval, Text: "1000", MinSize: Size{Width: 75}, MaxSize: Size{Width: 90}},
					}},
					Composite{Layout: HBox{}, StretchFactor: 1, Children: []Widget{
						TextEdit{AssignTo: &a.sendEdit, VScroll: true, HScroll: true, StretchFactor: 1, Font: Font{Family: "Consolas", PointSize: 10}},
						Composite{MinSize: Size{Width: 100}, MaxSize: Size{Width: 110}, Layout: VBox{}, Children: []Widget{
							PushButton{Text: "发送一次", StretchFactor: 1, OnClicked: func() { a.sendOnce(false) }},
							PushButton{AssignTo: &a.timerButton, Text: "开始定时", StretchFactor: 1, OnClicked: a.toggleTimer},
						}},
					}},
				},
			},
		},
	}).Create()
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
	if a.mode == nil || a.serialGroup == nil {
		return
	}
	mode := wincore.Mode(a.mode.Text())
	spec := wincore.SpecOf(mode)
	a.serialGroup.SetEnabled(spec.NeedsSerial && !a.connected)
	a.networkGroup.SetEnabled(spec.NeedsNet && !a.connected)
	if spec.NeedsProto {
		a.protocol.SetEnabled(!a.connected)
		a.role.SetEnabled(!a.connected)
	} else {
		switch mode {
		case wincore.ModeTCPServer:
			a.setProtocolRole(0, 0)
		case wincore.ModeTCPClient:
			a.setProtocolRole(0, 1)
		case wincore.ModeUDPServer:
			a.setProtocolRole(1, 0)
		case wincore.ModeUDPClient:
			a.setProtocolRole(1, 1)
		}
		a.protocol.SetEnabled(false)
		a.role.SetEnabled(false)
	}
	a.updateAddressDefault()
	if !a.connected {
		a.connectButton.SetText(map[bool]string{true: "开始监听", false: "连接"}[a.isServer()])
	}
}

func (a *application) setProtocolRole(protocol, role int) {
	_ = a.protocol.SetCurrentIndex(protocol)
	_ = a.role.SetCurrentIndex(role)
}

func (a *application) updateAddressDefault() {
	if a.address == nil || a.mode == nil || a.connected || a.mode.Text() == string(wincore.ModeSerial) {
		return
	}
	if a.isServer() {
		_ = a.address.SetText(":9000")
	} else {
		_ = a.address.SetText("127.0.0.1:9000")
	}
}

func (a *application) isServer() bool {
	mode := a.mode.Text()
	return strings.HasSuffix(mode, "服务端") || (mode == string(wincore.ModeSerialServer) && a.role.Text() == "服务端")
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
	address := strings.TrimSpace(a.address.Text())
	if a.mode.Text() != string(wincore.ModeSerial) && !strings.Contains(address, ":") {
		port, parseErr := strconv.Atoi(address)
		if parseErr != nil || port <= 0 {
			return wincore.Config{}, fmt.Errorf("地址应为 IP:端口")
		}
		if a.isServer() {
			address = ":" + address
		} else {
			address = "127.0.0.1:" + address
		}
		_ = a.address.SetText(address)
	}
	return wincore.BuildConfig(wincore.ConnParams{
		Mode: wincore.Mode(a.mode.Text()), SerialName: strings.TrimSpace(a.ports.Text()), Address: address,
		Baud: baud, DataBits: dataBits, StopBits: stopBits, Parity: a.parity.Text(),
		Protocol: a.protocol.Text(), Role: a.role.Text(),
	})
}

func (a *application) toggleConnection() {
	if a.connected {
		a.stopTimer(true)
		a.engine.Disconnect()
		a.connected = false
		a.mode.SetEnabled(true)
		a.status.SetText("● 未连接")
		a.updateMode()
		a.appendLog("已停止")
		return
	}
	cfg, err := a.config()
	if err != nil {
		a.showError(err)
		return
	}
	a.connectButton.SetEnabled(false)
	a.status.SetText("● 正在连接...")
	go func() {
		err := a.engine.Connect(cfg)
		a.mw.Synchronize(func() {
			a.connectButton.SetEnabled(true)
			if err != nil {
				a.status.SetText("● 连接失败")
				a.showError(err)
				return
			}
			a.connected = true
			a.mode.SetEnabled(false)
			a.serialGroup.SetEnabled(false)
			a.networkGroup.SetEnabled(false)
			a.connectButton.SetText(map[bool]string{true: "停止监听", false: "断开"}[a.isServer()])
			a.status.SetText(map[bool]string{true: "● 监听中", false: "● 已连接"}[a.isServer()])
		})
	}()
}

func (a *application) sendOnce(fromTimer bool) {
	input, asHex := a.sendEdit.Text(), a.hexSend.Checked()
	err := a.engine.Send(input, asHex, a.eol.Text())
	if err != nil {
		if fromTimer {
			a.stopTimer(true)
		}
		a.showError(err)
		return
	}
	// HTTP 模式的请求/响应由引擎日志与 onData 呈现,此处只记录其它模式的发送
	if a.mode.Text() != "HTTP 客户端" {
		var body string
		if a.hexDisplay.Load() {
			body = fmt.Sprintf("% X", []byte(input))
			if data, e := wincore.ParseData(input, asHex, a.eol.Text()); e == nil {
				body = fmt.Sprintf("% X", data)
			}
		} else {
			body = input
		}
		text := fmt.Sprintf("[%s 发送] %s\r\n", time.Now().Format("15:04:05.000"), body)
		a.mw.Synchronize(func() { a.appendDisplay(a.receiveEdit, text) })
	}
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
				if err := a.engine.Send(input, asHex, eol); err != nil {
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

func (a *application) onData(source string, data []byte) {
	var body string
	if a.hexDisplay.Load() {
		body = fmt.Sprintf("% X", data)
	} else {
		body = strings.ReplaceAll(strings.ToValidUTF8(string(data), "�"), "\x00", "␀")
		body = strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n")
	}
	text := fmt.Sprintf("[%s 接收] %s\r\n", time.Now().Format("15:04:05.000"), body)
	a.mw.Synchronize(func() {
		a.appendDisplay(a.receiveEdit, text)
		if a.monitorEdit != nil {
			a.appendDisplay(a.monitorEdit, text)
			if !a.monitorPaused {
				a.monitorEdit.ScrollToCaret()
			}
		}
	})
}

// onClosed 在被动断开(远端关闭等)时把界面同步回未连接状态。
func (a *application) onClosed() {
	a.mw.Synchronize(func() {
		if !a.connected {
			return
		}
		a.stopTimer(false)
		a.engine.Disconnect()
		a.connected = false
		a.mode.SetEnabled(true)
		a.status.SetText("● 未连接")
		a.updateMode()
		a.appendLog("连接已断开")
	})
}

func (a *application) onLog(text string) { a.appendLog(text) }

func (a *application) appendLog(text string) {
	if a.mw == nil {
		return
	}
	a.mw.Synchronize(func() { a.appendDisplay(a.receiveEdit, "\r\n["+text+"]\r\n") })
}

func (a *application) appendDisplay(edit *walk.TextEdit, text string) {
	if edit == nil {
		return
	}
	if edit.TextLength()+len(text) > 4500000 {
		_ = edit.SetText("[显示缓存已清理，完整数据仍保存在 SQLite]\r\n")
	}
	edit.AppendText(text)
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

func (a *application) openVSerial() {
	if a.vsWindow == nil {
		a.vsModel = new(vserialModel)
		if err := (MainWindow{
			AssignTo: &a.vsWindow, Title: "虚拟串口映射(后台运行,可多个)", MinSize: Size{Width: 640, Height: 420}, Size: Size{Width: 680, Height: 480}, Layout: VBox{Margins: Margins{Left: 12, Top: 12, Right: 12, Bottom: 12}},
			Children: []Widget{
				Label{Text: "TCP 端点 → 本机虚拟串口。添加后在后台持续运行,断开主连接也不受影响。"},
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
					PushButton{Text: "停止选中", OnClicked: a.removeVSerial},
					PushButton{Text: "复制设备路径", OnClicked: a.copyVSerialPath},
					Label{Text: "用 screen 或另一个串口工具打开该设备"},
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
	walk.MsgBox(owner, "Go 网络与串口工具", err.Error(), walk.MsgBoxOK|walk.MsgBoxIconError)
}
