//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"serial-tool/core"
)

// All analysis state belongs to its dialog; workers receive immutable snapshots.
// 右侧对话与 AI 面板共用 aiChat；AI 服务设置与是否启用也与面板共用，只存一份。
type analysisWindow struct {
	app    *application
	dlg    *walk.Dialog
	report *walk.TextEdit
	run    *walk.PushButton
	chat   *aiChat
	busy   bool
}

func (a *application) openAnalysisCenter() {
	w := &analysisWindow{app: a}
	var scope *walk.ComboBox
	controls := []Widget{
		Composite{Layout: HBox{Alignment: AlignHNearVCenter}, Children: []Widget{Label{Text: "分析范围"}, ComboBox{AssignTo: &scope, Model: []string{"选中报文", "当前可见报文", "全部保留报文"}, CurrentIndex: 1}, PushButton{AssignTo: &w.run, Text: "开始本地分析", OnClicked: func() {
			var packets []Packet
			if a.packetModel != nil {
				if scope.CurrentIndex() == 2 {
					packets = a.packetModel.all
				} else {
					packets = a.packetModel.visible
				}
			}
			raw := make([]core.Packet, len(packets))
			for i, p := range packets {
				raw[i] = p.Raw
			}
			var indices []int
			if scope.CurrentIndex() == 0 {
				if a.packetTable != nil {
					indices = a.packetTable.SelectedIndexes()
				}
			} else {
				for i := range raw {
					indices = append(indices, i)
				}
			}
			// Reject oversized snapshots before copying bytes on the UI thread.
			size := 0
			for _, i := range indices {
				if i >= 0 && i < len(raw) {
					size += len(raw[i].Data)
				}
			}
			if size > analysisInputLimit {
				w.fail(errors.New("范围超过 8 MiB，请缩小范围"))
				return
			}
			snapshot := analysisSelection(raw, indices)
			w.work(func(context.Context) (string, error) { return analysisPacketReport(snapshot) })
		}}, PushButton{Text: "分析数据库…", OnClicked: a.openDatabaseAnalysis}}},
	}
	w.open("分析中心", controls)
}

func (a *application) openDatabaseAnalysis() {
	w := &analysisWindow{app: a}
	var list *walk.ListBox
	var from, to, limit *walk.LineEdit
	var direction *walk.ComboBox
	names := []string{}
	refresh := func() {
		if w.busy {
			return
		}
		w.busy = true
		w.chat.status.SetText("读取数据库列表…")
		go func() {
			files, err := core.ListAnalysisDatabases(a.engine.DataDir())
			a.mw.Synchronize(func() {
				if w.dlg.IsDisposed() {
					return
				}
				w.busy = false
				if err != nil {
					w.fail(err)
					return
				}
				names = files
				list.SetModel(names)
				if len(names) > 0 {
					list.SetSelectedIndexes([]int{len(names) - 1})
				}
				w.chat.status.SetText(fmt.Sprintf("%d 个数据库；Ctrl / Shift 可多选", len(names)))
			})
		}()
	}
	controls := []Widget{
		Composite{Layout: HBox{Alignment: AlignHNearVCenter}, Children: []Widget{Label{Text: "选择采集数据库（Ctrl / Shift 多选）"}, PushButton{Text: "刷新列表", OnClicked: refresh}, PushButton{Text: "数据目录", OnClicked: a.openDataDir}}},
		ListBox{AssignTo: &list, Model: names, MultiSelection: true, MinSize: Size{Height: 90}, MaxSize: Size{Height: 120}},
		Composite{Layout: Grid{Alignment: AlignHNearVCenter, Columns: 4}, Children: []Widget{
			Label{Text: "开始 (RFC3339)"}, LineEdit{AssignTo: &from, Text: time.Now().Add(-24 * time.Hour).Format(time.RFC3339)}, Label{Text: "结束 (RFC3339)"}, LineEdit{AssignTo: &to, Text: time.Now().Format(time.RFC3339)},
			Label{Text: "方向"}, ComboBox{AssignTo: &direction, Model: []string{"ALL", "RX", "TX"}, CurrentIndex: 0}, Label{Text: "最新记录上限 (1–1000000)"}, LineEdit{AssignTo: &limit, Text: "10000"},
		}},
		PushButton{AssignTo: &w.run, Text: "本地分析选中数据库", OnClicked: func() {
			n, err := strconv.Atoi(strings.TrimSpace(limit.Text()))
			if err != nil || n < 1 || n > 1000000 {
				w.fail(errors.New("记录上限须为 1–1000000 的整数"))
				return
			}
			var selected []string
			for _, i := range list.SelectedIndexes() {
				if i >= 0 && i < len(names) {
					selected = append(selected, names[i])
				}
			}
			start, end, dir := from.Text(), to.Text(), direction.Text()
			w.work(func(context.Context) (string, error) {
				return core.AnalyzeDatabases(a.engine.DataDir(), selected, start, end, dir, n)
			})
		}},
	}
	w.openWithInit("数据库分析", controls, refresh)
}

func (w *analysisWindow) fail(err error) {
	walk.MsgBox(w.dlg, "分析", err.Error(), walk.MsgBoxIconError)
}
func (w *analysisWindow) open(title string, controls []Widget) { w.openWithInit(title, controls, nil) }

