//go:build windows

package main

import (
	"fmt"
	"io"
	"net"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/lxn/walk"
	"github.com/lxn/win"
)

// layoutDisplay 是一种屏幕与缩放组合下的可用工作区,单位是逻辑像素(96 DPI),
// 已按 Windows 11 减去 48 的任务栏。Windows 只提供有效分辨率不低于约 1024×600
// 的缩放档,所以标准缩放下最紧的工作区约为 1024×552。
type layoutDisplay struct {
	name string
	w, h int
}

var layoutDisplays = []layoutDisplay{
	{"有效 1024x600(最紧)", 1024, 552},
	{"1600x900 @150%", 1067, 552},
	{"1366x768 @125%", 1093, 566},
	{"1920x1080 @175%", 1097, 569},
	{"1280x800 @125%", 1024, 592},
	{"1366x768 @100%", 1366, 720},
	{"1920x1080 @150%", 1280, 672},
	{"1920x1080 @125%", 1536, 816},
	{"1920x1080 @100%", 1920, 1032},
}

// 发送框至少要完整显示这么多行,数据表至少要显示这么多行。
const (
	minSendLines = 3
	minTableRows = 3
)

type layoutReport struct {
	display      layoutDisplay
	dpi          int
	winW, winH   int // 窗口实际外框,逻辑像素
	overW        int // 超出工作区的宽度,逻辑像素
	overH        int
	clientH      int // 客户区与数据表、发送框的高度,逻辑像素,调布局时看余量
	tableH       int
	sendH        int
	sendLines    int
	tableRows    int
	unusedTableW int // 可视区不应留给空白或过宽的元数据列。
	dataShare    int // 数据栏占「数据栏 + 发送区」高度的百分比
	middleShare  int // 中栏占客户区宽度的百分比
	problems     []string
}

func (r layoutReport) String() string {
	return fmt.Sprintf("%-26s dpi=%d 窗口 %4dx%-4d 超出 %3dx%-3d 客户区高 %3d 数据表高 %3d(%d 行) 发送框高 %3d(%d 行) 数据栏 %d%% 中栏 %d%% 问题 %d",
		r.display.name, r.dpi, r.winW, r.winH, r.overW, r.overH, r.clientH, r.tableH, r.tableRows, r.sendH, r.sendLines, r.dataShare, r.middleShare, len(r.problems))
}

func TestLayoutFitsCommonDisplays(t *testing.T) {
	runLayoutMatrix(t, nil)
}

// DPI 无感知的窗口按 96 DPI 排版,由系统整体放大,等同 100% 缩放下的排版结果。
func TestLayoutFitsCommonDisplaysAt96DPI(t *testing.T) {
	setAwareness := syscall.NewLazyDLL("user32.dll").NewProc("SetThreadDpiAwarenessContext")
	if setAwareness.Find() != nil {
		t.Skip("SetThreadDpiAwarenessContext unavailable")
	}
	var previous uintptr
	runLayoutMatrix(t, func() {
		previous, _, _ = setAwareness.Call(^uintptr(0)) // DPI_AWARENESS_CONTEXT_UNAWARE
		t.Cleanup(func() { setAwareness.Call(previous) })
	})
}

func runLayoutMatrix(t *testing.T, beforeCreate func()) {
	runLayoutCases(t, beforeCreate, nil, layoutDisplays)
}

// 每种模式的表单与发送目标高度不同；助手展开后还会切换左右栏。
func TestLayoutModesFitCompactWorkspace(t *testing.T) {
	for index, name := range modes {
		t.Run(name, func(t *testing.T) {
			runLayoutCases(t, nil, func(a *application) {
				a.mode.SetCurrentIndex(index)
				a.updateMode()
			}, layoutDisplays[:1])
		})
	}
	t.Run("AI 助手", func(t *testing.T) {
		runLayoutCases(t, nil, func(a *application) { a.showAssistant() }, layoutDisplays[:1])
	})
	t.Run("更多筛选", func(t *testing.T) {
		runLayoutCases(t, nil, func(a *application) { a.toggleAdvancedFilters() }, layoutDisplays[:1])
	})
	t.Run("长报文与名称", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				go func() { defer c.Close(); io.Copy(io.Discard, c) }()
			}
		}()
		runLayoutCases(t, nil, func(a *application) { fillLongDropDowns(t, a, ln.Addr().String()) }, layoutDisplays[:1])
	})
}

