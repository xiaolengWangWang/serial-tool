//go:build windows

package main

import (
	"fmt"
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
	minSendLines = 4
	minTableRows = 3
)

type layoutReport struct {
	display    layoutDisplay
	dpi        int
	winW, winH int // 窗口实际外框,逻辑像素
	overW      int // 超出工作区的宽度,逻辑像素
	overH      int
	clientH    int // 客户区与数据表、发送框的高度,逻辑像素,调布局时看余量
	tableH     int
	sendH      int
	sendLines  int
	tableRows  int
	problems   []string
}

func (r layoutReport) String() string {
	return fmt.Sprintf("%-26s dpi=%d 窗口 %4dx%-4d 超出 %3dx%-3d 客户区高 %3d 数据表高 %3d(%d 行) 发送框高 %3d(%d 行) 问题 %d",
		r.display.name, r.dpi, r.winW, r.winH, r.overW, r.overH, r.clientH, r.tableH, r.tableRows, r.sendH, r.sendLines, len(r.problems))
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
}

func runLayoutCases(t *testing.T, beforeCreate func(), setup func(*application), displays []layoutDisplay) {
	t.Helper()
	a := newWorkbenchForTestWith(t, beforeCreate)
	var (
		reports []layoutReport
		skipped []string
		dpi     int
		aware   string
	)
	done := make(chan struct{})
	// 用窗口真正的消息循环驱动:walk 在后台算布局,消息循环里才把结果应用到控件上。
	go func() {
		defer close(done)
		defer a.mw.Synchronize(func() { win.PostQuitMessage(0) })
		onUI := func(f func()) {
			finished := make(chan struct{})
			a.mw.Synchronize(func() { f(); close(finished) })
			<-finished
		}
		var real win.RECT
		onUI(func() {
			if setup != nil {
				setup(a)
			}
			win.ShowWindow(a.mw.Handle(), win.SW_SHOWNOACTIVATE)
			real, _ = workAreaFor(a.mw.Handle())
			dpi, aware = a.mw.DPI(), threadDPIAwareness()
		})
		for _, d := range displays {
			if px(d.w, dpi) > real.Right-real.Left || px(d.h, dpi) > real.Bottom-real.Top {
				skipped = append(skipped, d.name)
				continue
			}
			work := win.RECT{Left: real.Left, Top: real.Top, Right: real.Left + px(d.w, dpi), Bottom: real.Top + px(d.h, dpi)}
			// 模拟启动:先回到设计尺寸,再按工作区收缩,等布局应用后再量。
			onUI(func() { _ = a.mw.SetSize(walk.Size{Width: 1280, Height: 820}) })
			time.Sleep(300 * time.Millisecond)
			onUI(func() { a.fitIntoWorkArea(work) })
			time.Sleep(700 * time.Millisecond)
			onUI(func() { reports = append(reports, measureLayout(a, d, work, dpi)) })
		}
	}()
	a.mw.Run()
	<-done

	t.Logf("DPI %d(线程 DPI 感知:%s)", dpi, aware)
	for _, name := range skipped {
		t.Logf("%-26s 跳过:按此 DPI 比本机屏幕大", name)
	}
	var failed bool
	for _, r := range reports {
		t.Log(r)
		for _, p := range r.problems {
			t.Log("    " + p)
		}
		if r.overW > 0 || r.overH > 0 || r.sendLines < minSendLines || r.tableRows < minTableRows || len(r.problems) > 0 {
			failed = true
		}
	}
	if len(reports) == 0 {
		t.Fatal("没有量到任何显示组合")
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
	r.problems = layoutProblems(a, dpi)
	return r
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
// 落到工作区外由窗口的超出量统一体现,不再逐个控件列出。滚动面板的内容本来
// 就比视口大;分组框的边框与标签页控件按设计与同级内容重叠,这几种不算问题。
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