// openWithInit 左侧是本地报告，右侧是 AI 对话：本地分析完成后报告就是对话的数据，
// 第一次提问附带报告（最多 32 KiB）；不输入问题直接发送时用默认问题。
func (w *analysisWindow) openWithInit(title string, controls []Widget, init func()) {
	panel := w.app.assistant
	w.chat = &aiChat{app: w.app, owner: func() walk.Form { return w.dlg }, config: panel.aiConfig, settings: func() { panel.settingsFor(w.dlg) },
		hint: "请分析报告中的协议、异常和排查建议。", onBusy: func(busy bool) { w.run.SetEnabled(!busy && !w.busy) }}
	state := "AI 未启用：本地分析不上传数据；点对话下方「设置」启用后可提问"
	if _, enabled := panel.aiConfig(); enabled {
		state = "AI 已启用：本地分析不上传数据，提问时附带本地报告"
	}
	chatColumn := append([]Widget{Label{Text: "AI 对话", Font: Font{Family: fontUI, PointSize: sizeBody, Bold: true}}}, w.chat.widgets()...)
	children := append(controls,
		Label{AssignTo: &w.chat.status, Text: state, EllipsisMode: EllipsisEnd},
		Composite{StretchFactor: 1, Layout: HBox{Alignment: AlignHNearVNear, MarginsZero: true, Spacing: 12}, Children: []Widget{
			Composite{StretchFactor: 1, Layout: VBox{Alignment: AlignHNearVNear, MarginsZero: true, Spacing: 6}, Children: []Widget{
				Label{Text: "本地报告", Font: Font{Family: fontUI, PointSize: sizeBody, Bold: true}},
				TextEdit{AssignTo: &w.report, ReadOnly: true, VScroll: true, HScroll: true, StretchFactor: 1, MinSize: Size{Height: 170}},
			}},
			Composite{StretchFactor: 1, Layout: VBox{Alignment: AlignHNearVNear, MarginsZero: true, Spacing: 6}, Children: chatColumn},
		}},
		Composite{Layout: HBox{Alignment: AlignHNearVCenter}, Children: []Widget{PushButton{Text: "导出报告与对话…", OnClicked: w.export}, HSpacer{}, PushButton{Text: "关闭", OnClicked: func() { w.dlg.Cancel() }}}},
	)
	if err := (Dialog{AssignTo: &w.dlg, Title: title, Size: Size{Width: 1080, Height: 780}, MinSize: Size{Width: 760, Height: 560}, Font: Font{Family: fontUI, PointSize: sizeBody}, Layout: VBox{Alignment: AlignHNearVNear, Margins: Margins{Left: 12, Top: 10, Right: 12, Bottom: 12}, Spacing: 8}, Children: children}).Create(w.app.mw); err != nil {
		w.app.showError(err)
		return
	}
	defer w.dlg.Dispose()
	w.dlg.Closing().Attach(func(_ *bool, _ walk.CloseReason) { w.chat.stop() })
	if init != nil {
		init()
	}
	growDialog(w.app, w.dlg, 1080, 780)
	w.dlg.Run()
}

// growDialog 把对话框放大到期望尺寸（逻辑像素，不超过所在屏幕工作区）并居中。
// walk 的 Dialog.Show 总把窗口设成内容的最小尺寸，声明的 Size 不起作用；这里排到
// 消息循环开始之后再改。
func growDialog(a *application, dlg *walk.Dialog, width, height int) {
	a.mw.Synchronize(func() {
		if dlg.IsDisposed() {
			return
		}
		rc, ok := workAreaFor(dlg.Handle())
		if !ok {
			return
		}
		dpi, b := dlg.DPI(), dlg.BoundsPixels()
		availW, availH := int(rc.Right-rc.Left), int(rc.Bottom-rc.Top)
		w := min(max(b.Width, walk.IntFrom96DPI(width, dpi)), availW)
		h := min(max(b.Height, walk.IntFrom96DPI(height, dpi)), availH)
		_ = dlg.SetBoundsPixels(walk.Rectangle{X: int(rc.Left) + (availW-w)/2, Y: int(rc.Top) + (availH-h)/2, Width: w, Height: h})
	})
}

// work 在后台跑本地分析，结果写进左侧报告，并作为右侧对话新一轮的数据。
func (w *analysisWindow) work(job func(context.Context) (string, error)) {
	if w.busy || w.chat.busy {
		return
	}
	w.busy = true
	w.run.SetEnabled(false)
	w.chat.status.SetText("本地分析中…")
	go func() {
		result, err := job(context.Background())
		w.app.mw.Synchronize(func() {
			if w.dlg.IsDisposed() {
				return
			}
			w.busy = false
			w.run.SetEnabled(true)
			if err != nil {
				w.chat.status.SetText("分析未完成")
				w.fail(err)
				return
			}
			w.report.SetText(strings.ReplaceAll(result, "\n", "\r\n"))
			w.chat.setContext(analysisReportForAI(result), "已载入新的本地报告")
			if _, enabled := w.app.assistant.aiConfig(); enabled {
				w.chat.status.SetText("本地分析完成；可直接提问，不输入问题时按默认问题分析报告")
			} else {
				w.chat.status.SetText("本地分析完成 · 未上传数据；启用 AI 后可针对报告提问")
			}
		})
	}()
}

func (w *analysisWindow) export() {
	content := w.report.Text()
	if len(w.chat.entries) > 0 {
		content += "\r\n\r\n" + strings.ReplaceAll(chatMarkdown(w.chat.entries), "\n", "\r\n")
	}
	if strings.TrimSpace(content) == "" {
		w.fail(errors.New("暂无可导出的内容"))
		return
	}
	fd := walk.FileDialog{Title: "保存分析报告", Filter: "Markdown (*.md)|*.md|文本 (*.txt)|*.txt", FilePath: "analysis-" + time.Now().Format("20060102-150405") + ".md"}
	ok, err := fd.ShowSave(w.dlg)
	if err != nil {
		w.fail(err)
		return
	}
	if !ok {
		return
	}
	if err = os.WriteFile(fd.FilePath, []byte(content), 0600); err != nil {
		w.fail(err)
		return
	}
	w.chat.status.SetText("已导出：" + fd.FilePath)
}
