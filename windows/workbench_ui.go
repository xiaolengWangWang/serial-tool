//go:build windows

package main

import (
	"encoding/csv"
	"fmt"
	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
	"os"
	"serial-tool/internal/wincore"
	"strings"
	"time"
	"unsafe"
)

// 三栏骨架：左栏连接配置（定宽可滚动）、中栏数据与发送（撑满剩余宽度）、
// 右栏 AI 面板（默认隐藏）。底部为一行状态栏。
func (a *application) createWindow() error {
	a.peerBaseline = map[string]wincore.ConnectionInfo{}
	a.assistant = &assistantPanel{app: a}
	if err := (MainWindow{
		AssignTo: &a.mw,
		Title:    "CommBox v" + wincore.Version + " · Windows",
		Size:     Size{Width: 1280, Height: 820},
		// 最小尺寸由内容决定（数据表至少 3 行、发送框至少 4 行），这里只兜底。
		// 1600x900@150%、1366x768@125% 等屏幕的工作区只有约 1024x552，
		// 原来的 1024x620 放不进去，底部发送区会压在任务栏下面。
		MinSize:    Size{Width: 960, Height: 480},
		Font:       Font{Family: fontUI, PointSize: sizeBody},
		Background: SolidColorBrush{Color: colorCanvas},
		Layout:     VBox{Alignment: AlignHNearVNear, Margins: Margins{Left: 10, Top: 6, Right: 10, Bottom: 4}, Spacing: 6},
		MenuItems:  a.menus(),
		Children: []Widget{
			// 不设工具栏：原有五个按钮在菜单栏或数据监控标题行都已有等价入口，
			// 去掉整行可把纵向空间还给数据表（对齐 mac，app.m 同样没有工具栏）。
			Composite{Layout: HBox{Alignment: AlignHNearVCenter, MarginsZero: true, Spacing: 10}, StretchFactor: 1, Children: []Widget{
				a.connectionPanel(),
				a.monitorPanel(),
				a.assistant.widget(),
			}},
			// 状态栏文字随连接状态变长。EllipsisMode 让这一行可压缩：
			// 不加时它的文字宽度会顶住整窗最小宽度，运行中还会跟着数字一起变。
			Composite{Background: SolidColorBrush{Color: colorPanel}, Layout: HBox{Margins: Margins{Left: 8, Right: 8}, SpacingZero: true}, Children: []Widget{
				Label{AssignTo: &a.footer, Text: "未连接  |  RX 0 B  |  TX 0 B", MinSize: Size{Height: 20}, Font: Font{Family: fontMono, PointSize: sizeMono}, TextColor: colorMuted, EllipsisMode: EllipsisEnd, Alignment: AlignHNearVCenter},
			}},
		},
	}).Create(); err != nil {
		return err
	}
	// 按逻辑像素切换窄屏布局，展开助手不会把窗口撑出工作区。
	a.mw.SizeChanged().Attach(a.enforceAssistantWidth)
	if a.autoUpdateAction != nil {
		_ = a.autoUpdateAction.SetChecked(a.autoUpdateEnabled())
	}
	a.updateSelectionLabel()
	a.initViewSwitch()
	a.fitToWorkArea()
	return nil
}

func (a *application) menus() []MenuItem {
	return []MenuItem{
		Menu{Text: "文件", Items: []MenuItem{Action{Text: "新建实例", OnTriggered: a.newInstance}, Action{Text: "保存 TXT", OnTriggered: func() { a.exportText(a.packetModel.exportText(), "commbox", a.mw) }}, Action{Text: "导出 CSV", OnTriggered: a.exportCSV}, Action{Text: "退出", OnTriggered: func() { a.mw.Close() }}}},
		Menu{Text: "查看", Items: []MenuItem{Action{Text: "AI 助手", OnTriggered: a.toggleAssistant}, Action{Text: "实时监控窗口", OnTriggered: a.openMonitor}}},
		Menu{Text: "工具", Items: []MenuItem{Action{Text: "HTTP 工作台", OnTriggered: a.openHTTPWorkspace}, Action{Text: "校验与转换", OnTriggered: a.openToolbox}, Action{Text: "连接管理", OnTriggered: a.openConnections}, Action{Text: "串口服务器", OnTriggered: func() {
			if !a.connected && !a.connecting {
				a.mode.SetCurrentIndex(3)
				a.updateMode()
			}
		}}, Action{Text: "VirtualCOM 连接说明", OnTriggered: a.showVirtualCOMHelp}, Action{Text: "历史数据分析", OnTriggered: a.openDatabaseAnalysis}}},
		Menu{Text: "设置", Items: []MenuItem{Action{Text: "AI 设置", OnTriggered: a.assistant.settings}, Action{Text: "连接数与桥接", OnTriggered: a.openConnections}, Separator{}, Action{AssignTo: &a.autoUpdateAction, Text: "启动时检查更新", Checkable: true, OnTriggered: a.toggleAutoUpdate}}},
		Menu{Text: "帮助", Items: []MenuItem{Action{Text: "使用说明", OnTriggered: a.showHelp}, Action{Text: "检查更新", OnTriggered: a.checkUpdate}, Action{Text: "发送 (F5)", Image: uiIcon("send"), Shortcut: Shortcut{Key: walk.KeyF5}, OnTriggered: func() { a.sendOnce(false) }}}},
	}
}