// fillLongDropDowns 走真实路径连上 TCP、发一条 120 字节报文、存一个长快捷名称，
// 让发送历史、快捷、最近连接和发送目标都带上长选项。walk 按最长选项定下拉框
// 最小宽度，未处理时一条长报文就能把窗口撑到两三千像素宽。
func fillLongDropDowns(t *testing.T, a *application, address string) {
	host, port, _ := net.SplitHostPort(address)
	a.netIP.SetText(host)
	a.netPort.SetText(port)
	cfg, err := a.config()
	if err == nil {
		err = a.engine.Connect(cfg)
	}
	if err != nil {
		t.Error(err)
		return
	}
	t.Cleanup(a.engine.Disconnect)
	long := strings.TrimSpace(strings.Repeat("01 10 00 00 ", 30))
	if err := a.sendCaptured(long, true, "", "", ""); err != nil {
		t.Error(err)
		return
	}
	if err := a.engine.SaveFavorite("Modbus 写多个保持寄存器 0x0000-0x0063 全部通道", long); err != nil {
		t.Error(err)
		return
	}
	a.refreshSendHistory()
	a.refreshFavorites()
	a.refreshRecentConn()
	a.refreshConnections()
	if w := int(a.sendHistory.SendMessage(win.CB_GETDROPPEDWIDTH, 0, 0)); w < walk.IntFrom96DPI(360, a.sendHistory.DPI()) {
		t.Errorf("发送历史展开列表只有 %d px,长报文看不全", w)
	}
}

// driveWorkbench 用窗口真正的消息循环驱动 steps:walk 在后台算布局,消息循环里才把
// 结果应用到控件上。steps 在另一个 goroutine 里执行,界面操作都要经 onUI 回到界面线程。
func driveWorkbench(a *application, steps func(onUI func(func()))) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer a.mw.Synchronize(func() { win.PostQuitMessage(0) })
		steps(func(f func()) {
			finished := make(chan struct{})
			a.mw.Synchronize(func() { f(); close(finished) })
			<-finished
		})
	}()
	a.mw.Run()
	<-done
}

// fitWorkbench 模拟启动:先回到设计尺寸,再按工作区收缩,等布局应用后再返回。
// 运行中的收窗检查也改按这块模拟屏幕判断,否则会把窗口拉回本机真实屏幕。
func fitWorkbench(a *application, onUI func(func()), work win.RECT) {
	onUI(func() {
		a.workArea = func() (win.RECT, bool) { return work, true }
		_ = a.mw.SetSize(walk.Size{Width: 1280, Height: 820})
	})
	time.Sleep(300 * time.Millisecond)
	onUI(func() { a.fitIntoWorkArea(work) })
	time.Sleep(700 * time.Millisecond)
}

// 系统默认把顶层窗口的尺寸上限定在屏幕加边框(WM_GETMINMAXINFO 的 ptMaxTrackSize),
// walk 只改下限。测试里子类化窗口过程放开上限,窗口就能比本机屏幕大,
// 小屏或高缩放的测试机也能量 1920x1080 @100% 这类大屏组合,不必跳过。
var (
	oversizePrev = map[win.HWND]uintptr{}
	oversizeProc = syscall.NewCallback(func(h win.HWND, msg uint32, wp, lp uintptr) uintptr {
		r := win.CallWindowProc(oversizePrev[h], h, msg, wp, lp)
		if msg == win.WM_GETMINMAXINFO {
			// lParam 是系统给的结构体地址;经 &lp 转换,vet 不把它当成可能失效的 uintptr 指针。
			(*(**win.MINMAXINFO)(unsafe.Pointer(&lp))).PtMaxTrackSize = win.POINT{X: 1 << 14, Y: 1 << 14}
		}
		return r
	})
)

