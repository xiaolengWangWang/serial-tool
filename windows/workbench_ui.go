//go:build windows

package main

import (
	"encoding/csv"
	"fmt"
	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"os"
	"serial-tool/internal/wincore"
	"strings"
	"time"
)

func (a *application) createWindow() error {
	a.peerBaseline = map[string]wincore.ConnectionInfo{}
	a.assistant = &assistantPanel{app: a}
	if err := (MainWindow{AssignTo: &a.mw, Title: "CommBox v" + wincore.Version + " · Windows", Size: Size{Width: 1280, Height: 820}, MinSize: Size{Width: 1120, Height: 760}, Font: Font{Family: "Microsoft YaHei UI", PointSize: 9}, Background: SolidColorBrush{Color: walk.RGB(245, 248, 252)}, Layout: VBox{Margins: Margins{Left: 12, Top: 8, Right: 12, Bottom: 8}, Spacing: 8}, MenuItems: []MenuItem{
		Menu{Text: "文件", Items: []MenuItem{Action{Text: "新建实例", OnTriggered: a.newInstance}, Action{Text: "保存 TXT", OnTriggered: func() { a.exportText(a.packetModel.exportText(), "commbox", a.mw) }}, Action{Text: "导出 CSV", OnTriggered: a.exportCSV}, Action{Text: "退出", OnTriggered: func() { a.mw.Close() }}}},
		Menu{Text: "查看", Items: []MenuItem{Action{Text: "AI 助手", OnTriggered: a.toggleAssistant}, Action{Text: "实时监控窗口", OnTriggered: a.openMonitor}}},
		Menu{Text: "工具", Items: []MenuItem{Action{Text: "HTTP 工作台", OnTriggered: a.openHTTPWorkspace}, Action{Text: "校验与转换", OnTriggered: a.openToolbox}, Action{Text: "连接管理", OnTriggered: a.openConnections}, Action{Text: "串口服务器", OnTriggered: func() {
			if !a.connected && !a.connecting {
				a.mode.SetCurrentIndex(3)
				a.updateMode()
			}
		}}, Action{Text: "虚拟串口映射", OnTriggered: a.openVSerial}, Action{Text: "历史数据分析", OnTriggered: a.openDatabaseAnalysis}}},
		Menu{Text: "设置", Items: []MenuItem{Action{Text: "AI 设置", OnTriggered: a.assistant.settings}, Action{Text: "连接数与桥接", OnTriggered: a.openConnections}}},
		Menu{Text: "帮助", Items: []MenuItem{Action{Text: "使用说明", OnTriggered: a.showHelp}, Action{Text: "发送 (F5)", Image: uiIcon("send"), Shortcut: Shortcut{Key: walk.KeyF5}, OnTriggered: func() { a.sendOnce(false) }}}},
	}, Children: []Widget{
		// 不设工具栏：原有五个按钮在菜单栏或数据监控标题行都已有等价入口，
		// 去掉整行可把纵向空间还给数据表（对齐 mac，app.m 同样没有工具栏）。
		Composite{Layout: HBox{MarginsZero: true, Spacing: 10}, StretchFactor: 1, Children: []Widget{
			ScrollView{HorizontalFixed: true, Layout: VBox{MarginsZero: true}, MinSize: Size{Width: 300}, MaxSize: Size{Width: 320}, Children: []Widget{
				Label{Text: "连接配置 · 工作模式", Font: Font{Family: "Microsoft YaHei UI", PointSize: 12, Bold: true}, TextColor: walk.RGB(23, 61, 110)},
				ComboBox{AssignTo: &a.mode, ToolTipText: "串口 / TCP / UDP / 串口服务器 / HTTP 客户端", Model: modes, CurrentIndex: 1, OnCurrentIndexChanged: a.updateMode},
				GroupBox{AssignTo: &a.serialGroup, Title: "串口参数", Layout: Grid{Columns: 2}, Children: []Widget{
					Label{Text: "端口"}, ComboBox{AssignTo: &a.ports, Editable: true, MinSize: Size{Width: 130}},
					Label{Text: "波特率"}, ComboBox{AssignTo: &a.baud, Editable: true, Model: []string{"1200", "2400", "4800", "9600", "19200", "38400", "57600", "115200", "230400", "460800", "921600"}, CurrentIndex: 7},
					Label{Text: "数据位"}, ComboBox{AssignTo: &a.data, Model: []string{"5", "6", "7", "8"}, CurrentIndex: 3},
					Label{Text: "校验"}, ComboBox{AssignTo: &a.parity, Model: []string{"无校验", "奇校验", "偶校验"}, CurrentIndex: 0},
					Label{Text: "停止位"}, ComboBox{AssignTo: &a.stop, Model: []string{"1", "2"}, CurrentIndex: 0}, PushButton{Text: "刷新串口", Image: uiIcon("refresh"), ColumnSpan: 2, OnClicked: a.refreshPorts},
				}},
				GroupBox{AssignTo: &a.networkGroup, Title: "网络参数", Layout: VBox{}, Children: []Widget{
					Label{AssignTo: &a.addressLabel, Text: "服务器地址"}, ComboBox{AssignTo: &a.netIP, Editable: true, Model: append([]string{"127.0.0.1", "0.0.0.0"}, wincore.LocalIPs()...), CurrentIndex: 0},
					Label{AssignTo: &a.portLabel, Text: "端口"}, LineEdit{AssignTo: &a.netPort, Text: "9000"},
					ComboBox{AssignTo: &a.protocol, Model: []string{"TCP", "UDP"}, CurrentIndex: 0, Visible: false},
					ComboBox{AssignTo: &a.role, Model: []string{"服务端", "客户端"}, CurrentIndex: 1, OnCurrentIndexChanged: a.updateMode},
					CheckBox{AssignTo: &a.autoReconnect, Text: "自动重连", Checked: true},
					Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{Label{Text: "重连间隔 (s)"}, LineEdit{AssignTo: &a.reconnectInterval, Text: "2", MinSize: Size{Width: 56}, MaxSize: Size{Width: 70}}, HSpacer{}}},
				}},
				Composite{Layout: HBox{}, Children: []Widget{Label{AssignTo: &a.statusDot, Text: "●", TextColor: colorGray}, Label{AssignTo: &a.status, Text: "未连接"}}},
				PushButton{AssignTo: &a.connectButton, Text: "连接", MinSize: Size{Height: 38}, Image: uiIcon("connect"), OnClicked: a.toggleConnection},
				Label{AssignTo: &a.peerTitle, Text: "客户端 / 对端 (0)", Font: Font{Family: "Microsoft YaHei UI", PointSize: 9, Bold: true}},
				ListBox{AssignTo: &a.peerList, Model: []string{}, StretchFactor: 1, MinSize: Size{Height: 80}, OnCurrentIndexChanged: a.showPeerDetails, OnItemActivated: func() { a.peerAction("send") }, ContextMenuItems: []MenuItem{
					Action{Text: "发送数据", OnTriggered: func() { a.peerAction("send") }}, Action{Text: "仅查看该客户端", OnTriggered: func() { a.peerAction("filter") }}, Action{Text: "断开连接", OnTriggered: func() { a.peerAction("disconnect") }}, Action{Text: "复制地址", OnTriggered: func() { a.peerAction("copy") }}, Action{Text: "清空显示统计", OnTriggered: func() { a.peerAction("reset"); a.showPeerDetails() }},
				}},
				Label{AssignTo: &a.detailsLabel, Text: "选择客户端查看连接信息", MinSize: Size{Height: 60}},
				Label{Text: "最近连接"}, ComboBox{AssignTo: &a.recentConn, OnCurrentIndexChanged: a.onRecentConnSelected},
			}},
			Composite{StretchFactor: 1, Layout: VBox{MarginsZero: true}, Children: []Widget{
				// 条数并入标题行，不再单独占一行。
				Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{Label{Text: "数据监控", Font: Font{Family: "Microsoft YaHei UI", PointSize: 12, Bold: true}}, Label{AssignTo: &a.statsLabel, Text: "0 条"}, HSpacer{}, PushButton{Text: "清空", Image: uiIcon("clear"), OnClicked: func() { a.packetModel.clear(); a.updatePacketStats() }}, PushButton{Text: "保存", Image: uiIcon("save"), OnClicked: func() { a.exportText(a.packetModel.exportText(), "commbox", a.mw) }}, PushButton{Text: "AI 分析", Image: uiIcon("ai"), OnClicked: func() { a.showAssistant(); a.assistant.analyze("全面诊断") }}}},
				// 原先分散在三行的筛选合并为一行，统一用 ComboBox，搜索框拉伸占满剩余宽度。
				// 每个下拉框按最长选项定宽，否则 HBox 会按剩余空间均摊，与内容无关。
				Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{LineEdit{AssignTo: &a.searchEdit, CueBanner: "搜索 HEX / ASCII / 文本", StretchFactor: 1, MinSize: Size{Width: 200}, OnTextChanged: a.applyFilter}, ComboBox{AssignTo: &a.dirFilter, Model: []string{"全部", "RX", "TX", "EVENT"}, CurrentIndex: 0, MinSize: Size{Width: 78}, MaxSize: Size{Width: 86}, OnCurrentIndexChanged: a.applyFilter}, ComboBox{AssignTo: &a.displayMode, Model: []string{"HEX + ASCII", "HEX", "ASCII"}, CurrentIndex: 0, MinSize: Size{Width: 112}, MaxSize: Size{Width: 124}, OnCurrentIndexChanged: a.updateDisplay}, ComboBox{AssignTo: &a.protocolFilter, Model: []string{"全部协议", "SERIAL", "TCP", "UDP", "HTTP"}, CurrentIndex: 0, MinSize: Size{Width: 94}, MaxSize: Size{Width: 104}, OnCurrentIndexChanged: a.applyFilter}, ComboBox{AssignTo: &a.timeFilter, Model: []string{"全部", "1分钟", "5分钟", "30分钟"}, CurrentIndex: 0, MinSize: Size{Width: 84}, MaxSize: Size{Width: 94}, OnCurrentIndexChanged: a.applyFilter}, PushButton{Text: "重置", MaxSize: Size{Width: 64}, OnClicked: a.clearFilter}}},
				VSplitter{StretchFactor: 1, Children: []Widget{
					// 协议 / 来源 / 连接 ID 默认隐藏：八列合计 895px，而 AI 面板展开时中栏仅约 700px，
					// 全显必然出横向滚动条。需要时通过右键菜单打开。
					TabWidget{StretchFactor: 4, Pages: []TabPage{{Title: "数据", Layout: VBox{MarginsZero: true}, Children: []Widget{TableView{AssignTo: &a.packetTable, Model: a.packetModel, MultiSelection: true, AlternatingRowBG: true, LastColumnStretched: true, Font: Font{Family: "Consolas", PointSize: 9}, OnSelectedIndexesChanged: a.updateSelectionLabel, Columns: []TableViewColumn{{Title: "时间", Width: 95}, {Title: "方向", Width: 42}, {Title: "HEX", Width: 245}, {Title: "ASCII", Width: 95}, {Title: "长度", Width: 48}, {Title: "协议", Width: 60, Hidden: true}, {Title: "来源", Width: 140, Hidden: true}, {Title: "连接 ID", Width: 170, Hidden: true}}, ContextMenuItems: []MenuItem{
						Action{Text: "复制 HEX", OnTriggered: func() { a.copyPacketField("hex") }}, Action{Text: "复制 ASCII", OnTriggered: func() { a.copyPacketField("ascii") }}, Action{Text: "复制整行", OnTriggered: func() { a.copyPacketField("all") }}, Action{Text: "重新发送", OnTriggered: func() { a.loadPacket(); a.sendOnce(false) }}, Action{Text: "添加到快捷发送", OnTriggered: a.loadPacket}, Action{Text: "AI 分析选中数据", OnTriggered: a.analyzeSelected}, Separator{}, Action{AssignTo: &a.detailColumns, Text: "显示协议 / 来源 / 连接 ID 列", Checkable: true, OnTriggered: a.toggleDetailColumns}, Action{Text: "导出 CSV", OnTriggered: a.exportCSV},
					}},
						// 选中操作行：AI 面板有“当前选中数据”这个分析范围，此前主区却没有选择入口和计数。
						Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
							Label{AssignTo: &a.selectionLabel, Text: "未选中"},
							PushButton{Text: "全选", OnClicked: a.selectAllPackets},
							PushButton{Text: "清除", OnClicked: a.clearPacketSelection},
							PushButton{Text: "反选", OnClicked: a.invertPacketSelection},
							PushButton{Text: "分析选中", Image: uiIcon("ai"), OnClicked: a.analyzeSelected},
							HSpacer{},
							LineEdit{AssignTo: &a.connectionFilter, CueBanner: "连接 ID / 来源地址", MaxSize: Size{Width: 170}, OnTextChanged: a.applyFilter},
							CheckBox{AssignTo: &a.autoScroll, Text: "自动滚动", Checked: true},
							CheckBox{AssignTo: &a.showTime, Text: "时间", Checked: true, OnCheckedChanged: a.updateDisplay},
						}},
					}}, {Title: "日志", Layout: VBox{}, Children: []Widget{TextEdit{AssignTo: &a.logEdit, ReadOnly: true, VScroll: true, HScroll: true, MaxLength: 5000000}}}}},
					// 对齐 mac 的固定底座高度（app.m 中 _sendView 恒为 190px），六行压到三行 + 一行状态提示。
					Composite{Layout: VBox{MarginsZero: true}, MinSize: Size{Height: 190}, Children: []Widget{
						// 行 1：发送格式与目标。行尾只有四个短选项，必须限宽，否则会占掉 240px。
						Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{CheckBox{AssignTo: &a.hexSend, Text: "HEX", MaxSize: Size{Width: 58}, Checked: true}, Label{Text: "行尾"}, ComboBox{AssignTo: &a.eol, Model: []string{"无", "LF", "CR", "CRLF"}, CurrentIndex: 0, MinSize: Size{Width: 68}, MaxSize: Size{Width: 76}}, Label{Text: "目标"}, ComboBox{AssignTo: &a.sendTarget, Model: []string{"默认目标 / TCP 全部客户端"}, CurrentIndex: 0, StretchFactor: 1, MinSize: Size{Width: 200}}, LineEdit{AssignTo: &a.udpTarget, CueBanner: "UDP 目标 IP:端口", MinSize: Size{Width: 130}, MaxSize: Size{Width: 150}}, CheckBox{AssignTo: &a.clearAfterSend, Text: "发送后清空", MaxSize: Size{Width: 96}}}},
						// 行 2：报文输入与发送，验证按钮从拥挤的行 3 移到这里。
						Composite{Layout: HBox{MarginsZero: true}, StretchFactor: 1, Children: []Widget{TextEdit{AssignTo: &a.sendEdit, StretchFactor: 1, VScroll: true, Font: Font{Family: "Consolas", PointSize: 10}}, Composite{Layout: VBox{MarginsZero: true}, MaxSize: Size{Width: 95}, Children: []Widget{PushButton{Text: "发送 (F5)", Image: uiIcon("send"), MinSize: Size{Height: 46}, OnClicked: func() { a.sendOnce(false) }}, PushButton{Text: "验证", Image: uiIcon("check"), OnClicked: a.validateSend}}}}},
						// 行 3：历史、快捷与定时/循环。两个下拉框拉伸，保证能显示完整报文。
						Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{ComboBox{AssignTo: &a.sendHistory, Editable: true, StretchFactor: 1, MinSize: Size{Width: 170}, OnCurrentIndexChanged: a.onSendHistorySelected}, ComboBox{AssignTo: &a.favorites, Editable: true, StretchFactor: 1, MinSize: Size{Width: 170}, OnCurrentIndexChanged: a.onFavoriteSelected}, PushButton{Text: "保存快捷", Image: uiIcon("save"), MaxSize: Size{Width: 96}, OnClicked: a.saveFavorite}, PushButton{Text: "删除", MaxSize: Size{Width: 58}, OnClicked: a.deleteFavorite}, Label{Text: "间隔 ms"}, LineEdit{AssignTo: &a.interval, Text: "1000", MinSize: Size{Width: 58}, MaxSize: Size{Width: 65}}, PushButton{AssignTo: &a.timerButton, Text: "开始定时", MaxSize: Size{Width: 88}, OnClicked: a.toggleTimer}, LineEdit{AssignTo: &a.loopCount, Text: "0", CueBanner: "次数", MinSize: Size{Width: 42}, MaxSize: Size{Width: 48}, ToolTipText: "循环次数，0 表示持续发送"}, PushButton{AssignTo: &a.loopButton, Text: "循环发送", MaxSize: Size{Width: 88}, OnClicked: a.toggleLoopSend}}},
						Label{AssignTo: &a.sendPreview, Text: "HEX 未选中时按文本发送 · 快捷框输入名称后保存", Font: Font{Family: "Microsoft YaHei UI", PointSize: 8}},
					}},
				}},
			}},
			a.assistant.widget(),
		}},
		Label{AssignTo: &a.footer, Text: "未连接  |  RX 0 B  |  TX 0 B", MinSize: Size{Height: 24}, TextColor: walk.RGB(42, 70, 100)},
	}}).Create(); err != nil {
		return err
	}
	// 对齐 mac：宽度不足 1280 时自动收起 AI 面板，保证中栏始终够宽承载数据表。
	a.mw.SizeChanged().Attach(a.enforceAssistantWidth)
	a.updateSelectionLabel()
	return nil
}