// connectionPanel 是左栏：模式、参数、连接状态与对端列表。
// 定宽并可纵向滚动，窗口再矮也不会把参数挤成一团。
func (a *application) connectionPanel() Widget {
	return ScrollView{AssignTo: &a.connectionPane, HorizontalFixed: true, MinSize: Size{Width: 300}, MaxSize: Size{Width: 316}, Background: SolidColorBrush{Color: colorPanel},
		Layout: VBox{Alignment: AlignHNearVNear, Margins: Margins{Left: 10, Top: 6, Right: 26, Bottom: 10}, Spacing: 8}, Children: []Widget{
			Label{Text: "连接配置", Font: fontSection, TextColor: colorBlue},
			ComboBox{AssignTo: &a.mode, ToolTipText: "串口 / TCP / UDP / 串口服务器 / HTTP 客户端", Model: modes, CurrentIndex: 1, MinSize: Size{Height: rowH}, OnCurrentIndexChanged: a.updateMode},
			Composite{Layout: HBox{Alignment: AlignHNearVCenter, MarginsZero: true, Spacing: 6}, Children: []Widget{
				inlineLabel("最近连接", 64),
				ComboBox{AssignTo: &a.recentConn, ToolTipText: "选择最近使用的连接，自动回填参数", StretchFactor: stretchFill, MinSize: Size{Width: 150, Height: rowH}, OnCurrentIndexChanged: a.onRecentConnSelected},
			}},
			// 两列栅格：标签列按最长标签自动定宽，每个字段只占一行，
			// 左栏内容不再溢出到需要滚动才能看到“最近连接”。
			GroupBox{AssignTo: &a.serialGroup, Title: "串口参数", Layout: Grid{Alignment: AlignHNearVCenter, Columns: 2, Spacing: 6, Margins: Margins{Left: 10, Top: 6, Right: 10, Bottom: 10}}, Children: []Widget{
				formLabel("端口"), ComboBox{AssignTo: &a.ports, Editable: true, ToolTipText: "支持物理串口及 VirtualCOM 免驱动端口。VirtualCOM 的波特率、数据位、校验与停止位不生效。", MinSize: Size{Width: 140, Height: rowH}},
				formLabel("波特率"), ComboBox{AssignTo: &a.baud, Editable: true, Model: []string{"1200", "2400", "4800", "9600", "19200", "38400", "57600", "115200", "230400", "460800", "921600"}, CurrentIndex: 7, MinSize: Size{Height: rowH}},
				formLabel("数据位"), ComboBox{AssignTo: &a.data, Model: []string{"5", "6", "7", "8"}, CurrentIndex: 3, MinSize: Size{Height: rowH}},
				formLabel("校验"), ComboBox{AssignTo: &a.parity, Model: []string{"无校验", "奇校验", "偶校验"}, CurrentIndex: 0, MinSize: Size{Height: rowH}},
				formLabel("停止位"), ComboBox{AssignTo: &a.stop, Model: []string{"1", "2"}, CurrentIndex: 0, MinSize: Size{Height: rowH}},
				PushButton{Text: "刷新串口", Image: uiIcon("refresh"), ColumnSpan: 2, MinSize: Size{Height: btnH}, OnClicked: a.refreshPorts},
			}},
			GroupBox{AssignTo: &a.networkGroup, Title: "网络参数", Layout: Grid{Alignment: AlignHNearVCenter, Columns: 2, Spacing: 6, Margins: Margins{Left: 10, Top: 6, Right: 10, Bottom: 10}}, Children: []Widget{
				formLabelTo("服务器 IP", &a.addressLabel), ComboBox{AssignTo: &a.netIP, Editable: true, Model: append([]string{"127.0.0.1", "0.0.0.0"}, wincore.LocalIPs()...), CurrentIndex: 0, MinSize: Size{Width: 140, Height: rowH}},
				formLabelTo("端口", &a.portLabel), LineEdit{AssignTo: &a.netPort, Text: "9000", MinSize: Size{Height: rowH}},
				a.protocolFormLabel(), ComboBox{AssignTo: &a.protocol, Model: []string{"TCP", "UDP"}, CurrentIndex: 0, Visible: false, MinSize: Size{Height: rowH}},
				formLabelTo("角色", &a.roleLabel), ComboBox{AssignTo: &a.role, Model: []string{"服务端", "客户端"}, CurrentIndex: 1, MinSize: Size{Height: rowH}, OnCurrentIndexChanged: a.updateMode},
				formLabel("重连"), Composite{Layout: HBox{Alignment: AlignHNearVCenter, MarginsZero: true, Spacing: 6}, Children: []Widget{
					CheckBox{AssignTo: &a.autoReconnect, Text: "自动", Checked: true, MinSize: Size{Width: 62}, MaxSize: Size{Width: 62}},
					LineEdit{AssignTo: &a.reconnectInterval, Text: "2", MinSize: Size{Width: 52, Height: rowH}, MaxSize: Size{Width: 52}},
					inlineLabel("s", 14),
					HSpacer{},
				}},
			}},
			// HSpacer 让状态靠左，与上方表单左对齐；此前两个 Label 被 HBox 均分而显得居中。
			Composite{Layout: HBox{Alignment: AlignHNearVCenter, MarginsZero: true, Spacing: 6}, Children: []Widget{
				Label{AssignTo: &a.statusDot, Text: "●", TextColor: colorGray, MinSize: Size{Width: 16}, MaxSize: Size{Width: 16}, Alignment: AlignHNearVCenter},
				Label{AssignTo: &a.status, Text: "未连接", EllipsisMode: EllipsisEnd, Alignment: AlignHNearVCenter, StretchFactor: 1},
				HSpacer{},
			}},
			PushButton{AssignTo: &a.connectButton, Text: "连接", MinSize: Size{Height: 36}, Image: uiIcon("connect"), OnClicked: a.toggleConnection},
			Label{AssignTo: &a.peerTitle, Text: "客户端 / 对端 (0)", Font: Font{Family: fontUI, PointSize: sizeBody, Bold: true}, EllipsisMode: EllipsisEnd},
			ListBox{AssignTo: &a.peerList, Model: []string{}, StretchFactor: 1, MinSize: Size{Height: 96}, OnCurrentIndexChanged: a.showPeerDetails, OnItemActivated: func() { a.peerAction("send") }, ContextMenuItems: []MenuItem{
				Action{Text: "发送数据", OnTriggered: func() { a.peerAction("send") }}, Action{Text: "仅查看该客户端", OnTriggered: func() { a.peerAction("filter") }}, Action{Text: "断开连接", OnTriggered: func() { a.peerAction("disconnect") }}, Action{Text: "复制地址", OnTriggered: func() { a.peerAction("copy") }}, Action{Text: "清空显示统计", OnTriggered: func() { a.peerAction("reset"); a.showPeerDetails() }},
			}},
			// 连接信息是四行文本，高度必须按四行给足，否则末行被裁掉半个字。
			Label{AssignTo: &a.detailsLabel, Text: "选择对端查看地址与收发统计\r\n右键可定向发送、筛选或断开", TextColor: colorMuted, MinSize: Size{Height: 86}, MaxSize: Size{Width: 282, Height: 86}},
		}}
}