func allowOversizeWindow(h win.HWND) {
	if _, ok := oversizePrev[h]; !ok {
		oversizePrev[h] = win.SetWindowLongPtr(h, win.GWLP_WNDPROC, oversizeProc)
	}
}

func runLayoutCases(t *testing.T, beforeCreate func(), setup func(*application), displays []layoutDisplay) {
	t.Helper()
	a := newWorkbenchForTestWith(t, beforeCreate)
	var (
		reports []layoutReport
		dpi     int
		aware   string
	)
	driveWorkbench(a, func(onUI func(func())) {
		var real win.RECT
		onUI(func() {
			allowOversizeWindow(a.mw.Handle())
			if setup != nil {
				setup(a)
			}
			win.ShowWindow(a.mw.Handle(), win.SW_SHOWNOACTIVATE)
			real, _ = workAreaFor(a.mw.Handle())
			dpi, aware = a.mw.DPI(), threadDPIAwareness()
		})
		for _, d := range displays {
			// 比本机屏幕大的工作区照样从屏幕左上角起算,窗口伸到屏幕外也照常排版与测量。
			work := win.RECT{Left: real.Left, Top: real.Top, Right: real.Left + px(d.w, dpi), Bottom: real.Top + px(d.h, dpi)}
			fitWorkbench(a, onUI, work)
			onUI(func() { reports = append(reports, measureLayout(a, d, work, dpi)) })
		}
	})

	t.Logf("DPI %d(线程 DPI 感知:%s)", dpi, aware)
	var failed bool
	for _, r := range reports {
		t.Log(r)
		for _, p := range r.problems {
			t.Log("    " + p)
		}
		if r.overW > 0 || r.overH > 0 || r.sendLines < minSendLines || r.tableRows < minTableRows || len(r.problems) > 0 {
			failed = true
		}
		if r.unusedTableW > 24 {
			t.Errorf("%s: 数据列没有利用 %d px 的可用宽度", r.display.name, r.unusedTableW)
		}
	}
	if failed {
		t.Errorf("有显示组合不满足:窗口放进工作区、发送框 ≥ %d 行、数据表 ≥ %d 行、控件不重叠不裁切", minSendLines, minTableRows)
	}
}

func measureLayout(a *application, d layoutDisplay, work win.RECT, dpi int) layoutReport {
	r := layoutReport{display: d, dpi: dpi}
	wr := windowRect(a.mw.Handle())
	r.winW, r.winH = lg(wr.Right-wr.Left, dpi), lg(wr.Bottom-wr.Top, dpi)
	r.overW = max(0, lg(wr.Right-work.Right, dpi))
	r.overH = max(0, lg(wr.Bottom-work.Bottom, dpi))
	cr := clientRectOnScreen(a.mw.Handle())
	r.clientH = lg(cr.Bottom-cr.Top, dpi)
	tr, sr := windowRect(a.packetTable.Handle()), windowRect(a.sendEdit.Handle())
	r.tableH, r.sendH = lg(tr.Bottom-tr.Top, dpi), lg(sr.Bottom-sr.Top, dpi)
	r.sendLines = visibleLines(a.sendEdit.Handle(), work.Bottom)
	r.tableRows = tableRowsPerPage(a.packetTable)
	used := 0
	for i := 0; i < a.packetTable.Columns().Len(); i++ {
		col := a.packetTable.Columns().At(i)
		if col.Visible() {
			used += col.Width()
		}
	}
	r.unusedTableW = lg(tr.Right-tr.Left, dpi) - used
	if a.packetTable.Columns().At(4).Width() > 80 {
		r.problems = append(r.problems, "长度列占用了应留给报文的宽度")
	}
	dr, sr2, mr := windowRect(a.dataPane.Handle()), windowRect(a.sendPane.Handle()), windowRect(a.monitorPane.Handle())
	data, send := int(dr.Bottom-dr.Top), int(sr2.Bottom-sr2.Top)
	middle, client := int(mr.Right-mr.Left), int(cr.Right-cr.Left)
	r.dataShare, r.middleShare = data*100/max(1, data+send), middle*100/max(1, client)
	// 用户展开更多筛选或更多发送时以展开内容为准,不要求比例。
	if !a.advancedFilters.Visible() && !a.sendExtrasOpen && data*100 < (data+send)*dataSharePercent {
		r.problems = append(r.problems, fmt.Sprintf("数据栏只占数据与发送合计高度的 %.1f%%", float64(data*100)/float64(data+send)))
	}
	if middle*100 < client*dataSharePercent {
		r.problems = append(r.problems, fmt.Sprintf("中栏只占客户区宽度的 %.1f%%", float64(middle*100)/float64(client)))
	}
	r.problems = append(r.problems, layoutProblems(a, dpi)...)
	return r
}

