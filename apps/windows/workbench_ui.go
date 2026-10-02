//go:build windows

package main

import (
	"encoding/csv"
	"fmt"
	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
	"os"
	"reflect"
	"serial-tool/core"
	"slices"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// 三栏骨架：左栏连接配置（定宽可滚动）、中栏数据与发送（撑满剩余宽度）、
// 右栏 AI 面板（默认隐藏）。底部为一行状态栏。
func (a *application) createWindow() error {
	a.peerBaseline = map[string]core.ConnectionInfo{}
	a.views = loadViewSettings(a.engine)
	a.assistant = &assistantPanel{app: a}
	if err := (MainWindow{
		AssignTo: &a.mw,
		Title:    "CommBox v" + core.Version + " · Windows",
		Size:     Size{Width: 1280, Height: 820},
		// 最小尺寸由内容决定（数据表至少 3 行、发送框至少 3 行），这里只兜底，
		// 要低于窄屏单栏时的内容下限：800x600 屏幕的工作区只有 800x552。
		MinSize:    Size{Width: 640, Height: 400},
		Font:       Font{Family: fontUI, PointSize: sizeBody},
		Background: SolidColorBrush{Color: colorCanvas},
		Layout:     VBox{Alignment: AlignHNearVNear, Margins: Margins{Left: 10, Top: 6, Right: 10, Bottom: 4}, Spacing: 6},
		MenuItems:  a.menus(),
		Children: []Widget{
			Composite{AssignTo: &a.appHeader, Layout: HBox{MarginsZero: true, Spacing: 8}, Children: []Widget{
				Label{Text: "CommBox", Font: fontSection, TextColor: colorBlue}, HSpacer{},
				PushButton{Text: "AI 助手", MinSize: Size{Width: 84, Height: btnH}, OnClicked: a.toggleAssistant},
				PushButton{Text: "视图设置", MinSize: Size{Width: 84, Height: btnH}, OnClicked: a.showViewSettings},
				PushButton{Text: "使用手册", MinSize: Size{Width: 84, Height: btnH}, OnClicked: a.showHelp},
			}},
			Composite{Layout: HBox{Alignment: AlignHNearVCenter, MarginsZero: true, Spacing: 10}, StretchFactor: 1, Children: []Widget{
				a.connectionPanel(),
				a.monitorPanel(),
				a.assistant.widget(),
			}},
			// 状态栏文字随连接状态变长。EllipsisMode 让这一行可压缩：
			// 不加时它的文字宽度会顶住整窗最小宽度，运行中还会跟着数字一起变。
			Composite{Background: SolidColorBrush{Color: colorPanel}, Layout: HBox{Alignment: AlignHNearVCenter, Margins: Margins{Left: 8, Right: 8}, Spacing: 6}, Children: []Widget{
				// 只有 Ideal 模式按图标本身定尺寸，其他模式会抢走整行乃至整窗的富余高度。
				ImageView{AssignTo: &a.footerIcon, Mode: ImageViewModeIdeal, Background: SolidColorBrush{Color: colorPanel}, MinSize: Size{Width: 16, Height: 16}, MaxSize: Size{Width: 16, Height: 16}},
				Label{AssignTo: &a.footer, Text: "未连接  |  RX 0 B  |  TX 0 B", MinSize: Size{Height: 20}, Font: Font{Family: fontMono, PointSize: sizeMono}, TextColor: colorMuted, EllipsisMode: EllipsisEnd, Alignment: AlignHNearVCenter},
				// 两个控件都不吃富余宽度，没有弹簧时它会摊成图标与文字之间的空档。
				HSpacer{},
			}},
		},
	}).Create(); err != nil {
		return err
	}
	// 按逻辑像素切换窄屏布局，展开助手不会把窗口撑出工作区。
	a.mw.SizeChanged().Attach(func() { a.arrangePanes(a.assistant.panel.Visible()) })
	// DPI 变化时 walk 按新字体重算选项宽度，随后窗口改尺寸，在这里再压回去。
	// 左栏三个下拉框的选项来自本机与历史，同样不能决定左栏宽度。
	compactCombos := []*walk.ComboBox{a.sendHistory, a.favorites, a.recentConn, a.ports, a.netIP}
	a.mw.SizeChanged().Attach(func() {
		for _, cb := range compactCombos {
			keepComboCompact(cb)
		}
	})
	keepComboCompact(a.netIP) // 本机地址在创建时就填进去了。
	a.packetTable.SizeChanged().Attach(a.fitPacketColumns)
	a.dataSendPane.SizeChanged().Attach(a.balanceSendArea)
	if a.autoUpdateAction != nil {
		_ = a.autoUpdateAction.SetChecked(a.autoUpdateEnabled())
	}
	a.updateSelectionLabel()
	a.updateFooterIcon()
	a.initViewSwitch()
	for _, button := range []*walk.PushButton{a.connectButton, a.sendButton, a.assistant.chat.button} {
		stylePrimaryButton(button)
	}
	a.applyViews()
	a.workArea = func() (win.RECT, bool) { return workAreaFor(a.mw.Handle()) }
	a.watchWorkArea()
	a.fitToWorkArea()
	a.mw.Disposing().Attach(func() {
		a.closed.Store(true)
		a.shutdownVirtualCOM()
	})
	return nil
}

func (a *application) menus() []MenuItem {
	return []MenuItem{
		Menu{Text: "文件", Items: []MenuItem{Action{Text: "新建实例", Image: uiIcon("new"), OnTriggered: a.newInstance}, Action{Text: "保存 TXT", Image: uiIcon("save"), OnTriggered: func() { a.exportText(a.packetModel.exportText(), "commbox", a.mw) }}, Action{Text: "导出 CSV", Image: uiIcon("save"), OnTriggered: a.exportCSV}, Action{Text: "退出", OnTriggered: func() { a.mw.Close() }}}},
		Menu{Text: "查看", Items: []MenuItem{Action{Text: "视图设置…", Image: uiIcon("settings"), OnTriggered: a.showViewSettings}, Separator{}, Action{Text: "AI 助手", Image: uiIcon("ai"), OnTriggered: a.toggleAssistant}, Action{Text: "实时监控窗口", Image: uiIcon("client"), OnTriggered: a.openMonitor}}},
		Menu{Text: "工具", Items: []MenuItem{Action{Text: "HTTP 工作台", Image: uiIcon("http"), OnTriggered: a.openHTTPWorkspace}, Action{Text: "校验与转换", Image: uiIcon("tool"), OnTriggered: a.openToolbox}, Action{Text: "连接管理", Image: uiIcon("settings"), OnTriggered: a.openConnections}, Action{Text: "串口服务器", OnTriggered: func() {
			if !a.connected && !a.connecting {
				a.mode.SetCurrentIndex(3)
				a.updateMode()
			}
		}}, Action{Text: "虚拟串口管理", Image: uiIcon("virtualcom"), OnTriggered: a.openVirtualCOM}, Action{Text: "VirtualCOM 连接说明", OnTriggered: a.showVirtualCOMHelp}, Action{Text: "历史数据分析", Image: uiIcon("history"), OnTriggered: a.openDatabaseAnalysis}}},
		Menu{Text: "设置", Items: []MenuItem{Action{Text: "AI 设置", OnTriggered: a.assistant.settings}, Action{Text: "连接数与桥接", OnTriggered: a.openConnections}, Separator{}, Action{AssignTo: &a.autoUpdateAction, Text: "启动时检查更新", Checkable: true, OnTriggered: a.toggleAutoUpdate}}},
		Menu{Text: "帮助", Items: []MenuItem{Action{Text: "完整使用手册", Image: uiIcon("help"), Shortcut: Shortcut{Key: walk.KeyF1}, OnTriggered: a.showHelp}, Action{Text: "检查更新", OnTriggered: a.checkUpdate}, Action{Text: "发送 (F5)", Image: uiIcon("send"), Shortcut: Shortcut{Key: walk.KeyF5}, OnTriggered: func() { a.sendOnce(false) }}}},
	}
}

// connectionPanel 是左栏：模式、参数、连接状态与对端列表。
// 定宽并可纵向滚动，窗口再矮也不会把参数挤成一团。
func (a *application) connectionPanel() Widget {
	return ScrollView{AssignTo: &a.connectionPane, HorizontalFixed: true, MinSize: Size{Width: connectionPaneMinWidth}, MaxSize: Size{Width: connectionPaneMaxWidth}, Background: SolidColorBrush{Color: colorPanel},
		Layout: VBox{Alignment: AlignHNearVNear, Margins: Margins{Left: 12, Top: 12, Right: 12, Bottom: 12}, Spacing: 10}, Children: []Widget{
			Composite{Layout: HBox{MarginsZero: true, Spacing: 6}, Children: []Widget{
				Label{Text: "连接配置", Font: fontSection, TextColor: colorBlue}, HSpacer{},
				PushButton{AssignTo: &a.dataPageButton, Text: "返回视图", Visible: false, MinSize: Size{Width: 84, Height: btnH}, MaxSize: Size{Width: 84}, OnClicked: a.showDataPage},
			}},
			fieldLabel("工作模式"), ComboBox{AssignTo: &a.mode, Model: modes, CurrentIndex: 1, MinSize: Size{Height: rowH}, OnCurrentIndexChanged: a.updateMode},
			GroupBox{AssignTo: &a.serialGroup, Title: "串口参数", Layout: VBox{Margins: Margins{Left: 8, Top: 6, Right: 8, Bottom: 8}, Spacing: 6}, Children: []Widget{
				fieldLabel("串口"), Composite{Layout: HBox{MarginsZero: true, Spacing: 6}, Children: []Widget{
					ComboBox{AssignTo: &a.ports, Editable: true, StretchFactor: 1, MinSize: Size{Width: 80, Height: rowH}, ToolTipText: "支持物理串口及 VirtualCOM 免驱动端口。免驱动端口只传输字节，串口参数不生效。"},
					PushButton{Text: "刷新", MinSize: Size{Width: 52, Height: btnH}, MaxSize: Size{Width: 52}, OnClicked: a.refreshPorts},
				}},
				fieldLabel("波特率"), Composite{Layout: HBox{MarginsZero: true, Spacing: 6}, Children: []Widget{
					ComboBox{AssignTo: &a.baud, Editable: true, Model: []string{"1200", "2400", "4800", "9600", "19200", "38400", "57600", "115200", "230400", "460800", "921600"}, CurrentIndex: 7, StretchFactor: 1, MinSize: Size{Width: 90, Height: rowH}}, inlineLabel("bps", 28),
				}},
				Composite{Layout: Grid{Columns: 2, MarginsZero: true, Spacing: 6}, Children: []Widget{
					fieldLabel("数据位"), fieldLabel("校验位"),
					ComboBox{AssignTo: &a.data, Model: []string{"5", "6", "7", "8"}, CurrentIndex: 3, MinSize: Size{Width: 60, Height: rowH}},
					ComboBox{AssignTo: &a.parity, Model: []string{"无校验", "奇校验", "偶校验"}, CurrentIndex: 0, MinSize: Size{Width: 80, Height: rowH}},
					fieldLabel("停止位"), HSpacer{},
					ComboBox{AssignTo: &a.stop, Model: []string{"1", "2"}, CurrentIndex: 0, MinSize: Size{Height: rowH}}, HSpacer{},
				}},
			}},
			GroupBox{AssignTo: &a.networkGroup, Title: "网络参数", Layout: VBox{Margins: Margins{Left: 8, Top: 6, Right: 8, Bottom: 8}, Spacing: 6}, Children: []Widget{
				fieldLabelTo("网络协议", &a.protocolLabel), ComboBox{AssignTo: &a.protocol, Model: []string{"TCP", "UDP"}, CurrentIndex: 0, Visible: false, MinSize: Size{Height: rowH}, OnCurrentIndexChanged: a.updateConnectionHints},
				fieldLabelTo("连接角色", &a.roleLabel), ComboBox{AssignTo: &a.role, Model: []string{"服务端", "客户端"}, CurrentIndex: 1, MinSize: Size{Height: rowH}, OnCurrentIndexChanged: a.updateMode},
				fieldLabelTo("目标地址", &a.addressLabel), ComboBox{AssignTo: &a.netIP, Editable: true, Model: append([]string{"127.0.0.1", "0.0.0.0"}, core.LocalIPs()...), CurrentIndex: 0, MinSize: Size{Width: 120, Height: rowH}},
				fieldLabelTo("端口", &a.portLabel), LineEdit{AssignTo: &a.netPort, Text: "9000", ToolTipText: "端口范围 1–65535", MinSize: Size{Height: rowH}},
				PushButton{AssignTo: &a.reconnectToggle, Text: "重连设置", OnClicked: func() {
					a.reconnectExpanded = !a.reconnectExpanded
					a.updateConnectionHints()
				}},
				Composite{AssignTo: &a.reconnectPane, Visible: false, Layout: VBox{MarginsZero: true, Spacing: 6}, Children: []Widget{
					CheckBox{AssignTo: &a.autoReconnect, Text: "断线自动重连", Checked: true},
					Composite{Layout: HBox{MarginsZero: true, Spacing: 6}, Children: []Widget{fieldLabel("重试间隔"), LineEdit{AssignTo: &a.reconnectInterval, Text: "2", MinSize: Size{Width: 40, Height: rowH}, MaxSize: Size{Width: 64}}, inlineLabel("s", 16), HSpacer{}}},
				}},
			}},
			Composite{Layout: HBox{MarginsZero: true, Spacing: 6}, Children: []Widget{
				Label{AssignTo: &a.statusDot, Text: "●", TextColor: colorGray, MinSize: Size{Width: 16}, MaxSize: Size{Width: 16}},
				Label{AssignTo: &a.status, Text: "未连接", EllipsisMode: EllipsisEnd, StretchFactor: 1}, HSpacer{},
			}},
			Composite{Layout: VBox{MarginsZero: true}, Children: []Widget{
				PushButton{AssignTo: &a.connectButton, Text: "连接", Font: Font{Family: fontUI, PointSize: sizeBody, Bold: true}, MinSize: Size{Height: 36}, OnClicked: a.toggleConnection},
			}},
			Label{AssignTo: &a.connectionHint, Text: "参数应与设备说明一致。", TextColor: colorMuted, MinSize: Size{Height: 48}, MaxSize: Size{Width: 210, Height: 48}, EllipsisMode: EllipsisEnd},
			fieldLabel("最近使用的连接"), ComboBox{AssignTo: &a.recentConn, ToolTipText: "选择后回填参数；请确认目标再连接", MinSize: Size{Width: 120, Height: rowH}, OnCurrentIndexChanged: a.onRecentConnSelected},
			Label{AssignTo: &a.peerTitle, Text: "客户端 / 对端 (0)", Font: Font{Family: fontUI, PointSize: sizeBody, Bold: true}, EllipsisMode: EllipsisEnd},
			ListBox{AssignTo: &a.peerList, Model: []string{}, MinSize: Size{Height: 96}, OnCurrentIndexChanged: a.showPeerDetails, OnItemActivated: func() { a.peerAction("send") }, ContextMenuItems: []MenuItem{
				Action{Text: "发送数据", OnTriggered: func() { a.peerAction("send") }}, Action{Text: "仅查看该客户端", OnTriggered: func() { a.peerAction("filter") }}, Action{Text: "断开连接", OnTriggered: func() { a.peerAction("disconnect") }}, Action{Text: "复制地址", OnTriggered: func() { a.peerAction("copy") }}, Action{Text: "清空显示统计", OnTriggered: func() { a.peerAction("reset"); a.showPeerDetails() }},
			}},
			Label{AssignTo: &a.detailsLabel, Text: "选择对端查看地址与收发统计", TextColor: colorMuted, MinSize: Size{Height: 86}, MaxSize: Size{Width: 210, Height: 86}},
		}}
}

// Labels sit above fields; units stay beside numeric values.
func fieldLabel(text string) Label { return Label{Text: text, TextColor: colorMuted} }
func fieldLabelTo(text string, to **walk.Label) Label {
	l := fieldLabel(text)
	l.AssignTo = to
	return l
}

// monitorPanel 是中栏：标题与筛选各占一行，其余空间分给数据与发送。
func (a *application) monitorPanel() Widget {
	return Composite{AssignTo: &a.monitorPane, StretchFactor: stretchFill, MinSize: Size{Width: minMonitorWidth}, Background: SolidColorBrush{Color: colorPanel}, Layout: VBox{Alignment: AlignHNearVNear, Margins: Margins{Left: 8, Top: 4, Right: 8, Bottom: 4}, Spacing: 3}, Children: []Widget{
		// 标题行只保留视图切换和常用操作，计数靠近筛选条件。
		Composite{Layout: HBox{Alignment: AlignHNearVCenter, MarginsZero: true, Spacing: 8}, Children: []Widget{
			Label{Text: "数据监控", Font: fontSection, TextColor: colorBlue, Alignment: AlignHNearVCenter, MinSize: Size{Width: 92}, MaxSize: Size{Width: 92}},
			// 数据 / 日志切换原是标签页，标签条单占约 30px 高；并进标题行后，
			// 矮屏（工作区约 1024x552）上这一行高度留给数据表。
			Composite{Layout: HBox{Alignment: AlignHNearVCenter, MarginsZero: true}, Children: []Widget{
				RadioButton{AssignTo: &a.viewData, Text: "数据", MinSize: Size{Width: 56}, MaxSize: Size{Width: 56}, OnClicked: func() { a.showLogView(false) }},
				RadioButton{AssignTo: &a.viewLog, Text: "日志", MinSize: Size{Width: 56}, MaxSize: Size{Width: 56}, OnClicked: func() { a.showLogView(true) }},
			}},
			HSpacer{StretchFactor: stretchFill},
			// 只在窄屏单栏时显示：左栏收起后从这里打开连接配置。
			PushButton{AssignTo: &a.connectionPageButton, Text: "连接配置", Visible: false, MinSize: Size{Width: 88, Height: btnH}, MaxSize: Size{Width: 88}, OnClicked: a.showConnectionPage},
			toolButton("清空", "clear", 80, func() { a.packetModel.clear(); a.updatePacketStats(); a.updateSelectionLabel() }),
			toolButton("导出", "save", 80, a.exportCSV),
		}},
		// 常用筛选只占一行，进阶条件按需展开，把高度留给报文。
		Composite{Layout: HBox{Alignment: AlignHNearVCenter, MarginsZero: true, Spacing: 6}, Children: []Widget{
			LineEdit{AssignTo: &a.searchEdit, CueBanner: "搜索 HEX / ASCII / 文本", StretchFactor: stretchFill, MinSize: Size{Width: 100, Height: rowH}, OnTextChanged: a.applyFilter},
			fixedCombo(&a.dirFilter, []string{dirAll, "RX", "TX", "EVENT"}, 96, a.applyFilter),
			PushButton{AssignTo: &a.filterToggle, Text: "更多筛选", MinSize: Size{Width: 84, Height: btnH}, MaxSize: Size{Width: 84}, OnClicked: a.toggleAdvancedFilters},
			PushButton{Text: "重置筛选", MinSize: Size{Width: 80, Height: btnH}, MaxSize: Size{Width: 80}, OnClicked: a.clearFilter},
			Label{AssignTo: &a.statsLabel, Text: "显示 0 / 0 条", TextColor: colorMuted, EllipsisMode: EllipsisEnd, Alignment: AlignHFarVCenter, MinSize: Size{Width: 100}, MaxSize: Size{Width: 140}},
		}},
		Composite{AssignTo: &a.advancedFilters, Visible: false, Layout: HBox{MarginsZero: true, Spacing: 6}, Children: []Widget{
			fixedCombo(&a.displayMode, []string{"HEX + ASCII", "HEX", "ASCII"}, 120, a.updateDisplay),
			LineEdit{AssignTo: &a.connectionFilter, CueBanner: "连接 ID / 来源地址", StretchFactor: stretchFill, MinSize: Size{Width: 150, Height: rowH}, OnTextChanged: a.applyFilter},
			fixedCombo(&a.protocolFilter, []string{"全部协议", "SERIAL", "TCP", "UDP", "HTTP"}, 98, a.applyFilter),
			fixedCombo(&a.timeFilter, []string{"全部时间", "1分钟", "5分钟", "30分钟"}, 96, a.applyFilter),
		}},
		// 不用分隔条：拖动会让发送区超过 30%。两者的实际高度由 balanceSendArea 维护。
		Composite{AssignTo: &a.dataSendPane, StretchFactor: 1, Layout: VBox{Alignment: AlignHNearVNear, MarginsZero: true, Spacing: dataSendSpacing}, Children: []Widget{a.packetViews(), a.sendArea()}},
	}}
}

// packetViews 是数据表（外加一行选中操作）与日志两个视图，由标题行的
// 数据 / 日志按钮切换，同一时间只显示一个。
func (a *application) packetViews() Widget {
	analyze := toolButton("分析选中", "ai", 128, a.analyzeSelected)
	analyze.AssignTo = &a.analyzeSelectionButton
	analyze.Enabled = false
	return Composite{AssignTo: &a.dataPane, StretchFactor: 7, Layout: VBox{Alignment: AlignHNearVNear, MarginsZero: true}, Children: []Widget{
		// 协议 / 来源 / 连接 ID 默认隐藏：八列合计 895px，而 AI 面板展开时中栏仅约 700px，
		// 全显必然出横向滚动条。需要时通过右键菜单打开。
		Composite{AssignTo: &a.dataView, Layout: VBox{Alignment: AlignHNearVNear, MarginsZero: true, Spacing: 4}, Children: []Widget{
			TableView{AssignTo: &a.packetTable, Model: a.packetModel, MinSize: Size{Height: minTableHeight}, MultiSelection: true, AlternatingRowBG: true, Font: Font{Family: fontMono, PointSize: sizeData}, StyleCell: a.stylePacketCell, OnSelectedIndexesChanged: a.updateSelectionLabel, Columns: []TableViewColumn{{Title: "时间", Width: 128}, {Title: "方向", Width: 52}, {Title: "HEX", Width: 248}, {Title: "ASCII", Width: 104}, {Title: "长度", Width: 64}, {Title: "协议", Width: 72, Hidden: true}, {Title: "来源", Width: 140, Hidden: true}, {Title: "连接 ID", Width: 170, Hidden: true}}, ContextMenuItems: []MenuItem{
				Action{Text: "全选", OnTriggered: a.selectAllPackets}, Action{Text: "取消选择", OnTriggered: a.clearPacketSelection}, Action{Text: "反选", OnTriggered: a.invertPacketSelection}, Separator{}, Action{Text: "复制 HEX", OnTriggered: func() { a.copyPacketField("hex") }}, Action{Text: "复制 ASCII", OnTriggered: func() { a.copyPacketField("ascii") }}, Action{Text: "复制整行", OnTriggered: func() { a.copyPacketField("all") }}, Action{Text: "重新发送", OnTriggered: func() {
					if a.loadPacket() {
						a.sendOnce(false)
					}
				}}, Action{Text: "添加到快捷发送", OnTriggered: func() { a.loadPacket() }}, Action{Text: "AI 分析选中数据", OnTriggered: a.analyzeSelected}, Separator{}, Action{AssignTo: &a.detailColumns, Text: "显示协议 / 来源 / 连接 ID 列", Checkable: true, OnTriggered: a.toggleDetailColumns}, Action{Text: "导出 CSV", Image: uiIcon("save"), OnTriggered: a.exportCSV},
			}},
			// 选中操作行：给 AI 面板的“当前选中数据”范围提供选择入口和计数。
			// 每个控件自带宽度上限，HBox 只能把富余宽度给 HSpacer，
			// 窗口收窄时按钮也就不会互相挤到文字叠在一起。
			Composite{Layout: HBox{Alignment: AlignHNearVCenter, MarginsZero: true, Spacing: 6}, Children: []Widget{
				Label{AssignTo: &a.selectionLabel, Text: "未选中", MinSize: Size{Width: 66}, MaxSize: Size{Width: 94}, EllipsisMode: EllipsisEnd, Alignment: AlignHNearVCenter},
				analyze,
				HSpacer{StretchFactor: stretchFill},
				CheckBox{AssignTo: &a.autoScroll, Text: "自动滚动", Checked: true, MinSize: Size{Width: 102}, MaxSize: Size{Width: 102}},
				CheckBox{AssignTo: &a.showTime, Text: "时间", Checked: true, MinSize: Size{Width: 72}, MaxSize: Size{Width: 72}, OnCheckedChanged: a.updateDisplay},
			}},
		}},
		TextEdit{AssignTo: &a.logEdit, Visible: false, ReadOnly: true, Background: SolidColorBrush{Color: colorPanel}, VScroll: true, HScroll: true, MaxLength: 5000000, Font: Font{Family: fontMono, PointSize: sizeData}},
	}}
}

// initViewSwitch 把数据 / 日志两个单选钮改成按下式外观，看起来是一组切换按钮。
// 声明式 RadioButton 不能直接带 BS_PUSHLIKE，只能创建后补上。
func (a *application) initViewSwitch() {
	for _, rb := range []*walk.RadioButton{a.viewData, a.viewLog} {
		h := rb.Handle()
		win.SetWindowLong(h, win.GWL_STYLE, win.GetWindowLong(h, win.GWL_STYLE)|win.BS_PUSHLIKE)
		win.InvalidateRect(h, nil, true)
	}
	a.viewData.SetChecked(true)
	a.mw.RequestLayout()
}

func (a *application) showLogView(log bool) {
	if a.dataView == nil || a.logEdit == nil {
		return
	}
	if !a.views.Data && a.views.Log {
		log = true
	}
	if !a.views.Log {
		log = false
	}
	a.viewData.SetChecked(!log)
	a.viewLog.SetChecked(log)
	a.dataView.SetVisible(!log && a.views.Data)
	a.logEdit.SetVisible(log && a.views.Log)
	a.balanceSendArea() // 两个视图的最小高度不同。
}

// 矮屏上数据表与发送区的保底高度（96 DPI 逻辑像素，随缩放一起放大）。
// 数据表：表头约 26 + 3 行 × 约 24 + 边框；发送框：3 行 12pt 等宽字 + 内边距，
// 并留出 125%/150% 下字高取整多出的一两个像素。
const (
	minTableHeight    = 102
	minSendEditHeight = 66
)

// 常规窗口使用三栏；空间不足时收起侧栏，窄屏按分区切换。
const (
	dataSharePercent       = 55
	dataSendSpacing        = 4
	minMonitorWidth        = 600
	bothSidePanesWidth     = 1200
	connectionPaneMinWidth = 240
	connectionPaneMaxWidth = 250
	assistantMinWidth      = 280
	assistantMaxWidth      = 300
	wideLayoutWidth        = 1000 // 左栏 + 中栏的窗口外框约 992
)

// 发送区按格式与目标、报文、历史与快捷、定时与循环分行。放不下完整发送区时，
// 历史与定时操作收进「更多发送」，结果提示始终可见。
// UDP 地址与目标并排，避免顶高小屏窗口。
func (a *application) sendArea() Widget {
	return Composite{AssignTo: &a.sendPane, Background: SolidColorBrush{Color: colorCanvas}, Layout: VBox{Margins: Margins{Left: 10, Top: 8, Right: 10, Bottom: 8}, Spacing: 6}, Children: []Widget{
		Composite{Layout: HBox{MarginsZero: true, Spacing: 6}, Children: []Widget{
			Label{Text: "发送数据", Font: fontSection, TextColor: colorBlue}, HSpacer{},
			PushButton{AssignTo: &a.sendExtrasToggle, Text: "更多发送", MinSize: Size{Width: 84, Height: btnH}, MaxSize: Size{Width: 84}, ToolTipText: "历史、快捷、定时与循环；收起不会停止任务", OnClicked: a.toggleSendExtras},
		}},
		Composite{Layout: HBox{MarginsZero: true, Spacing: 6}, Children: []Widget{
			CheckBox{AssignTo: &a.hexSend, Text: "HEX 格式", Checked: true, MinSize: Size{Width: 88}, MaxSize: Size{Width: 88}, ToolTipText: "勾选按十六进制字节发送；取消后按文本发送。"},
			inlineLabel("行尾", 32), ComboBox{AssignTo: &a.eol, Model: []string{"无", "LF", "CR", "CRLF"}, CurrentIndex: 0, MinSize: Size{Width: 70, Height: rowH}, MaxSize: Size{Width: 70}},
			inlineLabel("发送目标", 62), ComboBox{AssignTo: &a.sendTarget, Model: []string{"默认目标"}, CurrentIndex: 0, StretchFactor: stretchFill, MinSize: Size{Width: 100, Height: rowH}, ToolTipText: "默认目标：TCP 服务端广播，UDP 服务端回复最近对端；其他模式发送到当前连接。"},
			LineEdit{AssignTo: &a.udpTarget, Visible: false, CueBanner: "IP:端口（可选）", ToolTipText: "仅默认目标下生效，留空使用当前连接或最近对端。", MinSize: Size{Width: 112, Height: rowH}, MaxSize: Size{Width: 150}},
		}},
		Composite{Layout: HBox{MarginsZero: true, Spacing: 8}, StretchFactor: 1, Children: []Widget{
			TextEdit{AssignTo: &a.sendEdit, Background: SolidColorBrush{Color: colorPanel}, StretchFactor: 1, VScroll: true, MinSize: Size{Height: minSendEditHeight}, ToolTipText: "例如 01 03 00 00；取消 HEX 后输入文本。F5 发送。", Font: Font{Family: fontMono, PointSize: sizeData}},
			Composite{Layout: VBox{MarginsZero: true, Spacing: 4}, MinSize: Size{Width: 108}, MaxSize: Size{Width: 108}, Children: []Widget{
				PushButton{AssignTo: &a.sendButton, Text: "发送 (F5)", Font: Font{Family: fontUI, PointSize: sizeBody, Bold: true}, MinSize: Size{Height: 36}, MaxSize: Size{Height: 36}, OnClicked: func() { a.sendOnce(false) }},
				PushButton{Text: "验证格式", MinSize: Size{Height: btnH}, MaxSize: Size{Height: btnH}, OnClicked: a.validateSend},
			}},
		}},
		Composite{Layout: HBox{MarginsZero: true, Spacing: 8}, Children: []Widget{
			Label{AssignTo: &a.sendPreview, Text: "HEX 使用两位字节，空格分隔", TextColor: colorMuted, MinSize: Size{Width: 40}, EllipsisMode: EllipsisEnd}, HSpacer{},
			CheckBox{AssignTo: &a.clearAfterSend, Text: "发送后清空", ToolTipText: "成功发送后清空输入，不影响运行中的任务。", MinSize: Size{Width: 112}, MaxSize: Size{Width: 112}},
		}},
		Composite{AssignTo: &a.sendExtras, Layout: VBox{Alignment: AlignHNearVNear, MarginsZero: true, Spacing: 4}, Children: []Widget{
			Composite{Layout: HBox{Alignment: AlignHNearVCenter, MarginsZero: true, Spacing: 6}, Children: []Widget{
				inlineLabel("历史", 38),
				ComboBox{AssignTo: &a.sendHistory, Editable: true, StretchFactor: 1, MinSize: Size{Width: 96, Height: rowH}, OnCurrentIndexChanged: a.onSendHistorySelected},
				inlineLabel("快捷", 38),
				ComboBox{AssignTo: &a.favorites, Editable: true, StretchFactor: 1, MinSize: Size{Width: 96, Height: rowH}, OnCurrentIndexChanged: a.onFavoriteSelected},
				toolButton("保存", "save", 92, a.saveFavorite),
				toolButton("删除", "", 70, a.deleteFavorite),
			}},
			Composite{Layout: HBox{Alignment: AlignHNearVCenter, MarginsZero: true, Spacing: 6}, Children: []Widget{
				inlineLabel("间隔", 38),
				LineEdit{AssignTo: &a.interval, Text: "1000", ToolTipText: "最小 10 ms；按启动时的报文和目标重复发送，修改内容需停止后重启。", MinSize: Size{Width: 72, Height: rowH}, MaxSize: Size{Width: 72}},
				inlineLabel("ms", 24),
				PushButton{AssignTo: &a.timerButton, Text: "开始定时", MinSize: Size{Width: 96, Height: btnH}, MaxSize: Size{Width: 96}, OnClicked: a.toggleTimer},
				inlineLabel("次数", 38),
				LineEdit{AssignTo: &a.loopCount, Text: "0", MinSize: Size{Width: 62, Height: rowH}, MaxSize: Size{Width: 62}, ToolTipText: "循环次数，0 表示持续发送"},
				PushButton{AssignTo: &a.loopButton, Text: "循环发送", MinSize: Size{Width: 96, Height: btnH}, MaxSize: Size{Width: 96}, OnClicked: a.toggleLoopSend},
				HSpacer{},
			}},
		}},
	}}
}

// Send controls retain their minimum height; remaining space goes to the data table.
func (a *application) balanceSendArea() {
	if a.dataSendPane == nil || a.dataPane == nil || a.sendPane == nil || a.sendExtras == nil || a.balancingSend {
		return
	}
	if !a.views.Send || (!a.views.Data && !a.views.Log) {
		a.sendExtras.SetVisible(a.sendExtrasOpen)
		a.sendExtrasToggle.SetVisible(true)
		a.updateSendExtrasToggle()
		return
	}
	a.balancingSend = true
	defer func() { a.balancingSend = false }()
	dpi := a.dataSendPane.DPI()
	gap := walk.IntFrom96DPI(dataSendSpacing, dpi)
	total := a.dataSendPane.ClientBoundsPixels().Height - gap
	if total <= 0 {
		return
	}
	// 按物理像素分配，展开高级操作时优先保证控件高度。
	target := total * (100 - dataSharePercent) / 100
	extras := a.sendExtras.MinSizeHint().Height + walk.IntFrom96DPI(4, dpi)
	compact := a.sendPane.MinSizeHint().Height
	if a.sendExtras.Visible() {
		compact -= extras
	}

	open := a.sendExtrasOpen
	if a.sendExtras.Visible() != open {
		a.sendExtras.SetVisible(open)
	}
	if !a.sendExtrasToggle.Visible() {
		a.sendExtrasToggle.SetVisible(true)
	}
	a.updateSendExtrasToggle()

	sendMin := compact
	if open {
		sendMin += extras
	}
	send := max(target, sendMin)
	layout := a.dataSendPane.Layout().(*walk.BoxLayout)
	_ = layout.SetStretchFactor(a.sendPane, max(1, (send-sendMin)*1000))
	_ = layout.SetStretchFactor(a.dataPane, max(1, (total-send-a.dataPane.MinSizeHint().Height)*1000))
}

func (a *application) toggleSendExtras() {
	a.sendExtrasOpen = !a.sendExtras.Visible()
	a.balanceSendArea()
}

// updateSendExtrasToggle 在两行收起时提示定时 / 循环仍在进行，停止按钮就在展开后的行里。
func (a *application) updateSendExtrasToggle() {
	if a.sendExtrasToggle == nil || a.sendExtras == nil {
		return
	}
	a.timerMu.Lock()
	timing := a.timerCancel != nil
	a.timerMu.Unlock()
	a.loopMu.Lock()
	looping := a.loopCancel != nil
	a.loopMu.Unlock()
	text := "更多发送"
	switch {
	case a.sendExtras.Visible():
		text = "收起发送"
	case timing:
		text = "定时中"
	case looping:
		text = "循环中"
	}
	if a.sendExtrasToggle.Text() != text {
		a.sendExtrasToggle.SetText(text)
	}
}

// 状态栏首段是「模式 + 状态」，前面配同一模式的图标，扫一眼就能认出连接类型。
var modeIcons = map[core.Mode]string{
	core.ModeSerial:       "serial",
	core.ModeTCPClient:    "client",
	core.ModeTCPServer:    "server",
	core.ModeUDPClient:    "udp",
	core.ModeUDPServer:    "udp",
	core.ModeSerialServer: "bridge",
	core.ModeHTTPClient:   "http",
}

func (a *application) updateFooterIcon() {
	if a.footerIcon == nil || a.mode == nil || a.role == nil {
		return
	}
	mode := a.uiMode()
	if string(mode) == a.lastFooterIcon {
		return
	}
	if icon := smallIcon(modeIcons[mode]); icon != nil {
		a.lastFooterIcon = string(mode)
		_ = a.footerIcon.SetImage(icon)
		_ = a.footerIcon.SetToolTipText(string(mode))
	}
}

// 方向保留文字，并用颜色帮助快速区分收发；其他列保持系统的选择与焦点绘制。
func (a *application) stylePacketCell(style *walk.CellStyle) {
	if style.Col() != 1 || style.Row() < 0 || style.Row() >= len(a.packetModel.visible) {
		return
	}
	switch a.packetModel.visible[style.Row()].Direction {
	case "RX":
		style.TextColor = colorGreen
	case "TX":
		style.TextColor = colorBlue
	default:
		style.TextColor = colorMuted
	}
}

// formLabel 是栅格里的表单标签：与右侧输入框垂直居中，文字不贴框顶。
func formLabel(text string) Label {
	return Label{Text: text, Alignment: AlignHNearVCenter}
}

func formLabelTo(text string, to **walk.Label) Label {
	l := formLabel(text)
	l.AssignTo = to
	return l
}

// protocolFormLabel 与协议下拉框成对显隐，单独取出只为带上 Visible: false。
func (a *application) protocolFormLabel() Label {
	l := formLabelTo("协议", &a.protocolLabel)
	l.Visible = false
	return l
}

// inlineLabel 是行内标签：定宽，HBox 才不会把它拉伸到半行宽，
// 标签与紧随其后的控件之间也就不会空出一大段。
func inlineLabel(text string, width int) Label {
	return Label{Text: text, Alignment: AlignHNearVCenter, MinSize: Size{Width: width}, MaxSize: Size{Width: width}}
}

// fixedCombo 是定宽下拉框：HBox 按剩余空间均摊，与内容无关，
// 定宽后筛选行才会紧贴排布，而不是每个框各占六分之一宽度。
func fixedCombo(to **walk.ComboBox, model []string, width int, changed walk.EventHandler) ComboBox {
	return ComboBox{AssignTo: to, Model: model, CurrentIndex: 0, MinSize: Size{Width: width, Height: rowH}, MaxSize: Size{Width: width}, OnCurrentIndexChanged: changed}
}

// setDropDownItems 更新发送历史、快捷名称这类由用户输入决定长短的下拉选项。
// 换模型会立即触发布局并按新选项把窗口撑大，所以在整窗暂停布局时换完、压好再恢复。
// 定时发送同一条报文时历史不变，直接跳过，免得每次发送都整窗重排重绘。
func setDropDownItems(cb *walk.ComboBox, items []string) {
	if old, ok := cb.Model().([]string); ok && slices.Equal(old, items) {
		return
	}
	if form := cb.Form(); form != nil && !form.Suspended() {
		form.SetSuspended(true)
		defer form.SetSuspended(false)
	}
	_ = cb.SetModel(items)
	keepComboCompact(cb)
}

// keepComboCompact 让选项长度不再决定下拉框的最小宽度。walk 把最长选项的文字宽度
// 缓存在私有字段 maxItemTextWidth 里并直接当作最小宽度，一条长报文就能把发送区
// 连同窗口撑出屏幕。缓存压成 1 后最小宽度回到声明的 MinSize；walk 换选项和 DPI
// 变化后会重算，所以两处之后都要再调一次。展开的列表至少 360px，长报文也看得清。
func keepComboCompact(cb *walk.ComboBox) {
	if cb == nil || cb.IsDisposed() {
		return
	}
	cb.SendMessage(win.CB_SETDROPPEDWIDTH, uintptr(walk.IntFrom96DPI(360, cb.DPI())), 0)
	field := reflect.ValueOf(cb).Elem().FieldByName("maxItemTextWidth")
	if !field.IsValid() || field.Kind() != reflect.Int {
		return // walk 改了内部结构；长内容布局测试会失败提示。
	}
	if width := (*int)(unsafe.Pointer(field.UnsafeAddr())); *width != 1 {
		*width = 1
		cb.RequestLayout()
	}
}

// toolButton 是定宽按钮：宽度按“图标 + 文字 + 内边距”给足，
// 上下限相同，既不会被拉伸，也不会被挤到少显示一个字。
func toolButton(text, icon string, width int, clicked walk.EventHandler) PushButton {
	b := PushButton{Text: text, MinSize: Size{Width: width, Height: btnH}, MaxSize: Size{Width: width}, OnClicked: clicked}
	if icon != "" {
		b.Image = uiIcon(icon)
	}
	return b
}

func (a *application) showVirtualCOMHelp() {
	walk.MsgBox(a.mw, "VirtualCOM 免驱动连接",
		"在「工具 → 虚拟串口管理」创建一对端口，无需另外启动 VirtualCOM。关闭管理窗口后端口继续工作，退出创建端口的 CommBox 实例时释放。\r\n\r\n"+
			"在两个 CommBox 窗口中选择「串口」→「刷新串口」，分别打开这一对的两个 COM 号，即可双向收发。也可在命令行用 -list 查看、-port COM号 打开。\r\n\r\n"+
			"VirtualCOM 传输原始字节，波特率、数据位、校验、停止位与控制信号不生效。退出创建端口的程序会断开通信；创建新端口后需重新连接。\r\n\r\n"+
			"此适配仅用于 CommBox，不会让未适配的第三方串口软件自动兼容。TCP/UDP 转发可使用「串口服务器」模式。",
		walk.MsgBoxOK|walk.MsgBoxIconInformation)
}

// fitToWorkArea 把窗口收进任务栏之外的可用区域并居中。
// 默认 1280x820 在小屏或高缩放比下会越过工作区下沿，底部的发送区被任务栏盖住；
// 只在放不下时才缩小，放得下就仅调整位置，不牺牲数据表高度。
func (a *application) fitToWorkArea() {
	if a.mw == nil || a.workArea == nil {
		return
	}
	if rc, ok := a.workArea(); ok {
		a.fitIntoWorkArea(rc)
	}
}

// workAreaFor 取窗口所在显示器的工作区（物理像素）。多屏且缩放不同时，
// 主屏的工作区不代表窗口实际所在的那块屏。
func workAreaFor(hwnd win.HWND) (win.RECT, bool) {
	var mi win.MONITORINFO
	mi.CbSize = uint32(unsafe.Sizeof(mi))
	if m := win.MonitorFromWindow(hwnd, win.MONITOR_DEFAULTTONEAREST); m != 0 && win.GetMonitorInfo(m, &mi) {
		return mi.RcWork, true
	}
	const spiGetWorkArea = 0x0030
	var rc win.RECT
	return rc, win.SystemParametersInfo(spiGetWorkArea, 0, unsafe.Pointer(&rc), 0)
}

// fitIntoWorkArea 按给定工作区（物理像素）收缩并居中主窗口，测试用它模拟各种屏幕。
func (a *application) fitIntoWorkArea(rc win.RECT) {
	availW, availH := int(rc.Right-rc.Left), int(rc.Bottom-rc.Top)
	if availW <= 0 || availH <= 0 {
		return
	}
	// 先定栏位再改尺寸：窄屏收起左栏后最小宽度才降下来，窗口才缩得进去。
	a.updateNarrow(rc)
	// 宽度上限取工作区的 85%：1280x820 在 175% 缩放的屏幕上换算后接近满屏，
	// 留出余量才便于和其他窗口并排。高度与并排无关，放不下时用满工作区：
	// 1920x1080@150% 上按 85% 收缩会白白少掉约 100px，数据表只剩三行。
	// 屏幕足够大时仍用设计尺寸，不做放大。
	b := a.mw.BoundsPixels()
	_ = a.mw.SetBoundsPixels(walk.Rectangle{X: b.X, Y: b.Y, Width: min(b.Width, availW*85/100), Height: min(b.Height, availH)})
	// 系统会把尺寸抬到内容的最小尺寸，按抬过之后的实际大小居中，
	// 否则窄屏上窗口右缘会越出工作区。
	b = a.mw.BoundsPixels()
	_ = a.mw.SetBoundsPixels(walk.Rectangle{
		X:      int(rc.Left) + max(0, (availW-b.Width)/2),
		Y:      int(rc.Top) + max(0, (availH-b.Height)/2),
		Width:  b.Width,
		Height: b.Height,
	})
}

// 窗口在任何时候都不能比所在屏幕的工作区大。启动时由 fitToWorkArea 收一次；
// 运行中换到另一块屏、改分辨率或缩放、任务栏变高之后，walk 只按系统建议的矩形
// 保持逻辑尺寸，窗口可能越过屏幕。这里子类化窗口过程，在这些事件之后再检查一次。
var (
	workAreaWatch     = map[win.HWND]*application{}
	workAreaWatchPrev = map[win.HWND]uintptr{}
	workAreaWatchProc = syscall.NewCallback(func(h win.HWND, msg uint32, wp, lp uintptr) uintptr {
		a, prev := workAreaWatch[h], workAreaWatchPrev[h]
		r := win.CallWindowProc(prev, h, msg, wp, lp)
		const spiSetWorkArea = 0x002F
		if a == nil {
			return r
		}
		switch msg {
		case win.WM_ENTERSIZEMOVE:
			a.movingWindow = true
		case win.WM_EXITSIZEMOVE:
			a.movingWindow = false
			a.scheduleKeepInWorkArea()
		case win.WM_DISPLAYCHANGE, win.WM_DPICHANGED:
			a.scheduleKeepInWorkArea()
		case win.WM_SETTINGCHANGE:
			if wp == spiSetWorkArea {
				a.scheduleKeepInWorkArea()
			}
		case win.WM_WINDOWPOSCHANGED:
			// 贴靠、Win+Shift+方向键换屏不经过拖动循环；拖动中不打断用户。
			if !a.movingWindow {
				a.scheduleKeepInWorkArea()
			}
		case win.WM_NCDESTROY:
			delete(workAreaWatch, h)
			delete(workAreaWatchPrev, h)
		}
		return r
	})
)

func (a *application) watchWorkArea() {
	h := a.mw.Handle()
	workAreaWatch[h] = a
	workAreaWatchPrev[h] = win.SetWindowLongPtr(h, win.GWLP_WNDPROC, workAreaWatchProc)
}

// scheduleKeepInWorkArea 把检查推到当前消息处理完之后：walk 处理 DPI 变化时还要
// 重排、改尺寸，在窗口过程里直接改尺寸会和它打架。连续多个事件只检查一次。
func (a *application) scheduleKeepInWorkArea() {
	if a == nil || a.refitPending || a.mw == nil || a.workArea == nil {
		return
	}
	a.refitPending = true
	a.mw.Synchronize(func() {
		// 检查期间保持 refitPending：keepInside 自己改尺寸引起的 WM_WINDOWPOSCHANGED
		// 不再排新的检查。否则最小尺寸比工作区还大时，系统把尺寸抬回去又触发检查，
		// 会一直循环。
		defer func() { a.refitPending = false }()
		if rc, ok := a.workArea(); ok {
			a.keepInside(rc)
		}
	})
}

// keepInside 只在窗口比工作区大时才收缩并移回屏幕内；放得下的窗口不动，
// 用户自己拖到半出屏幕的位置也保留。最大化、最小化时由系统管，不插手。
func (a *application) keepInside(rc win.RECT) {
	h := a.mw.Handle()
	if win.IsIconic(h) || win.IsZoomed(h) {
		return
	}
	a.updateNarrow(rc)
	b := a.mw.BoundsPixels()
	availW, availH := int(rc.Right-rc.Left), int(rc.Bottom-rc.Top)
	if b.Width <= availW && b.Height <= availH {
		return
	}
	w, hh := min(b.Width, availW), min(b.Height, availH)
	x := min(max(b.X, int(rc.Left)), int(rc.Right)-w)
	y := min(max(b.Y, int(rc.Top)), int(rc.Bottom)-hh)
	_ = a.mw.SetBoundsPixels(walk.Rectangle{X: x, Y: y, Width: w, Height: hh})
}

// updateNarrow 按工作区宽度（逻辑像素）切换窄屏单栏。回到宽屏时恢复左栏常驻。
func (a *application) updateNarrow(rc win.RECT) {
	// Short screens use menu equivalents, leaving room for data and the composer.
	if a.appHeader != nil {
		setVisible(a.appHeader, walk.IntTo96DPI(int(rc.Bottom-rc.Top), a.mw.DPI()) >= 650)
	}
	narrow := walk.IntTo96DPI(int(rc.Right-rc.Left), a.mw.DPI()) < wideLayoutWidth
	if narrow == a.narrow {
		return
	}
	a.narrow = narrow
	a.connectionPage = false
	a.arrangePanes(a.assistant.panel.Visible())
}

// arrangePanes 按 AI 面板是否展开（ai）决定三栏的显隐。
// 常规屏幕：中栏常驻；AI 展开且客户区不足 bothSidePanesWidth 时收起左栏。
// 窄屏单栏：一次只显示一栏，默认中栏；左栏、AI 面板显示时取消宽度上限、占满窗口，
// 中栏与侧栏按可用空间切换。
func (a *application) arrangePanes(ai bool) {
	if a.mw == nil || a.connectionPane == nil || a.monitorPane == nil || a.assistant == nil || a.assistant.panel == nil || a.arrangingPanes {
		return
	}
	a.arrangingPanes = true
	defer func() { a.arrangingPanes = false }()
	hasMonitor := a.views.Data || a.views.Log || a.views.Send
	conn, monitor := (a.views.Connection || a.connectionPage) && (!ai || a.mw.ClientBounds().Width >= bothSidePanesWidth || !hasMonitor), hasMonitor
	connMax, aiMax, monitorMin := connectionPaneMaxWidth, assistantMaxWidth, minMonitorWidth
	if a.narrow {
		conn, monitor = (a.connectionPage || (!hasMonitor && a.views.Connection)) && !ai, hasMonitor && !ai && !a.connectionPage
		connMax, aiMax, monitorMin = 0, 0, 0
	}
	if !hasMonitor {
		if !ai {
			conn = true // Closing the only AI pane must leave a usable workspace.
		}
		if ai && !conn {
			aiMax = 0
		}
		if conn && !ai {
			connMax = 0
		}
	}
	setWidthLimits(a.connectionPane, connectionPaneMinWidth, connMax)
	setWidthLimits(a.assistant.panel, assistantMinWidth, aiMax)
	setWidthLimits(a.monitorPane, monitorMin, 0)
	// walk 的 ScrollView 关掉横向滚动后不能横向拉伸，去掉宽度上限也照样停在最小宽度。
	// 窄屏单栏时打开横向滚动让侧栏占满窗口；侧栏比内容宽，不会真的出现横向滚动条。
	setHorizontalScroll(a.connectionPane, a.narrow || !hasMonitor)
	setHorizontalScroll(a.assistant.panel, a.narrow || !hasMonitor)
	setVisible(a.connectionPageButton, a.narrow || !a.views.Connection)
	setVisible(a.dataPageButton, a.narrow || !a.views.Connection)
	// 先收起再展开：反过来 walk 会先按多出的一栏把窗口撑宽。
	if !conn {
		setVisible(a.connectionPane, false)
	}
	if !monitor {
		setVisible(a.monitorPane, false)
	}
	setVisible(a.connectionPane, conn)
	setVisible(a.monitorPane, monitor)
}

// setWidthLimits 与 setVisible 只在值变化时才写：每次写都会整窗重排，
// 而 arrangePanes 在拖动改尺寸时每一步都会被调用。
func setWidthLimits(w walk.Widget, minW, maxW int) {
	lo, hi := walk.Size{Width: minW}, walk.Size{Width: maxW}
	if w.MinSize() != lo || w.MaxSize() != hi {
		_ = w.SetMinMaxSize(lo, hi)
	}
}

func setVisible(w walk.Widget, visible bool) {
	if w.Visible() != visible {
		w.SetVisible(visible)
	}
}

// 打开横向滚动后 walk 不再从内容宽度里扣掉纵向滚动条，内容会伸到滚动条下面，
// 右边距补上滚动条的宽度（96 DPI 下 17）。两个侧栏平时的右边距都是 10。
func setHorizontalScroll(sv *walk.ScrollView, on bool) {
	if h, v := sv.Scrollbars(); h != on {
		sv.SetScrollbars(on, v)
		m := sv.Layout().Margins()
		m.HFar = 10
		if on {
			m.HFar += 17
		}
		_ = sv.Layout().SetMargins(m)
		sv.RequestLayout()
	}
}

func (a *application) showConnectionPage() {
	a.connectionPage = true
	a.assistant.panel.SetVisible(false)
	a.arrangePanes(false)
}

func (a *application) showDataPage() {
	a.connectionPage = false
	if !a.views.Data && !a.views.Log && !a.views.Send && a.views.AI {
		a.showAssistant()
		return
	}
	a.arrangePanes(a.assistant.panel.Visible())
}

// Win32 在重排时会收起下拉列表。用户选项期间暂缓统计区刷新，
// 收发与存储照常进行，下一轮统计会补上最新值。
func (a *application) comboDropDownOpen() bool {
	combos := []*walk.ComboBox{a.mode, a.ports, a.baud, a.data, a.parity, a.stop,
		a.protocol, a.role, a.netIP, a.recentConn, a.dirFilter, a.displayMode,
		a.protocolFilter, a.timeFilter, a.eol, a.sendTarget, a.sendHistory, a.favorites}
	if a.assistant != nil {
		combos = append(combos, a.assistant.scope)
	}
	for _, combo := range combos {
		if combo != nil && !combo.IsDisposed() && win.SendMessage(combo.Handle(), win.CB_GETDROPPEDSTATE, 0, 0) != 0 {
			return true
		}
	}
	return false
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
	a.fitPacketColumns()
}

// 只让 HEX / ASCII 接收剩余宽度；长度等元数据列保持紧凑。
// 两种表示同时显示时 HEX 占约 2/3，单独显示时使用全部剩余空间。
func (a *application) fitPacketColumns() {
	if a.packetTable == nil || a.packetTable.Columns().Len() < 8 {
		return
	}
	columns := a.packetTable.Columns()
	available := a.packetTable.ClientBounds().Width - 20 // 边框与垂直滚动条。
	for i := 0; i < columns.Len(); i++ {
		if i != 2 && i != 3 && columns.At(i).Visible() {
			available -= columns.At(i).Width()
		}
	}
	hex, ascii := columns.At(2), columns.At(3)
	switch {
	case hex.Visible() && ascii.Visible():
		available = max(250, available)
		_ = hex.SetWidth(available * 2 / 3)
		_ = ascii.SetWidth(available - available*2/3)
	case hex.Visible():
		_ = hex.SetWidth(max(150, available))
	case ascii.Visible():
		_ = ascii.SetWidth(max(100, available))
	}
}

func (a *application) toggleAdvancedFilters() {
	show := !a.advancedFilters.Visible()
	a.advancedFilters.SetVisible(show)
	a.updateFilterToggle()
}

func (a *application) updateFilterToggle() {
	if a.filterToggle == nil || a.advancedFilters == nil || a.protocolFilter == nil || a.timeFilter == nil || a.connectionFilter == nil {
		return
	}
	text := "更多筛选"
	if a.advancedFilters.Visible() {
		text = "收起筛选"
	} else if a.protocolFilter.CurrentIndex() > 0 || a.timeFilter.CurrentIndex() > 0 || strings.TrimSpace(a.connectionFilter.Text()) != "" {
		text = "筛选已启用"
	}
	if a.filterToggle.Text() != text {
		a.filterToggle.SetText(text)
	}
}

// updateSelectionLabel 显示当前选中条数，对应 mac 的 _selectionLabel。
func (a *application) updateSelectionLabel() {
	if a.selectionLabel == nil || a.packetTable == nil {
		return
	}
	n := len(a.packetTable.SelectedIndexes())
	if a.analyzeSelectionButton != nil {
		a.analyzeSelectionButton.SetEnabled(n > 0)
	}
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
	if len(a.packetTable.SelectedIndexes()) == 0 {
		a.setSendFeedback("请先在数据表中选择需要分析的报文", true)
		return
	}
	a.showAssistant()
	a.assistant.scope.SetCurrentIndex(0)
	a.assistant.attach.SetChecked(true)
	a.assistant.packetOptions.SetVisible(true)
	a.assistant.chat.input.SetText("请分析选中的报文。")
	a.assistant.chat.input.SetFocus()
}

func (a *application) toggleAssistant() {
	if a.assistant.panel.Visible() {
		a.assistant.panel.SetVisible(false)
		a.arrangePanes(false)
	} else {
		a.showAssistant()
	}
}

// showAssistant 先按「AI 已展开」收好其他栏再显示 AI，防止 walk 先把整个窗口
// 扩到多一栏的最小宽度。
func (a *application) showAssistant() {
	a.connectionPage = false
	a.arrangePanes(true)
	a.arrangingPanes = true
	a.assistant.panel.SetVisible(true)
	a.arrangingPanes = false
}
func (a *application) updateDisplay() {
	if a.packetTable == nil || a.displayMode == nil || a.showTime == nil {
		return
	}
	a.packetTable.Columns().At(0).SetVisible(a.showTime.Checked())
	a.packetTable.Columns().At(2).SetVisible(a.displayMode.CurrentIndex() != 2)
	a.packetTable.Columns().At(3).SetVisible(a.displayMode.CurrentIndex() != 1)
	a.fitPacketColumns()
}
func (a *application) loadPacket() bool {
	i := a.packetTable.CurrentIndex()
	if i >= 0 && i < len(a.packetModel.visible) {
		a.sendEdit.SetText(a.packetModel.visible[i].Hex)
		a.hexSend.SetChecked(true)
		a.setSendFeedback("已载入报文；快捷框输入名称后点击保存", false)
		return true
	}
	a.setSendFeedback("请先选择一条报文", true)
	return false
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
	// 同样每秒被调用：文本没变就不要写回,避免无谓重排关掉展开中的下拉列表。
	if s := fmt.Sprintf("本地  %s\r\n远程  %s\r\nRX %s  /  TX %s\r\n时长  %s", p.LocalAddress, p.RemoteAddress, core.FormatBytes(p.RXBytes-b.RXBytes), core.FormatBytes(p.TXBytes-b.TXBytes), core.FormatDuration(time.Since(p.ConnectedAt))); s != a.lastDetails {
		a.lastDetails = s
		a.detailsLabel.SetText(s)
	}
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