// monitorPanel 是中栏：标题与筛选各占一行，其余高度交给数据表与发送区的分隔条。
func (a *application) monitorPanel() Widget {
	return Composite{StretchFactor: 1, Background: SolidColorBrush{Color: colorPanel}, Layout: VBox{Alignment: AlignHNearVNear, Margins: Margins{Left: 8, Top: 6, Right: 8, Bottom: 6}, Spacing: 4}, Children: []Widget{
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
			toolButton("清空", "clear", 80, func() { a.packetModel.clear(); a.updatePacketStats(); a.updateSelectionLabel() }),
			toolButton("保存", "save", 80, func() { a.exportText(a.packetModel.exportText(), "commbox", a.mw) }),
			toolButton("AI 分析", "ai", 104, func() { a.showAssistant(); a.assistant.analyze("全面诊断") }),
		}},
		// 搜索与连接条件一行，显示与范围一行，小窗口不再被整排下拉框撑宽。
		Composite{Layout: HBox{Alignment: AlignHNearVCenter, MarginsZero: true, Spacing: 6}, Children: []Widget{
			LineEdit{AssignTo: &a.searchEdit, CueBanner: "搜索 HEX / ASCII / 文本", StretchFactor: stretchFill, MinSize: Size{Width: 160, Height: rowH}, OnTextChanged: a.applyFilter},
			LineEdit{AssignTo: &a.connectionFilter, CueBanner: "连接 ID / 来源地址", MinSize: Size{Width: 150, Height: rowH}, MaxSize: Size{Width: 220}, OnTextChanged: a.applyFilter},
			PushButton{Text: "重置筛选", MinSize: Size{Width: 96, Height: btnH}, MaxSize: Size{Width: 96}, OnClicked: a.clearFilter},
		}},
		Composite{Layout: HBox{Alignment: AlignHNearVCenter, MarginsZero: true, Spacing: 6}, Children: []Widget{
			fixedCombo(&a.dirFilter, []string{dirAll, "RX", "TX", "EVENT"}, 96, a.applyFilter),
			fixedCombo(&a.displayMode, []string{"HEX + ASCII", "HEX", "ASCII"}, 120, a.updateDisplay),
			fixedCombo(&a.protocolFilter, []string{"全部协议", "SERIAL", "TCP", "UDP", "HTTP"}, 98, a.applyFilter),
			fixedCombo(&a.timeFilter, []string{"全部时间", "1分钟", "5分钟", "30分钟"}, 96, a.applyFilter),
			HSpacer{StretchFactor: stretchFill},
			Label{AssignTo: &a.statsLabel, Text: "显示 0 / 0 条", TextColor: colorMuted, EllipsisMode: EllipsisEnd, Alignment: AlignHFarVCenter, MinSize: Size{Width: 100}, MaxSize: Size{Width: 180}},
		}},
		VSplitter{StretchFactor: 1, Children: []Widget{a.packetViews(), a.sendArea()}},
	}}
}

