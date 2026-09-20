//go:build windows

// VirtualCOM 图形界面。布局依据功能规格第 3 节。
//
// 与规格的两处差异,都是因为本版没有驱动:
//   - 顶部的「驱动状态」改成「实现方式:用户态(无驱动)」。本方案不装驱动,
//     显示驱动状态是假信息;点击后展示实测的兼容性边界。
//   - 「更多 ⋯」展开成一排文字按钮。规格要求删除用文字标识而非图标,
//     直接摊开比藏进菜单更清楚。
package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"

	"github.com/lxn/win"
	"virtualcom/internal/vcom"
)

const refreshInterval = time.Second

type app struct {
	mw   *walk.MainWindow
	mgr  *vcom.Manager
	host *walk.Composite // 卡片容器

	cards  []*pairCard
	footer *walk.Label

	titleFont *walk.Font
	nameFont  *walk.Font
	dimFont   *walk.Font

	lastFooter string
	closing    bool

	// 启动时清掉的残留编号,记下来供诊断报告说明「那个编号去哪了」。
	cleanedAtStartup []string
	cleanupErr       string
}

// parsePresetPairs 解析命令行里的 --pair COM10:COM11,可重复。
// 用于带预设串口对启动,省得每次开界面都手点一遍。
func parsePresetPairs(args []string) ([][2]string, error) {
	var out [][2]string
	for i := 0; i < len(args); i++ {
		if args[i] != "--pair" {
			continue
		}
		if i+1 >= len(args) {
			return nil, fmt.Errorf("--pair 后面缺少参数,应形如 --pair COM10:COM11")
		}
		i++
		parts := strings.Split(args[i], ":")
		if len(parts) != 2 {
			return nil, fmt.Errorf("串口对格式应为 COM10:COM11,收到 %q", args[i])
		}
		out = append(out, [2]string{strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])})
	}
	return out, nil
}

func main() {
	a := &app{mgr: vcom.NewManager()}
	defer a.mgr.CloseAll()

	presets, presetErr := parsePresetPairs(os.Args[1:])

	a.titleFont, _ = walk.NewFont("Microsoft YaHei UI", 16, walk.FontBold)
	a.nameFont, _ = walk.NewFont("Microsoft YaHei UI", 12, walk.FontBold)
	a.dimFont, _ = walk.NewFont("Microsoft YaHei UI", 9, 0)

	var backendBtn *walk.PushButton
	if err := (MainWindow{
		AssignTo: &a.mw,
		Title:    "VirtualCOM " + vcom.Version,
		MinSize:  Size{Width: 760, Height: 460},
		Size:     Size{Width: 880, Height: 560},
		Layout:   VBox{MarginsZero: false},
		Children: []Widget{
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					Label{Text: "VirtualCOM", Font: Font{Family: "Microsoft YaHei UI", PointSize: 16, Bold: true}},
					HSpacer{},
					PushButton{
						AssignTo:  &backendBtn,
						Text:      "实现方式:用户态(无驱动)",
						MinSize:   Size{Width: 240, Height: 30},
						OnClicked: a.showCompatibility,
					},
				},
			},
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					HSpacer{},
					PushButton{
						Text:      "＋ 创建串口对",
						MinSize:   Size{Width: 140, Height: 32},
						OnClicked: a.onCreate,
					},
				},
			},
			ScrollView{
				Layout: VBox{MarginsZero: true},
				Children: []Widget{
					Composite{
						AssignTo: &a.host,
						Layout:   VBox{MarginsZero: true, SpacingZero: false},
					},
					VSpacer{},
				},
			},
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					Label{AssignTo: &a.footer, Text: "0 个串口对 · 0 个虚拟端口"},
					HSpacer{},
					PushButton{Text: "诊断", MinSize: Size{Width: 80}, OnClicked: a.showDiagnostics},
					PushButton{Text: "刷新", MinSize: Size{Width: 80}, OnClicked: func() { a.refresh(true) }},
				},
			},
		},
	}).Create(); err != nil {
		panic(err)
	}

	a.mw.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		a.closing = true
	})

	// 启动时先清掉自家的残留编号。上次进程崩溃或被强杀时来不及拆除,
	// COM 名会留在系统里而管道早没了,用户看到的是「这个编号被占用」却查不出原因。
	// 只清目标指向本项目管道、且管道确实已不存在的链接,不会碰到正在运行的另一个实例。
	if removed, err := vcom.RemoveStaleLinks(); err != nil {
		a.errorBox("清理残留编号时出错", err.Error())
	} else if len(removed) > 0 {
		a.infoBox("已清理残留编号",
			fmt.Sprintf("上次退出时有编号没来得及释放,已清理:\n\n    %s", strings.Join(removed, "  ")))
	}

	// 预设串口对在窗口建好之后再创建,失败时才有地方弹提示。
	if presetErr != nil {
		a.errorBox("命令行参数有误", presetErr.Error())
	}
	for _, p := range presets {
		if _, err := a.mgr.Create(p[0], p[1]); err != nil {
			a.errorBox("创建预设串口对失败", fmt.Sprintf("%s ⇄ %s:%v", p[0], p[1], err))
		}
	}

	a.refresh(true)
	go a.refreshLoop()

	a.mw.Run()
}