// 矮屏上历史 / 快捷与定时 / 循环两行收进「更多发送」,按需展开;收起时按钮提示
// 定时仍在进行;工作区放得下设计尺寸时两行常驻,按钮隐藏。
func TestSendExtrasFollowAvailableHeight(t *testing.T) {
	a := newWorkbenchForTestWith(t, nil)
	var errs []string
	driveWorkbench(a, func(onUI func(func())) {
		var real win.RECT
		var dpi int
		onUI(func() {
			allowOversizeWindow(a.mw.Handle())
			win.ShowWindow(a.mw.Handle(), win.SW_SHOWNOACTIVATE)
			real, _ = workAreaFor(a.mw.Handle())
			dpi = a.mw.DPI()
		})
		workFor := func(d layoutDisplay) win.RECT {
			return win.RECT{Left: real.Left, Top: real.Top, Right: real.Left + px(d.w, dpi), Bottom: real.Top + px(d.h, dpi)}
		}
		// text 为空表示按钮隐藏,文字无所谓。
		expect := func(step string, open, toggle bool, text string) {
			time.Sleep(300 * time.Millisecond)
			onUI(func() {
				gotText := a.sendExtrasToggle.Text()
				if text == "" {
					gotText = ""
				}
				got := fmt.Sprintf("两行显示=%v 按钮显示=%v 按钮文字=%q", a.sendExtras.Visible(), a.sendExtrasToggle.Visible(), gotText)
				want := fmt.Sprintf("两行显示=%v 按钮显示=%v 按钮文字=%q", open, toggle, text)
				if got != want {
					errs = append(errs, step+": "+got+",应为 "+want)
				}
			})
		}
		fitWorkbench(a, onUI, workFor(layoutDisplays[0]))
		expect("最紧工作区", false, true, "更多发送")
		onUI(a.toggleSendExtras)
		expect("点更多发送", true, true, "收起发送")
		onUI(a.toggleSendExtras)
		expect("点收起发送", false, true, "更多发送")
		onUI(func() {
			a.timerMu.Lock()
			a.timerCancel = make(chan struct{})
			a.timerMu.Unlock()
			a.updateSendExtrasToggle()
		})
		expect("收起时定时进行中", false, true, "定时中")
		onUI(func() { a.stopTimer(false) })
		expect("定时停止", false, true, "更多发送")
		fitWorkbench(a, onUI, workFor(layoutDisplays[len(layoutDisplays)-1]))
		expect("1920x1080 @100%(放得下设计尺寸)", true, false, "")
	})
	for _, e := range errs {
		t.Error(e)
	}
}

// windowFits 判断窗口外框是否没超出工作区的宽高(位置不论)。
func windowFits(h win.HWND, work win.RECT) bool {
	wr := windowRect(h)
	return wr.Right-wr.Left <= work.Right-work.Left && wr.Bottom-wr.Top <= work.Bottom-work.Top
}

