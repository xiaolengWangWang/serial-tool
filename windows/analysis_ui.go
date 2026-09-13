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
	"serial-tool/internal/wincore"
)

// All analysis state belongs to its dialog; workers receive immutable snapshots.
type analysisWindow struct {
	app                    *application
	dlg                    *walk.Dialog
	report, chat, question *walk.TextEdit
	status                 *walk.Label
	run, send              *walk.PushButton
	enabled                *walk.CheckBox
	base, key, model       *walk.LineEdit
	turns                  []analysisTurn
	cancel                 context.CancelFunc
	busy                   bool
}

func (a *application) openAnalysisCenter() {
	w := &analysisWindow{app: a}
	var scope *walk.ComboBox
	controls := []Widget{
		Composite{Layout: HBox{}, Children: []Widget{Label{Text: "分析范围"}, ComboBox{AssignTo: &scope, Model: []string{"选中报文", "当前可见报文", "全部保留报文"}, CurrentIndex: 1}, PushButton{AssignTo: &w.run, Text: "开始本地分析", OnClicked: func() {
			var packets []Packet
			if a.packetModel != nil {
				if scope.CurrentIndex() == 2 {
					packets = a.packetModel.all
				} else {
					packets = a.packetModel.visible
				}
			}
			raw := make([]wincore.Packet, len(packets))
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
			w.work(func(context.Context) (string, error) { return analysisPacketReport(snapshot) }, false)
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
		w.status.SetText("读取数据库列表…")
		go func() {
			files, err := wincore.ListAnalysisDatabases(a.engine.DataDir())
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
				w.status.SetText(fmt.Sprintf("%d 个数据库；Ctrl / Shift 可多选", len(names)))
			})
		}()
	}
	controls := []Widget{
		Composite{Layout: HBox{}, Children: []Widget{Label{Text: "选择采集数据库（Ctrl / Shift 多选）"}, PushButton{Text: "刷新列表", OnClicked: refresh}, PushButton{Text: "数据目录", OnClicked: a.openDataDir}}},
		ListBox{AssignTo: &list, Model: names, MultiSelection: true, MinSize: Size{Height: 90}, MaxSize: Size{Height: 120}},
		Composite{Layout: Grid{Columns: 4}, Children: []Widget{
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
				return wincore.AnalyzeDatabases(a.engine.DataDir(), selected, start, end, dir, n)
			}, false)
		}},
	}
	w.openWithInit("数据库分析", controls, refresh)
}

func (w *analysisWindow) fail(err error) {
	walk.MsgBox(w.dlg, "分析", err.Error(), walk.MsgBoxIconError)
}
func (w *analysisWindow) open(title string, controls []Widget) { w.openWithInit(title, controls, nil) }
func (w *analysisWindow) openWithInit(title string, controls []Widget, init func()) {
	setting := func(key, fallback string) string {
		value := w.app.engine.GetSetting("deepseek." + key)
		if value == "" {
			return fallback
		}
		return value
	}
	children := append(controls,
		Label{AssignTo: &w.status, Text: "本地分析不上传数据。AI 仅在启用后点击发送时请求所填服务。"},
		TabWidget{StretchFactor: 1, Pages: []TabPage{
			{Title: "本地报告", Layout: VBox{}, Children: []Widget{TextEdit{AssignTo: &w.report, ReadOnly: true, VScroll: true, HScroll: true, MinSize: Size{Height: 170}}}},
			{Title: "AI 设置与对话", Layout: VBox{}, Children: []Widget{
				CheckBox{AssignTo: &w.enabled, Text: "启用 AI：允许点击发送时上传报告和对话（默认关闭）"},
				Composite{Layout: Grid{Columns: 2}, Children: []Widget{Label{Text: "Base URL"}, LineEdit{AssignTo: &w.base, Text: setting("base_url", "https://api.deepseek.com")}, Label{Text: "模型"}, LineEdit{AssignTo: &w.model, Text: setting("model", "deepseek-chat")}, Label{Text: "API Key"}, LineEdit{AssignTo: &w.key, Text: setting("api_key", ""), PasswordMode: true}}},
				PushButton{Text: "保存连接设置（Key 存于本地设置库）", OnClicked: func() {
					for key, value := range map[string]string{"base_url": w.base.Text(), "model": w.model.Text(), "api_key": w.key.Text()} {
						if err := w.app.engine.SetSetting("deepseek."+key, value); err != nil {
							w.fail(err)
							return
						}
					}
					w.status.SetText("连接设置已保存；启用状态仅限当前窗口")
				}},
				TextEdit{AssignTo: &w.chat, ReadOnly: true, VScroll: true, StretchFactor: 1, MinSize: Size{Height: 100}},
				TextEdit{AssignTo: &w.question, VScroll: true, MinSize: Size{Height: 50}, MaxSize: Size{Height: 80}, Text: "请分析报告中的协议、异常和排查建议。"},
				Composite{Layout: HBox{}, Children: []Widget{PushButton{AssignTo: &w.send, Text: "发送报告 / 继续追问", OnClicked: w.sendAI}, PushButton{Text: "清空对话", OnClicked: func() {
					if !w.busy {
						w.turns = nil
						w.chat.SetText("")
					}
				}}, PushButton{Text: "取消 AI 请求", OnClicked: func() {
					if w.cancel != nil {
						w.cancel()
					}
				}}}},
			}},
		}},
		Composite{Layout: HBox{}, Children: []Widget{PushButton{Text: "导出报告与对话…", OnClicked: w.export}, HSpacer{}, PushButton{Text: "关闭", OnClicked: func() { w.dlg.Cancel() }}}},
	)
	if err := (Dialog{AssignTo: &w.dlg, Title: title, Size: Size{Width: 980, Height: 780}, MinSize: Size{Width: 780, Height: 620}, Layout: VBox{}, Children: children}).Create(w.app.mw); err != nil {
		w.app.showError(err)
		return
	}
	defer w.dlg.Dispose()
	w.dlg.Closing().Attach(func(_ *bool, _ walk.CloseReason) {
		if w.cancel != nil {
			w.cancel()
		}
	})
	if init != nil {
		init()
	}
	w.dlg.Run()
}