// packetViews 是数据表（外加一行选中操作）与日志两个视图，由标题行的
// 数据 / 日志按钮切换，同一时间只显示一个。
func (a *application) packetViews() Widget {
	analyze := toolButton("分析选中", "ai", 128, a.analyzeSelected)
	analyze.AssignTo = &a.analyzeSelectionButton
	analyze.Enabled = false
	return Composite{StretchFactor: 5, Layout: VBox{Alignment: AlignHNearVNear, MarginsZero: true}, Children: []Widget{
		// 协议 / 来源 / 连接 ID 默认隐藏：八列合计 895px，而 AI 面板展开时中栏仅约 700px，
		// 全显必然出横向滚动条。需要时通过右键菜单打开。
		Composite{AssignTo: &a.dataView, Layout: VBox{Alignment: AlignHNearVNear, MarginsZero: true, Spacing: 4}, Children: []Widget{
			TableView{AssignTo: &a.packetTable, Model: a.packetModel, MinSize: Size{Height: minTableHeight}, MultiSelection: true, AlternatingRowBG: true, LastColumnStretched: true, Font: Font{Family: fontMono, PointSize: sizeMono}, StyleCell: a.stylePacketCell, OnSelectedIndexesChanged: a.updateSelectionLabel, Columns: []TableViewColumn{{Title: "时间", Width: 108}, {Title: "方向", Width: 52}, {Title: "HEX", Width: 248}, {Title: "ASCII", Width: 104}, {Title: "长度", Width: 58}, {Title: "协议", Width: 60, Hidden: true}, {Title: "来源", Width: 140, Hidden: true}, {Title: "连接 ID", Width: 170, Hidden: true}}, ContextMenuItems: []MenuItem{
				Action{Text: "复制 HEX", OnTriggered: func() { a.copyPacketField("hex") }}, Action{Text: "复制 ASCII", OnTriggered: func() { a.copyPacketField("ascii") }}, Action{Text: "复制整行", OnTriggered: func() { a.copyPacketField("all") }}, Action{Text: "重新发送", OnTriggered: func() {
					if a.loadPacket() {
						a.sendOnce(false)
					}
				}}, Action{Text: "添加到快捷发送", OnTriggered: func() { a.loadPacket() }}, Action{Text: "AI 分析选中数据", OnTriggered: a.analyzeSelected}, Separator{}, Action{AssignTo: &a.detailColumns, Text: "显示协议 / 来源 / 连接 ID 列", Checkable: true, OnTriggered: a.toggleDetailColumns}, Action{Text: "导出 CSV", OnTriggered: a.exportCSV},
			}},
			// 选中操作行：AI 面板有“当前选中数据”这个分析范围，此前主区却没有选择入口和计数。
			// 每个控件自带宽度上限，HBox 只能把富余宽度给 HSpacer，
			// 窗口收窄时按钮也就不会互相挤到文字叠在一起。
			Composite{Layout: HBox{Alignment: AlignHNearVCenter, MarginsZero: true, Spacing: 6}, Children: []Widget{
				Label{AssignTo: &a.selectionLabel, Text: "未选中", MinSize: Size{Width: 66}, MaxSize: Size{Width: 94}, EllipsisMode: EllipsisEnd, Alignment: AlignHNearVCenter},
				toolButton("全选", "", 70, a.selectAllPackets),
				toolButton("取消", "", 70, a.clearPacketSelection),
				toolButton("反选", "", 70, a.invertPacketSelection),
				analyze,
				HSpacer{StretchFactor: stretchFill},
				CheckBox{AssignTo: &a.autoScroll, Text: "自动滚动", Checked: true, MinSize: Size{Width: 102}, MaxSize: Size{Width: 102}},
				CheckBox{AssignTo: &a.showTime, Text: "时间", Checked: true, MinSize: Size{Width: 72}, MaxSize: Size{Width: 72}, OnCheckedChanged: a.updateDisplay},
			}},
		}},
		TextEdit{AssignTo: &a.logEdit, Visible: false, ReadOnly: true, VScroll: true, HScroll: true, MaxLength: 5000000, Font: Font{Family: fontMono, PointSize: sizeMono}},
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
	a.viewData.SetChecked(!log)
	a.viewLog.SetChecked(log)
	a.dataView.SetVisible(!log)
	a.logEdit.SetVisible(log)
}

// 矮屏上数据表与发送区的保底高度（96 DPI 逻辑像素，随缩放一起放大）。
// 数据表：表头约 24 + 3 行 × 约 20 + 边框；发送框：4 行等宽字 + 内边距，
// 并留出 125%/150% 下字高取整多出的一两个像素。分隔条拖不过这两个下限。
const (
	minTableHeight    = 90
	minSendEditHeight = 72
)

// 发送区按格式与目标、报文、历史与快捷、定时与循环分行，结果提示并在定时行右侧，
// 省下的一整行高度留给矮屏上的报文框和数据表。UDP 地址与目标并排，避免顶高小屏窗口。
func (a *application) sendArea() Widget {
	// 分隔条按 5:3 分高度，但各自不低于下限。原来 4:1 时发送区要到分隔条约
	// 985px 高才分得到比下限多的高度，实际上一直停在下限；5:3 下 1280x820
	// 报文框约 6 行，矮屏仍按下限保底，多出的都给数据表。
	return Composite{StretchFactor: 3, Background: SolidColorBrush{Color: colorCanvas}, Layout: VBox{Alignment: AlignHNearVNear, Margins: Margins{Left: 6, Top: 4, Right: 6}, Spacing: 6}, Children: []Widget{
		Composite{Layout: HBox{Alignment: AlignHNearVCenter, MarginsZero: true, Spacing: 6}, Children: []Widget{
			Label{Text: "发送", Font: Font{Family: fontUI, PointSize: sizeBody, Bold: true}, TextColor: colorBlue, Alignment: AlignHNearVCenter, MinSize: Size{Width: 36}, MaxSize: Size{Width: 36}},
			CheckBox{AssignTo: &a.hexSend, Text: "HEX", Checked: true, MinSize: Size{Width: 56}, MaxSize: Size{Width: 56}},
			inlineLabel("行尾", 38),
			ComboBox{AssignTo: &a.eol, Model: []string{"无", "LF", "CR", "CRLF"}, CurrentIndex: 0, MinSize: Size{Width: 80, Height: rowH}, MaxSize: Size{Width: 80}},
			inlineLabel("目标", 38),
			ComboBox{AssignTo: &a.sendTarget, Model: []string{"默认目标"}, CurrentIndex: 0, StretchFactor: stretchFill, MinSize: Size{Width: 112, Height: rowH}, MaxSize: Size{Width: 240}, ToolTipText: "默认目标：TCP 服务端广播，UDP 服务端回复最近对端，其他模式发送到当前连接；也可选择单个连接"},
			LineEdit{AssignTo: &a.udpTarget, Visible: false, CueBanner: "IP:端口（可选）", ToolTipText: "UDP 指定目标，例如 127.0.0.1:9000；仅在默认目标下生效，留空使用当前连接或最近对端", MinSize: Size{Width: 132, Height: rowH}, MaxSize: Size{Width: 200}},
			HSpacer{StretchFactor: stretchFill},
		}},
		Composite{Layout: HBox{Alignment: AlignHNearVCenter, MarginsZero: true, Spacing: 8}, StretchFactor: 1, Children: []Widget{
			TextEdit{AssignTo: &a.sendEdit, StretchFactor: 1, VScroll: true, MinSize: Size{Height: minSendEditHeight}, ToolTipText: "输入 HEX 字节或取消勾选 HEX 后输入文本；F5 发送，验证按钮检查内容", Font: Font{Family: fontMono, PointSize: sizeMono}},
			Composite{Layout: VBox{Alignment: AlignHNearVNear, MarginsZero: true, Spacing: 6}, MinSize: Size{Width: 116}, MaxSize: Size{Width: 116}, Children: []Widget{
				PushButton{Text: "发送 (F5)", Font: Font{Family: fontUI, PointSize: sizeBody, Bold: true}, Image: uiIcon("send"), MinSize: Size{Height: 40}, MaxSize: Size{Height: 40}, OnClicked: func() { a.sendOnce(false) }},
				PushButton{Text: "验证", Image: uiIcon("check"), MinSize: Size{Height: btnH}, MaxSize: Size{Height: btnH}, OnClicked: a.validateSend},
			}},
		}},
		Composite{Layout: HBox{Alignment: AlignHNearVCenter, MarginsZero: true, Spacing: 6}, Children: []Widget{
			inlineLabel("历史", 38),
			ComboBox{AssignTo: &a.sendHistory, Editable: true, StretchFactor: 1, MinSize: Size{Width: 96, Height: rowH}, OnCurrentIndexChanged: a.onSendHistorySelected},
			inlineLabel("快捷", 38),
			ComboBox{AssignTo: &a.favorites, Editable: true, StretchFactor: 1, MinSize: Size{Width: 96, Height: rowH}, OnCurrentIndexChanged: a.onFavoriteSelected},
			toolButton("保存", "save", 92, a.saveFavorite),
			toolButton("删除", "", 70, a.deleteFavorite),
			CheckBox{AssignTo: &a.clearAfterSend, Text: "发送后清空", MinSize: Size{Width: 112}, MaxSize: Size{Width: 112}},
		}},
		Composite{Layout: HBox{Alignment: AlignHNearVCenter, MarginsZero: true, Spacing: 6}, Children: []Widget{
			inlineLabel("间隔 ms", 66),
			LineEdit{AssignTo: &a.interval, Text: "1000", MinSize: Size{Width: 72, Height: rowH}, MaxSize: Size{Width: 72}},
			PushButton{AssignTo: &a.timerButton, Text: "开始定时", MinSize: Size{Width: 96, Height: btnH}, MaxSize: Size{Width: 96}, OnClicked: a.toggleTimer},
			inlineLabel("次数", 38),
			LineEdit{AssignTo: &a.loopCount, Text: "0", MinSize: Size{Width: 62, Height: rowH}, MaxSize: Size{Width: 62}, ToolTipText: "循环次数，0 表示持续发送"},
			PushButton{AssignTo: &a.loopButton, Text: "循环发送", MinSize: Size{Width: 96, Height: btnH}, MaxSize: Size{Width: 96}, OnClicked: a.toggleLoopSend},
			// 提示按文字宽度显示，窄窗口下压缩成省略号，完整内容在悬停提示里。
			// 标签不会吃掉整行富余，末尾仍要 HSpacer，否则富余宽度会摊成控件间的空隙。
			Label{AssignTo: &a.sendPreview, Text: "输入报文后可先验证，按 F5 发送", ToolTipText: "未勾选 HEX 时按文本发送；快捷框输入名称后可保存当前报文", MinSize: Size{Width: 40}, TextColor: colorMuted, EllipsisMode: EllipsisEnd, Alignment: AlignHNearVCenter},
			HSpacer{},
		}},
	}}
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

// formLabel 是栅格里的表单标签：与右侧输入框垂直居中，文字不再贴在框顶。
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
		"先在 VirtualCOM 中创建一对端口并保持程序运行。\r\n\r\n"+
			"在两个 CommBox 窗口中选择「串口」→「刷新串口」，分别打开这一对的两个 COM 号，即可双向收发。也可在命令行用 -list 查看、-port COM号 打开。\r\n\r\n"+
			"VirtualCOM 传输原始字节，波特率、数据位、校验、停止位与控制信号不生效。退出 VirtualCOM 会断开通信；创建新端口后需重新连接。\r\n\r\n"+
			"此适配仅用于 CommBox，不会让未适配的第三方串口软件自动兼容。TCP/UDP 转发可使用「串口服务器」模式。",
		walk.MsgBoxOK|walk.MsgBoxIconInformation)
}

// fitToWorkArea 把窗口收进任务栏之外的可用区域并居中。
// 默认 1280x820 在小屏或高缩放比下会越过工作区下沿，底部的发送区被任务栏盖住；
// 只在放不下时才缩小，放得下就仅调整位置，不牺牲数据表高度。
func (a *application) fitToWorkArea() {
	if a.mw == nil {
		return
	}
	if rc, ok := workAreaFor(a.mw.Handle()); ok {
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

// 窄窗口先让出连接栏空间，关闭助手后恢复。使用逻辑像素以适配 DPI 缩放。
func (a *application) enforceAssistantWidth() {
	if a.mw == nil || a.connectionPane == nil || a.assistant == nil || a.assistant.panel == nil || a.arrangingPanes {
		return
	}
	a.arrangingPanes = true
	defer func() { a.arrangingPanes = false }()
	a.connectionPane.SetVisible(!a.assistant.panel.Visible() || a.mw.ClientBounds().Width >= 1380)
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
	a.assistant.analyze("全面诊断")
}

func (a *application) toggleAssistant() {
	if a.assistant.panel.Visible() {
		a.assistant.stop()
		a.assistant.panel.SetVisible(false)
		a.enforceAssistantWidth()
	} else {
		a.showAssistant()
	}
}
func (a *application) showAssistant() {
	a.arrangingPanes = true
	// 在显示 AI 之前隐藏连接栏，防止 Walk 先把整个窗口扩到三栏最小宽度。
	if a.mw.ClientBounds().Width < 1380 {
		a.connectionPane.SetVisible(false)
	}
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
	if s := fmt.Sprintf("本地  %s\r\n远程  %s\r\nRX %s  /  TX %s\r\n时长  %s", p.LocalAddress, p.RemoteAddress, wincore.FormatBytes(p.RXBytes-b.RXBytes), wincore.FormatBytes(p.TXBytes-b.TXBytes), wincore.FormatDuration(time.Since(p.ConnectedAt))); s != a.lastDetails {
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
func (a *application) showHelp() {
	walk.MsgBox(a.mw, "CommBox 使用说明", "选择左侧工作模式 → 填写参数 → 连接 → 输入报文 → F5 发送。\r\n\r\n客户端列表右键可定向发送、过滤和断开。同 IP 的不同端口按独立会话管理。\r\n未勾选 HEX 时按文本发送。定时 / 循环固定使用启动时的数据和目标。\r\n数据保留最新 10000 条，完整历史自动保存于本地。拖动数据与发送区分隔线调整空间。\r\n\r\nAI 默认关闭。设置服务后，主动分析或追问才提交所选数据。AI 失败不影响通信。\r\nVirtualCOM 免驱动端口可直接在串口模式打开，详见「工具 → VirtualCOM 连接说明」。", walk.MsgBoxOK)
}