// 800x600 这类连「左栏 + 中栏」都放不下的屏幕:三栏一次只显示一栏,
// 中栏标题行的「连接配置」与左栏的「返回数据」切换,侧栏占满窗口,窗口始终放得下。
func TestNarrowScreenShowsOnePaneAtATime(t *testing.T) {
	a := newWorkbenchForTestWith(t, nil)
	var errs []string
	driveWorkbench(a, func(onUI func(func())) {
		var real win.RECT
		var dpi int
		onUI(func() {
			allowOversizeWindow(a.mw.Handle())
			win.ShowWindow(a.mw.Handle(), win.SW_SHOWNOACTIVATE)
			real, _ = workAreaFor(a.mw.Handle())
			dpi = a.mw.DPI()
		})
		screen := layoutDisplay{"800x600 @100%", 800, 552}
		work := win.RECT{Left: real.Left, Top: real.Top, Right: real.Left + px(screen.w, dpi), Bottom: real.Top + px(screen.h, dpi)}
		// buttons 表示窄屏切换按钮是否启用;按钮所在的栏隐藏时它自然也不可见。
		expect := func(step string, conn, monitor, ai, buttons bool) {
			time.Sleep(500 * time.Millisecond)
			onUI(func() {
				format := "连接栏=%v 中栏=%v AI=%v 「连接配置」=%v 「返回数据」=%v 放得下=%v"
				got := fmt.Sprintf(format, a.connectionPane.Visible(), a.monitorPane.Visible(), a.assistant.panel.Visible(), a.connectionPageButton.Visible(), a.dataPageButton.Visible(), windowFits(a.mw.Handle(), work))
				want := fmt.Sprintf(format, conn, monitor, ai, buttons && monitor, buttons && conn, true)
				if got != want {
					errs = append(errs, step+": "+got+",应为 "+want)
				}
				for _, p := range layoutProblems(a, dpi) {
					errs = append(errs, step+": "+p)
				}
				// 侧栏单独显示时占满客户区(减去左右边距 20)。
				client := lg(clientRectOnScreen(a.mw.Handle()).Right-clientRectOnScreen(a.mw.Handle()).Left, dpi)
				for _, pane := range []*walk.ScrollView{a.connectionPane, a.assistant.panel} {
					if pane.Visible() && !monitor {
						if w := lg(windowRect(pane.Handle()).Right-windowRect(pane.Handle()).Left, dpi); w < client-24 {
							errs = append(errs, fmt.Sprintf("%s: 侧栏只有 %d 宽,客户区 %d", step, w, client))
						}
					}
				}
			})
		}
		fitWorkbench(a, onUI, work)
		onUI(func() {
			r := measureLayout(a, screen, work, dpi)
			t.Log(r)
			if r.overW > 0 || r.overH > 0 || r.sendLines < minSendLines || r.tableRows < minTableRows || len(r.problems) > 0 {
				errs = append(errs, fmt.Sprintf("800x600 数据页: %v %v", r, r.problems))
			}
		})
		expect("800x600 默认显示中栏", false, true, false, true)
		onUI(a.showConnectionPage)
		expect("点「连接配置」", true, false, false, true)
		onUI(a.showDataPage)
		expect("点「返回数据」", false, true, false, true)
		onUI(a.showAssistant)
		expect("展开 AI", false, false, true, true)
		onUI(a.toggleAssistant)
		expect("关闭 AI", false, true, false, true)
		onUI(a.showConnectionPage)
		work = win.RECT{Left: real.Left, Top: real.Top, Right: real.Left + px(1024, dpi), Bottom: real.Top + px(552, dpi)}
		fitWorkbench(a, onUI, work)
		expect("回到 1024 宽", true, true, false, false)
	})
	for _, e := range errs {
		t.Error(e)
	}
}