func (w *analysisWindow) work(job func(context.Context) (string, error), ai bool) {
	if w.busy {
		return
	}
	w.busy = true
	w.run.SetEnabled(false)
	w.send.SetEnabled(false)
	w.status.SetText("分析中…")
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	go func() {
		result, err := job(ctx)
		cancel()
		w.app.mw.Synchronize(func() {
			if w.dlg.IsDisposed() {
				return
			}
			w.busy = false
			w.cancel = nil
			w.run.SetEnabled(true)
			w.send.SetEnabled(true)
			if err != nil {
				w.status.SetText("分析未完成")
				w.fail(err)
				return
			}
			if ai {
				w.turns = append(w.turns, analysisTurn{Role: "assistant", Content: result})
				w.chat.SetText(w.chat.Text() + "\r\nAI：\r\n" + strings.ReplaceAll(result, "\n", "\r\n") + "\r\n")
			} else {
				w.report.SetText(strings.ReplaceAll(result, "\n", "\r\n"))
				w.turns = nil
				w.chat.SetText("")
			}
			w.status.SetText("分析完成")
		})
	}()
}

func (w *analysisWindow) sendAI() {
	if w.busy {
		return
	}
	if !w.enabled.Checked() {
		w.fail(errors.New("请先勾选启用 AI"))
		return
	}
	question := strings.TrimSpace(w.question.Text())
	if question == "" {
		w.fail(errors.New("请输入问题"))
		return
	}
	prompt := question
	if len(w.turns) == 0 {
		report := strings.TrimSpace(w.report.Text())
		if report == "" {
			w.fail(errors.New("请先完成本地分析"))
			return
		}
		if len(report) > 32<<10 {
			w.fail(errors.New("报告超过 32 KiB，请缩小范围后分析"))
			return
		}
		prompt = "本地报告：\n" + report + "\n\n问题：" + question
	}
	cfg := analysisAIConfig{Enabled: true, Base: w.base.Text(), Key: w.key.Text(), Model: w.model.Text()}
	turns := append(append([]analysisTurn(nil), w.turns...), analysisTurn{Role: "user", Content: prompt})
	w.work(func(ctx context.Context) (string, error) { return analysisAIChat(ctx, cfg, turns) }, true)
	w.turns = turns
	w.chat.SetText(w.chat.Text() + "\r\n用户：" + question + "\r\n")
}

func (w *analysisWindow) export() {
	content := w.report.Text()
	if text := w.chat.Text(); text != "" {
		content += "\r\n\r\n# AI 对话\r\n" + text
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
	w.status.SetText("已导出：" + fd.FilePath)
}