// refreshLoop 周期性刷新状态。刷新只更新文字,不重建控件,
// 否则每秒一次重排会把正在操作的界面搅乱。
func (a *app) refreshLoop() {
	tick := time.NewTicker(refreshInterval)
	defer tick.Stop()
	for range tick.C {
		if a.closing {
			return
		}
		a.mw.Synchronize(func() { a.refresh(false) })
	}
}

// refresh 拉一次快照并同步到界面。rebuild 为真时强制重建卡片。
func (a *app) refresh(rebuild bool) {
	snap := a.mgr.List()

	if rebuild || a.cardSetChanged(snap) {
		a.rebuildCards(snap)
	} else {
		for i, p := range snap.Pairs {
			if i < len(a.cards) {
				a.cards[i].update(p)
			}
		}
	}

	footer := fmt.Sprintf("%d 个串口对 · %d 个虚拟端口", len(snap.Pairs), len(snap.Pairs)*2)
	if footer != a.lastFooter {
		a.footer.SetText(footer)
		a.lastFooter = footer
	}
}

// cardSetChanged 判断串口对的构成(数量或编号)有没有变,只有变了才重建控件。
func (a *app) cardSetChanged(snap vcom.Snapshot) bool {
	if len(snap.Pairs) != len(a.cards) {
		return true
	}
	for i, p := range snap.Pairs {
		c := a.cards[i]
		if c.id != p.ID || c.comA != p.A.Name || c.comB != p.B.Name {
			return true
		}
	}
	return false
}

func (a *app) rebuildCards(snap vcom.Snapshot) {
	a.host.SetSuspended(true)
	defer a.host.SetSuspended(false)

	for _, c := range a.cards {
		c.root.Dispose()
	}
	a.cards = nil

	if len(snap.Pairs) == 0 {
		a.buildEmptyState()
		return
	}
	for _, p := range snap.Pairs {
		if c := a.buildCard(p); c != nil {
			a.cards = append(a.cards, c)
		}
	}
}

// buildEmptyState 没有串口对时的空状态(规格第 3 节)。
func (a *app) buildEmptyState() {
	root, err := walk.NewComposite(a.host)
	if err != nil {
		return
	}
	root.SetLayout(walk.NewVBoxLayout())

	hint, _ := walk.NewLabel(root)
	hint.SetText("还没有虚拟串口对。")
	hint.SetFont(a.nameFont)

	sub, _ := walk.NewLabel(root)
	sub.SetText("点右上角「创建串口对」新建一对相互连通的端口,\n两个串口软件分别打开这两个编号即可互相收发。")
	sub.SetFont(a.dimFont)

	a.cards = append(a.cards, &pairCard{root: root, empty: true})
}

// pairCard 是一对串口在界面上的卡片。两端信息对称展示。
type pairCard struct {
	app        *app
	id         string
	comA, comB string
	empty      bool

	root                  walk.Container // 串口对是 GroupBox(带边框),空状态是 Composite
	aName, aState, aProc  *walk.Label
	aTX                   *walk.Label
	bName, bState, bProc  *walk.Label
	bTX                   *walk.Label
	linkLabel, errLabel   *walk.Label
	enableBtn, detailBtn  *walk.PushButton
	renameBtn, restartBtn *walk.PushButton
	deleteBtn             *walk.PushButton
	lastTexts             map[*walk.Label]string
}