// 运行中工作区变小(换屏、改分辨率、任务栏变高)或窗口被改得比屏幕大时收回来;
// 放得下的窗口哪怕被拖到半出屏幕也不动。
func TestWindowStaysWithinWorkArea(t *testing.T) {
	a := newWorkbenchForTestWith(t, nil)
	var errs []string
	driveWorkbench(a, func(onUI func(func())) {
		var real win.RECT
		var dpi int
		onUI(func() {
			allowOversizeWindow(a.mw.Handle())
			win.ShowWindow(a.mw.Handle(), win.SW_SHOWNOACTIVATE)
			real, _ = workAreaFor(a.mw.Handle())
			dpi = a.mw.DPI()
		})
		area := func(w, h int) win.RECT {
			return win.RECT{Left: real.Left, Top: real.Top, Right: real.Left + px(w, dpi), Bottom: real.Top + px(h, dpi)}
		}
		var work win.RECT
		// change 模拟一次显示变化:换掉工作区后发出 msg。
		change := func(step string, w, h int, msg uint32, wp uintptr) {
			work = area(w, h)
			onUI(func() {
				a.workArea = func() (win.RECT, bool) { return work, true }
				win.SendMessage(a.mw.Handle(), msg, wp, 0)
			})
			time.Sleep(700 * time.Millisecond)
			onUI(func() {
				if !windowFits(a.mw.Handle(), work) {
					wr := windowRect(a.mw.Handle())
					errs = append(errs, fmt.Sprintf("%s: 窗口 %dx%d 超出工作区 %dx%d", step, lg(wr.Right-wr.Left, dpi), lg(wr.Bottom-wr.Top, dpi), w, h))
				}
			})
		}
		fitWorkbench(a, onUI, area(1920, 1032))
		change("分辨率改小到 1024x552", 1024, 552, win.WM_DISPLAYCHANGE, 32)
		change("任务栏变高,工作区只剩 800x552", 800, 552, win.WM_SETTINGCHANGE, 0x002F)
		onUI(func() {
			if !a.narrow || a.connectionPane.Visible() {
				errs = append(errs, "工作区 800 宽时没有切到窄屏单栏")
			}
		})
		change("回到 1920x1032", 1920, 1032, win.WM_DISPLAYCHANGE, 32)
		onUI(func() {
			if a.narrow || !a.connectionPane.Visible() {
				errs = append(errs, "回到宽屏后左栏没有恢复")
			}
		})
		// 程序或系统把窗口改得比工作区大:窗口位置改变之后收回来。
		onUI(func() {
			_ = a.mw.SetBoundsPixels(walk.Rectangle{X: int(work.Left), Y: int(work.Top), Width: int(work.Right - work.Left + px(200, dpi)), Height: int(work.Bottom - work.Top + px(100, dpi))})
		})
		time.Sleep(700 * time.Millisecond)
		onUI(func() {
			if !windowFits(a.mw.Handle(), work) {
				errs = append(errs, "窗口被改得比工作区大后没有收回")
			}
		})
		// 放得下的窗口被拖到一半出屏幕:保持用户的位置。
		var before walk.Rectangle
		onUI(func() {
			b := a.mw.BoundsPixels()
			before = walk.Rectangle{X: int(work.Right) - b.Width/2, Y: b.Y, Width: min(b.Width, int(px(1280, dpi))), Height: min(b.Height, int(px(820, dpi)))}
			_ = a.mw.SetBoundsPixels(before)
		})
		time.Sleep(700 * time.Millisecond)
		onUI(func() {
			if b := a.mw.BoundsPixels(); b != before {
				errs = append(errs, fmt.Sprintf("放得下的窗口被挪动了: %v → %v", before, b))
			}
		})
	})
	for _, e := range errs {
		t.Error(e)
	}
}