// enforceAssistantWidth 在窗口过窄时收起 AI 面板，等价于 app.m layoutMainPanes 里的
// if (width < 1280) _analysisVisible = NO。
func (a *application) enforceAssistantWidth() {
	if a.mw == nil || a.assistant == nil || a.assistant.panel == nil {
		return
	}
	if a.mw.ClientBoundsPixels().Width < 1280 && a.assistant.panel.Visible() {
		a.assistant.stop()
		a.assistant.panel.SetVisible(false)
	}
}

// toggleDetailColumns 切换协议 / 来源 / 连接 ID 三列，默认隐藏以避免横向滚动。
func (a *application) toggleDetailColumns() {
	if a.packetTable == nil || a.detailColumns == nil {
		return
	}
	show := a.detailColumns.Checked()
	for _, i := range []int{5, 6, 7} {
		a.packetTable.Columns().At(i).SetVisible(show)
	}
}

// updateSelectionLabel 显示当前选中条数，对应 mac 的 _selectionLabel。
func (a *application) updateSelectionLabel() {
	if a.selectionLabel == nil || a.packetTable == nil {
		return
	}
	n := len(a.packetTable.SelectedIndexes())
	if n == 0 {
		a.selectionLabel.SetText("未选中")
		return
	}
	a.selectionLabel.SetText(fmt.Sprintf("已选 %d 条", n))
}