func (a *app) buildCard(p vcom.PairInfo) *pairCard {
	// 用 GroupBox 而不是 Composite:它自带边框,正好是规格要的「一张卡片对应一对串口」。
	root, err := walk.NewGroupBox(a.host)
	if err != nil {
		return nil
	}
	root.SetTitle("") // 只要边框,不要标题
	root.SetLayout(walk.NewVBoxLayout())

	c := &pairCard{
		app: a, id: p.ID, comA: p.A.Name, comB: p.B.Name,
		root: root, lastTexts: map[*walk.Label]string{},
	}

	// 第一行:两端端口名与状态,中间是配对标识
	top, _ := walk.NewComposite(root)
	top.SetLayout(walk.NewHBoxLayout())

	c.aName, _ = walk.NewLabel(top)
	c.aName.SetFont(a.nameFont)
	c.aState, _ = walk.NewLabel(top)

	spacerL, _ := walk.NewLabel(top)
	spacerL.SetText("")
	walk.NewHSpacer(top)

	c.linkLabel, _ = walk.NewLabel(top)
	c.linkLabel.SetText("⇄")
	c.linkLabel.SetFont(a.nameFont)

	walk.NewHSpacer(top)

	c.bState, _ = walk.NewLabel(top)
	c.bName, _ = walk.NewLabel(top)
	c.bName.SetFont(a.nameFont)

	// 第二行:占用程序
	procRow, _ := walk.NewComposite(root)
	procRow.SetLayout(walk.NewHBoxLayout())
	c.aProc, _ = walk.NewLabel(procRow)
	c.aProc.SetFont(a.dimFont)
	walk.NewHSpacer(procRow)
	c.bProc, _ = walk.NewLabel(procRow)
	c.bProc.SetFont(a.dimFont)

	// 第三行:TX
	txRow, _ := walk.NewComposite(root)
	txRow.SetLayout(walk.NewHBoxLayout())
	c.aTX, _ = walk.NewLabel(txRow)
	walk.NewHSpacer(txRow)
	c.bTX, _ = walk.NewLabel(txRow)

	// 异常说明:只有真的有异常时才占位
	c.errLabel, _ = walk.NewLabel(root)
	c.errLabel.SetFont(a.dimFont)

	// 操作行
	btnRow, _ := walk.NewComposite(root)
	btnRow.SetLayout(walk.NewHBoxLayout())
	walk.NewHSpacer(btnRow)

	c.detailBtn = mustButton(btnRow, "详情", func() { a.showDetail(c.id) })
	c.renameBtn = mustButton(btnRow, "修改编号", func() { a.onRename(c.id) })
	c.enableBtn = mustButton(btnRow, "禁用", func() { a.onToggleEnabled(c.id) })
	c.restartBtn = mustButton(btnRow, "重启", func() { a.onRestart(c.id) })
	c.deleteBtn = mustButton(btnRow, "删除", func() { a.onDelete(c.id) })

	c.update(p)
	return c
}

func mustButton(parent walk.Container, text string, onClick func()) *walk.PushButton {
	b, err := walk.NewPushButton(parent)
	if err != nil {
		return nil
	}
	b.SetText(text)
	b.SetMinMaxSize(walk.Size{Width: 88, Height: 28}, walk.Size{})
	b.Clicked().Attach(onClick)
	return b
}

// setText 只在内容真的变化时才写回控件,避免每秒重排。
func (c *pairCard) setText(l *walk.Label, s string) {
	if l == nil {
		return
	}
	if c.lastTexts[l] == s {
		return
	}
	l.SetText(s)
	c.lastTexts[l] = s
}

func (c *pairCard) update(p vcom.PairInfo) {
	if c.empty {
		return
	}
	c.setText(c.aName, p.A.Name)
	c.setText(c.bName, p.B.Name)
	c.setText(c.aState, "· "+p.A.State)
	c.setText(c.bState, p.B.State+" ·")
	c.setText(c.aProc, occupantText(p.A))
	c.setText(c.bProc, occupantText(p.B))
	c.setText(c.aTX, "TX "+humanBytes(p.A.TX))
	c.setText(c.bTX, "TX "+humanBytes(p.B.TX))

	if p.Error != "" {
		c.setText(c.errLabel, "异常:"+p.Error)
	} else {
		c.setText(c.errLabel, "")
	}

	if c.enableBtn != nil {
		want := "禁用"
		if !p.Enabled {
			want = "启用"
		}
		if c.enableBtn.Text() != want {
			c.enableBtn.SetText(want)
		}
	}
}

// occupantText 显示占用程序。取不到时写明原因,不用空白冒充「没人占用」。
func occupantText(p vcom.PortInfo) string {
	switch {
	case p.Process != "":
		return fmt.Sprintf("%s (PID %d)", p.Process, p.PID)
	case p.ProcessNote != "":
		return p.ProcessNote
	case p.State == vcom.StateInUse:
		return "占用程序无法获取"
	default:
		return ""
	}
}