func TestLayoutDataDisplayModesUseAvailableWidth(t *testing.T) {
	for index, name := range []string{"HEX + ASCII", "HEX", "ASCII"} {
		t.Run(name, func(t *testing.T) {
			runLayoutCases(t, nil, func(a *application) {
				a.displayMode.SetCurrentIndex(index)
				a.updateDisplay()
			}, layoutDisplays[5:])
		})
	}
}

func threadDPIAwareness() string {
	user32 := syscall.NewLazyDLL("user32.dll")
	getContext, getAwareness := user32.NewProc("GetThreadDpiAwarenessContext"), user32.NewProc("GetAwarenessFromDpiAwarenessContext")
	if getContext.Find() != nil || getAwareness.Find() != nil {
		return "未知"
	}
	ctx, _, _ := getContext.Call()
	aw, _, _ := getAwareness.Call(ctx)
	switch aw {
	case 0:
		return "无感知"
	case 1:
		return "系统级"
	case 2:
		return "按显示器"
	}
	return fmt.Sprint(aw)
}
func px(logical, dpi int) int32    { return int32((logical*dpi + 48) / 96) }
func lg(pixels int32, dpi int) int { return (int(pixels)*96 + dpi/2) / dpi }

func windowRect(h win.HWND) win.RECT {
	var r win.RECT
	win.GetWindowRect(h, &r)
	return r
}

func clientRectOnScreen(h win.HWND) win.RECT {
	var r win.RECT
	win.GetClientRect(h, &r)
	tl := win.POINT{X: r.Left, Y: r.Top}
	br := win.POINT{X: r.Right, Y: r.Bottom}
	win.ClientToScreen(h, &tl)
	win.ClientToScreen(h, &br)
	return win.RECT{Left: tl.X, Top: tl.Y, Right: br.X, Bottom: br.Y}
}

// visibleLines 数发送框里能完整看到的文字行数:排版区域先被控件自身、
// 再被工作区下沿截断(窗口超出屏幕时,底下几行其实看不到)。
func visibleLines(edit win.HWND, clipBottom int32) int {
	var fr win.RECT
	win.SendMessage(edit, win.EM_GETRECT, 0, uintptr(unsafe.Pointer(&fr)))
	top := win.POINT{X: fr.Left, Y: fr.Top}
	win.ClientToScreen(edit, &top)
	bottom := top.Y + (fr.Bottom - fr.Top)
	if cb := clientRectOnScreen(edit).Bottom; cb < bottom {
		bottom = cb
	}
	if clipBottom < bottom {
		bottom = clipBottom
	}
	lh := textLineHeight(edit)
	if lh <= 0 || bottom <= top.Y {
		return 0
	}
	return int((bottom - top.Y) / lh)
}

func textLineHeight(h win.HWND) int32 {
	hdc := win.GetDC(h)
	defer win.ReleaseDC(h, hdc)
	if font := win.HGDIOBJ(win.SendMessage(h, win.WM_GETFONT, 0, 0)); font != 0 {
		old := win.SelectObject(hdc, font)
		defer win.SelectObject(hdc, old)
	}
	var tm win.TEXTMETRIC
	if !win.GetTextMetrics(hdc, &tm) {
		return 0
	}
	return tm.TmHeight
}

func tableRowsPerPage(tv *walk.TableView) int {
	var best win.HWND
	var bestW int32
	for _, h := range childWindows(tv.Handle()) {
		if className(h) == "SysListView32" && win.IsWindowVisible(h) {
			if r := windowRect(h); r.Right-r.Left > bestW {
				best, bestW = h, r.Right-r.Left
			}
		}
	}
	if best == 0 {
		return 0
	}
	return int(win.SendMessage(best, win.LVM_GETCOUNTPERPAGE, 0, 0))
}

var (
	enumChildResult []win.HWND
	enumChildProc   = syscall.NewCallback(func(h win.HWND, _ uintptr) uintptr {
		enumChildResult = append(enumChildResult, h)
		return 1
	})
)