func (a *application) selectAllPackets() {
	if a.packetTable == nil {
		return
	}
	all := make([]int, len(a.packetModel.visible))
	for i := range all {
		all[i] = i
	}
	a.packetTable.SetSelectedIndexes(all)
}

func (a *application) clearPacketSelection() {
	if a.packetTable != nil {
		a.packetTable.SetSelectedIndexes(nil)
	}
}

func (a *application) invertPacketSelection() {
	if a.packetTable == nil {
		return
	}
	selected := map[int]bool{}
	for _, i := range a.packetTable.SelectedIndexes() {
		selected[i] = true
	}
	var rest []int
	for i := range a.packetModel.visible {
		if !selected[i] {
			rest = append(rest, i)
		}
	}
	a.packetTable.SetSelectedIndexes(rest)
}

// analyzeSelected 把当前选中的报文交给 AI 面板分析，范围固定为“选中数据”。
func (a *application) analyzeSelected() {
	a.showAssistant()
	a.assistant.scope.SetCurrentIndex(0)
	a.assistant.analyze("全面诊断")
}

func (a *application) switchMode(index, role int) {
	if a.connected || a.connecting {
		a.sendPreview.SetText("请先断开当前连接再切换模式")
		return
	}
	i := []int{1, 1, 2, 0}[index]
	a.mode.SetCurrentIndex(i)
	a.role.SetCurrentIndex(role)
	a.updateAddressDefault()
	a.updateMode()
}
func (a *application) toggleAssistant() {
	if a.assistant.panel.Visible() {
		a.assistant.stop()
		a.assistant.panel.SetVisible(false)
	} else {
		a.showAssistant()
	}
}
func (a *application) showAssistant() { a.assistant.panel.SetVisible(true) }
func (a *application) updateDisplay() {
	if a.packetTable == nil || a.displayMode == nil || a.showTime == nil {
		return
	}
	a.packetTable.Columns().At(0).SetVisible(a.showTime.Checked())
	a.packetTable.Columns().At(2).SetVisible(a.displayMode.CurrentIndex() != 2)
	a.packetTable.Columns().At(3).SetVisible(a.displayMode.CurrentIndex() != 1)
}
func (a *application) loadPacket() {
	i := a.packetTable.CurrentIndex()
	if i >= 0 && i < len(a.packetModel.visible) {
		a.sendEdit.SetText(a.packetModel.visible[i].Hex)
		a.hexSend.SetChecked(true)
		a.sendPreview.SetText("已载入报文；快捷框输入名称后点击保存快捷")
	}
}
func (a *application) showPeerDetails() {
	if a.detailsLabel == nil {
		return
	}
	p, ok := a.selectedPeer()
	if !ok {
		return
	}
	b := a.peerBaseline[p.ID]
	a.detailsLabel.SetText(fmt.Sprintf("本地  %s\r\n远程  %s\r\nRX %s  /  TX %s\r\n时长  %s", p.LocalAddress, p.RemoteAddress, wincore.FormatBytes(p.RXBytes-b.RXBytes), wincore.FormatBytes(p.TXBytes-b.TXBytes), wincore.FormatDuration(time.Since(p.ConnectedAt))))
}
func (a *application) exportCSV() {
	fd := walk.FileDialog{Title: "导出当前可见数据", Filter: "CSV (*.csv)|*.csv", FilePath: "commbox-" + time.Now().Format("20060102-150405") + ".csv"}
	ok, err := fd.ShowSave(a.mw)
	if err != nil {
		a.showError(err)
		return
	}
	if !ok {
		return
	}
	var out strings.Builder
	out.WriteString("\xef\xbb\xbf")
	w := csv.NewWriter(&out)
	w.Write([]string{"时间", "方向", "协议", "连接ID", "来源", "HEX", "ASCII", "长度"})
	for _, p := range a.packetModel.visible {
		row := []string{p.TS.Format(time.RFC3339Nano), p.Direction, p.Raw.Transport, p.Raw.ConnectionID, p.Raw.Endpoint, p.Hex, p.ASCII, fmt.Sprint(p.Length)}
		for i, s := range row {
			if len(s) > 0 && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
				row[i] = "'" + s
			}
		}
		w.Write(row)
	}
	w.Flush()
	if err = os.WriteFile(fd.FilePath, []byte(out.String()), 0600); err != nil {
		a.showError(err)
	}
}
func (a *application) showHelp() {
	walk.MsgBox(a.mw, "CommBox 使用说明", "选择左侧工作模式 → 填写参数 → 连接 → 输入报文 → F5 发送。\r\n\r\n客户端列表右键可定向发送、过滤和断开。同 IP 的不同端口按独立会话管理。\r\n未勾选 HEX 时按文本发送。定时 / 循环固定使用启动时的数据和目标。\r\n数据保留最新 10000 条，完整历史自动保存于本地。拖动数据与发送区分隔线调整空间。\r\n\r\nAI 默认关闭。设置服务后，主动分析或追问才提交所选数据。AI 失败不影响通信。\r\n虚拟串口需要 com0com；驱动功能仍待实机验证。", walk.MsgBoxOK)
}