func humanBytes(n uint64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// ---------- 操作 ----------

func (a *app) onCreate() {
	comA, comB, manual, ok := a.askCreate()
	if !ok {
		return
	}
	if !manual {
		comA, comB = "", ""
	}
	info, err := a.mgr.Create(comA, comB)
	if err != nil {
		a.errorBox("创建失败", err.Error())
		return
	}
	a.refresh(true)
	a.infoBox("创建成功", fmt.Sprintf("已创建 %s ⇄ %s\n\n两个串口软件分别打开这两个编号即可互通。",
		info.A.Name, info.B.Name))
}

// askCreate 弹出创建对话框。默认自动选号,可切到手动填写。
func (a *app) askCreate() (comA, comB string, manual, ok bool) {
	var dlg *walk.Dialog
	var acceptPB, cancelPB *walk.PushButton
	var autoRB, manualRB *walk.RadioButton
	var editA, editB *walk.LineEdit
	var hint *walk.Label

	suggestA, suggestB := a.suggestFreePair()

	syncEnabled := func() {
		on := manualRB.Checked()
		editA.SetEnabled(on)
		editB.SetEnabled(on)
	}

	res, _ := (Dialog{
		AssignTo:      &dlg,
		Title:         "创建串口对",
		DefaultButton: &acceptPB,
		CancelButton:  &cancelPB,
		MinSize:       Size{Width: 420, Height: 240},
		Layout:        VBox{},
		Children: []Widget{
			RadioButton{AssignTo: &autoRB, Text: "自动选择空闲编号(推荐)", OnClicked: func() { syncEnabled() }},
			Label{AssignTo: &hint, Text: fmt.Sprintf("当前可用:%s 与 %s", suggestA, suggestB)},
			RadioButton{AssignTo: &manualRB, Text: "手动指定编号", OnClicked: func() { syncEnabled() }},
			Composite{
				Layout: Grid{Columns: 2},
				Children: []Widget{
					Label{Text: "A 端:"},
					LineEdit{AssignTo: &editA, Text: suggestA},
					Label{Text: "B 端:"},
					LineEdit{AssignTo: &editB, Text: suggestB},
				},
			},
			Label{Text: "提交前会检查编号是否冲突。", Font: Font{Family: "Microsoft YaHei UI", PointSize: 9}},
			VSpacer{},
			Composite{
				Layout: HBox{},
				Children: []Widget{
					HSpacer{},
					PushButton{AssignTo: &acceptPB, Text: "创建", OnClicked: func() { dlg.Accept() }},
					PushButton{AssignTo: &cancelPB, Text: "取消", OnClicked: func() { dlg.Cancel() }},
				},
			},
		},
	}).Run(a.mw)

	if res != walk.DlgCmdOK {
		return "", "", false, false
	}
	if manualRB.Checked() {
		return strings.TrimSpace(editA.Text()), strings.TrimSpace(editB.Text()), true, true
	}
	return "", "", false, true
}

// suggestFreePair 给手动模式一个合理的默认值。
func (a *app) suggestFreePair() (string, string) {
	used, err := vcom.UsedCOMNames()
	if err != nil {
		return "COM10", "COM11"
	}
	for _, p := range a.mgr.List().Pairs {
		used[strings.ToUpper(p.A.Name)] = true
		used[strings.ToUpper(p.B.Name)] = true
	}
	var free []string
	for n := 3; n <= 255 && len(free) < 2; n++ {
		name := fmt.Sprintf("COM%d", n)
		if !used[name] {
			free = append(free, name)
		}
	}
	if len(free) < 2 {
		return "COM10", "COM11"
	}
	return free[0], free[1]
}

func (a *app) onRename(id string) {
	p, ok := a.findPair(id)
	if !ok {
		return
	}

	var dlg *walk.Dialog
	var acceptPB, cancelPB *walk.PushButton
	var editA, editB *walk.LineEdit

	res, _ := (Dialog{
		AssignTo:      &dlg,
		Title:         "修改编号",
		DefaultButton: &acceptPB,
		CancelButton:  &cancelPB,
		MinSize:       Size{Width: 400, Height: 200},
		Layout:        VBox{},
		Children: []Widget{
			Label{Text: fmt.Sprintf("当前:%s ⇄ %s", p.A.Name, p.B.Name)},
			Composite{
				Layout: Grid{Columns: 2},
				Children: []Widget{
					Label{Text: "A 端:"},
					LineEdit{AssignTo: &editA, Text: p.A.Name},
					Label{Text: "B 端:"},
					LineEdit{AssignTo: &editB, Text: p.B.Name},
				},
			},
			Label{Text: "配对关系保持不变。任一端正在使用时无法修改。",
				Font: Font{Family: "Microsoft YaHei UI", PointSize: 9}},
			VSpacer{},
			Composite{
				Layout: HBox{},
				Children: []Widget{
					HSpacer{},
					PushButton{AssignTo: &acceptPB, Text: "保存", OnClicked: func() { dlg.Accept() }},
					PushButton{AssignTo: &cancelPB, Text: "取消", OnClicked: func() { dlg.Cancel() }},
				},
			},
		},
	}).Run(a.mw)

	if res != walk.DlgCmdOK {
		return
	}
	if err := a.mgr.Rename(id, strings.TrimSpace(editA.Text()), strings.TrimSpace(editB.Text())); err != nil {
		a.errorBox("修改编号失败", err.Error())
	}
	a.refresh(true)
}

func (a *app) onToggleEnabled(id string) {
	p, ok := a.findPair(id)
	if !ok {
		return
	}
	if p.Enabled {
		if !a.confirm("禁用串口对",
			fmt.Sprintf("将禁用 %s 与 %s。\n\n配置会保留,重新启用后按原编号恢复。", p.A.Name, p.B.Name)) {
			return
		}
	}
	if err := a.mgr.SetEnabled(id, !p.Enabled); err != nil {
		a.errorBox("操作失败", err.Error())
	}
	a.refresh(true)
}

func (a *app) onRestart(id string) {
	p, ok := a.findPair(id)
	if !ok {
		return
	}
	if !a.confirm("重启串口对",
		fmt.Sprintf("将重启 %s 与 %s。\n\n重启会中断通信并清除尚未收发的数据。", p.A.Name, p.B.Name)) {
		return
	}
	if err := a.mgr.Restart(id); err != nil {
		a.errorBox("重启失败", err.Error())
	}
	a.refresh(true)
}

func (a *app) onDelete(id string) {
	p, ok := a.findPair(id)
	if !ok {
		return
	}
	if !a.confirm("删除串口对",
		fmt.Sprintf("将删除这两个端口:\n\n    %s\n    %s\n\n删除后它们立即从系统中消失。", p.A.Name, p.B.Name)) {
		return
	}
	if err := a.mgr.Delete(id); err != nil {
		a.errorBox("删除失败", err.Error())
	}
	a.refresh(true)
}

func (a *app) showDetail(id string) {
	p, ok := a.findPair(id)
	if !ok {
		return
	}
	a.textDialog("串口对详情 —— "+p.A.Name+" ⇄ "+p.B.Name, detailText(p), true)
}

func detailText(p vcom.PairInfo) string {
	var b strings.Builder
	fmt.Fprintf(&b, "串口对 %s    启用:%v    创建时间:%s\r\n",
		p.ID, p.Enabled, p.CreatedAt.Format("2006-01-02 15:04:05"))
	if p.Error != "" {
		fmt.Fprintf(&b, "最近错误:%s\r\n", p.Error)
	}
	b.WriteString("\r\n")

	for _, port := range []vcom.PortInfo{p.A, p.B} {
		fmt.Fprintf(&b, "【%s】状态:%s\r\n", port.Name, port.State)
		who := port.Process
		if who == "" {
			who = "无"
			if port.State == vcom.StateInUse {
				who = "无法获取"
			}
		}
		fmt.Fprintf(&b, "  占用程序:%s\r\n", who)
		if port.ProcessNote != "" {
			fmt.Fprintf(&b, "  说明:%s\r\n", port.ProcessNote)
		}
		fmt.Fprintf(&b, "  PID:%s\r\n", pidText(port))
		fmt.Fprintf(&b, "  发送 TX:%d 字节    接收 RX:%d 字节\r\n", port.TX, port.RX)
		fmt.Fprintf(&b, "  读取次数:%d    写入次数:%d    错误:%d\r\n", port.Reads, port.Writes, port.Errors)
		fmt.Fprintf(&b, "  待收缓存:%d / %d 字节    已丢弃:%d 字节\r\n",
			port.BufferUsed, port.BufferCapacity, port.BufferDropped)
		b.WriteString("\r\n")
	}

	b.WriteString("串口参数与控制信号:本版不提供。\r\n")
	b.WriteString("用户态实现无法响应 GetCommState/SetCommState 等串口专用 API,\r\n")
	b.WriteString("因此没有波特率、数据位、校验、停止位,也没有 RTS/CTS/DTR/DSR/DCD。\r\n")
	return b.String()
}

func pidText(p vcom.PortInfo) string {
	if p.PID == 0 {
		if p.State == vcom.StateInUse {
			return "无法获取"
		}
		return "无"
	}
	return fmt.Sprintf("%d", p.PID)
}

func (a *app) showDiagnostics() {
	a.textDialog("诊断报告", strings.ReplaceAll(a.mgr.Diagnose(), "\n", "\r\n"), true)
}

func (a *app) showCompatibility() {
	text := strings.Join([]string{
		"实现方式:用户态,不安装驱动、不需要任何签名、不依赖第三方组件。",
		"",
		"能用(已在真机实测):",
		"    CreateFile 打开端口,跨进程可见",
		"    ReadFile / WriteFile 双向收发,支持全双工",
		"    Overlapped 异步 I/O、CancelIoEx 取消",
		"    占用程序名与 PID、缓存用量与收发统计",
		"    多组串口并行,互不串组",
		"",
		"不能用:",
		"    GetCommState / SetCommState",
		"    GetCommTimeouts / SetCommTimeouts",
		"    SetCommMask / WaitCommEvent",
		"    PurgeComm / ClearCommError",
		"    以上一律返回 ERROR_INVALID_FUNCTION",
		"    GetFileType 返回 FILE_TYPE_PIPE,真串口是 FILE_TYPE_CHAR",
		"    无波特率 / 数据位 / 校验 / 停止位设置",
		"    无 RTS / CTS / DTR / DSR / DCD / BREAK",
		"    设备管理器不显示这些端口",
		"",
		"直接后果:",
		"    凡是用 .NET SerialPort、pyserial、go.bug.st/serial 打开端口的软件,",
		"    会在打开阶段直接失败。只有把端口当字节流读写的程序能用。",
		"",
		"原因:",
		"    内核里的命名管道驱动不处理串口 IOCTL,用户态代码改变不了这一点。",
		"    要消除上述限制必须安装内核驱动,而 Windows x64 只加载带可信签名的",
		"    驱动,这与本版不装驱动、不签名的选型互斥。",
	}, "\r\n")
	a.textDialog("兼容性边界", text, true)
}

// textDialog 弹一个只读文本窗口,可一键复制(规格 2.9)。
func (a *app) textDialog(title, text string, copyable bool) {
	var dlg *walk.Dialog
	var closePB, copyPB *walk.PushButton
	var edit *walk.TextEdit

	children := []Widget{
		TextEdit{AssignTo: &edit, Text: text, ReadOnly: true, VScroll: true,
			Font: Font{Family: "Consolas", PointSize: 10}},
	}
	buttons := []Widget{HSpacer{}}
	if copyable {
		buttons = append(buttons, PushButton{AssignTo: &copyPB, Text: "复制全部", OnClicked: func() {
			if err := walk.Clipboard().SetText(edit.Text()); err != nil {
				a.errorBox("复制失败", err.Error())
				return
			}
			a.infoBox("已复制", "内容已复制到剪贴板。")
		}})
	}
	buttons = append(buttons, PushButton{AssignTo: &closePB, Text: "关闭", OnClicked: func() { dlg.Accept() }})
	children = append(children, Composite{Layout: HBox{}, Children: buttons})

	(Dialog{
		AssignTo:      &dlg,
		Title:         title,
		DefaultButton: &closePB,
		CancelButton:  &closePB,
		MinSize:       Size{Width: 680, Height: 460},
		Layout:        VBox{},
		Children:      children,
	}).Run(a.mw)
}

func (a *app) findPair(id string) (vcom.PairInfo, bool) {
	for _, p := range a.mgr.List().Pairs {
		if p.ID == id {
			return p, true
		}
	}
	return vcom.PairInfo{}, false
}

func (a *app) confirm(title, msg string) bool {
	return walk.MsgBox(a.mw, title, msg,
		walk.MsgBoxYesNo|walk.MsgBoxIconQuestion) == win.IDYES
}

func (a *app) errorBox(title, msg string) {
	walk.MsgBox(a.mw, title, msg, walk.MsgBoxIconError)
}

func (a *app) infoBox(title, msg string) {
	walk.MsgBox(a.mw, title, msg, walk.MsgBoxIconInformation)
}