func childWindows(parent win.HWND) []win.HWND {
	enumChildResult = nil
	win.EnumChildWindows(parent, enumChildProc, 0)
	return append([]win.HWND(nil), enumChildResult...)
}

func className(h win.HWND) string {
	buf := make([]uint16, 64)
	n, _ := win.GetClassName(h, &buf[0], len(buf))
	return syscall.UTF16ToString(buf[:n])
}

func windowText(h win.HWND) string {
	buf := make([]uint16, 48)
	n := win.SendMessage(h, win.WM_GETTEXT, uintptr(len(buf)), uintptr(unsafe.Pointer(&buf[0])))
	return syscall.UTF16ToString(buf[:n])
}

func describe(h win.HWND, dpi int) string {
	r := windowRect(h)
	name := className(h)
	if text := strings.TrimSpace(windowText(h)); text != "" {
		name += " \"" + text + "\""
	}
	return fmt.Sprintf("%s %dx%d", name, lg(r.Right-r.Left, dpi), lg(r.Bottom-r.Top, dpi))
}

func isGroupBoxFrame(h win.HWND) bool {
	return className(h) == "Button" && win.GetWindowLong(h, win.GWL_STYLE)&0xF == win.BS_GROUPBOX
}

// layoutProblems 检查所有可见控件:超出父控件客户区(被裁切)、同级互相重叠。
// 落到工作区外由窗口的超出量统一体现,不再逐个控件列出。滚动面板只纵向滚动,
// 内容比视口高不算问题,比视口宽(含压在滚动条下)就是右侧被裁掉;分组框的
// 边框与标签页控件按设计与同级内容重叠,这几种不算问题。
func layoutProblems(a *application, dpi int) []string {
	var problems []string
	root := a.mw.Handle()
	scrollParents := map[win.HWND]bool{a.connectionPane.Handle(): true}
	if a.assistant != nil && a.assistant.panel != nil {
		scrollParents[a.assistant.panel.Handle()] = true
	}
	byParent := map[win.HWND][]win.HWND{}
	for _, h := range childWindows(root) {
		if win.IsWindowVisible(h) {
			p := win.GetParent(h)
			byParent[p] = append(byParent[p], h)
		}
	}
	const slack = 1
	// walk 从内层容器宽度里扣掉滚动条；逐个控件看是否伸到可视区外。
	for sv := range scrollParents {
		vr := clientRectOnScreen(sv)
		for _, h := range childWindows(sv) {
			if r := windowRect(h); win.IsWindowVisible(h) && win.GetParent(h) != sv && r.Right > r.Left && (r.Left < vr.Left-slack || r.Right > vr.Right+slack) {
				problems = append(problems, "超出滚动面板可视宽度: "+describe(h, dpi))
			}
		}
	}
	for p, kids := range byParent {
		pr := clientRectOnScreen(p)
		for _, h := range kids {
			r := windowRect(h)
			if r.Right-r.Left <= 0 || r.Bottom-r.Top <= 0 {
				continue
			}
			if !scrollParents[p] && (r.Left < pr.Left-slack || r.Top < pr.Top-slack || r.Right > pr.Right+slack || r.Bottom > pr.Bottom+slack) {
				problems = append(problems, "被父控件裁切: "+describe(h, dpi)+" 在 "+describe(p, dpi))
			}
		}
		for i := range kids {
			for j := i + 1; j < len(kids); j++ {
				hi, hj := kids[i], kids[j]
				if isGroupBoxFrame(hi) || isGroupBoxFrame(hj) || className(hi) == "SysTabControl32" || className(hj) == "SysTabControl32" {
					continue
				}
				ri, rj := windowRect(hi), windowRect(hj)
				if ri.Left < rj.Right-slack && rj.Left < ri.Right-slack && ri.Top < rj.Bottom-slack && rj.Top < ri.Bottom-slack {
					problems = append(problems, "重叠: "+describe(hi, dpi)+" 与 "+describe(hj, dpi))
				}
			}
		}
	}
	return problems
}
