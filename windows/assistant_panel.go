//go:build windows

package main

import (
	"context"
	"fmt"
	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"serial-tool/internal/wincore"
	"strconv"
	"strings"
	"time"
)

type assistantPanel struct {
	app          *application
	panel        *walk.Composite
	scope        *walk.ComboBox
	from, to     *walk.LineEdit
	report, chat *walk.TextEdit
	question     *walk.LineEdit
	status       *walk.Label
	run, send    *walk.PushButton
	tabs         *walk.TabWidget
	enabled      bool
	busy         bool
	cancel       context.CancelFunc
	turns        []analysisTurn
	contextText  string
	config       analysisAIConfig
	maxPackets   int
	requestID    uint64
}

func (w *assistantPanel) widget() Widget {
	w.config = analysisAIConfig{Base: w.app.engine.GetSetting("deepseek.base_url"), Key: w.app.engine.GetSetting("deepseek.api_key"), Model: w.app.engine.GetSetting("deepseek.model"), Timeout: 30 * time.Second}
	if w.config.Base == "" {
		w.config.Base = "https://api.deepseek.com"
	}
	if w.config.Model == "" {
		w.config.Model = "deepseek-chat"
	}
	w.maxPackets = 500
	if n, e := strconv.Atoi(w.app.engine.GetSetting("ai.max_packets")); e == nil && n > 0 && n <= 500 {
		w.maxPackets = n
	}
	quick := func(text string) Widget {
		return PushButton{Text: text, Image: uiIcon("check"), OnClicked: func() { w.analyze(text) }}
	}
	// 宽度对齐 mac 的 255px（app.m layoutMainPanes），把省下的横向空间还给数据表。
	return Composite{AssignTo: &w.panel, Visible: false, MinSize: Size{Width: 250}, MaxSize: Size{Width: 270}, Background: SolidColorBrush{Color: walk.RGB(239, 244, 255)}, Layout: VBox{Margins: Margins{Left: 10, Top: 8, Right: 10, Bottom: 8}}, Children: []Widget{
		Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{Label{Text: "AI 通信助手", Font: Font{Family: "Microsoft YaHei UI", PointSize: 12, Bold: true}, TextColor: walk.RGB(84, 66, 170)}, HSpacer{}, PushButton{Text: "×", MaxSize: Size{Width: 28}, OnClicked: w.app.toggleAssistant}}},
		Label{AssignTo: &w.status, Text: "AI 未启用 · 可使用本地分析"},
		TabWidget{AssignTo: &w.tabs, StretchFactor: 1, Pages: []TabPage{
			{Title: "智能分析", Layout: VBox{}, Children: []Widget{
				Label{Text: "分析范围"}, ComboBox{AssignTo: &w.scope, Model: []string{"当前选中数据", "当前全部数据", "最近 100 条", "自定义行号范围"}, CurrentIndex: 2},
				Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{LineEdit{AssignTo: &w.from, Text: "1", CueBanner: "起始行"}, Label{Text: "至"}, LineEdit{AssignTo: &w.to, Text: "100", CueBanner: "结束行"}}},
				PushButton{AssignTo: &w.run, Text: "开始分析", Image: uiIcon("ai"), MinSize: Size{Height: 34}, OnClicked: func() { w.analyze("全面诊断") }},
				Composite{Layout: Grid{Columns: 2}, Children: []Widget{quick("协议识别"), quick("Modbus 分析"), quick("CRC 校验"), quick("大小端分析"), quick("通信时序"), quick("异常报文分析"), quick("粘包 / 拆包"), quick("数据类型推测")}},
				Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{Label{Text: "分析结果", Font: Font{Bold: true}}, HSpacer{}, PushButton{Text: "复制", Image: uiIcon("copy"), OnClicked: func() { walk.Clipboard().SetText(w.report.Text()) }}}},
				TextEdit{AssignTo: &w.report, ReadOnly: true, VScroll: true, StretchFactor: 1, MinSize: Size{Height: 100}},
			}},
			{Title: "历史分析", Layout: VBox{}, Children: []Widget{
				Label{Text: "从本地采集历史中分析，不影响当前收发。"},
				PushButton{Text: "最近 5 分钟", OnClicked: func() { w.history(5 * time.Minute) }}, PushButton{Text: "最近 30 分钟", OnClicked: func() { w.history(30 * time.Minute) }}, PushButton{Text: "最近 1 小时", OnClicked: func() { w.history(time.Hour) }},
				PushButton{Text: "自定义时间 / 数据库", OnClicked: w.app.openDatabaseAnalysis}, VSpacer{}, Label{Text: "历史报告生成后，可在底部继续提问。"},
			}},
			{Title: "对话记录", Layout: VBox{}, Children: []Widget{TextEdit{AssignTo: &w.chat, ReadOnly: true, VScroll: true, StretchFactor: 1}, PushButton{Text: "清空对话", OnClicked: func() {
				if !w.busy {
					w.turns = nil
					w.chat.SetText("")
				}
			}}, PushButton{Text: "导出报告与对话", OnClicked: func() { w.app.exportText(w.report.Text()+"\r\n\r\n"+w.chat.Text(), "commbox-analysis", w.app.mw) }}}},
		}},
		LineEdit{AssignTo: &w.question, CueBanner: "继续询问，例如：这个值是多少？"},
		Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{PushButton{AssignTo: &w.send, Text: "发送追问", Image: uiIcon("send"), OnClicked: w.ask}, PushButton{Text: "停止", Image: uiIcon("stop"), OnClicked: w.stop}, PushButton{Text: "AI 设置", Image: uiIcon("settings"), OnClicked: w.settings}}},
	}}
}

func selectAnalysisPackets(all, visible []Packet, selected []int, scope, from, to, limit int) ([]wincore.Packet, error) {
	var source []Packet
	switch scope {
	case 0:
		for _, i := range selected {
			if i >= 0 && i < len(visible) {
				source = append(source, visible[i])
			}
		}
	case 1:
		source = all
	case 2:
		source = all
		if len(source) > 100 {
			source = source[len(source)-100:]
		}
	case 3:
		if from < 1 || to < from || to > len(visible) {
			return nil, fmt.Errorf("请输入当前可见数据内的有效行号范围（1–%d）", len(visible))
		}
		source = visible[from-1 : to]
	default:
		return nil, fmt.Errorf("分析范围无效")
	}
	if len(source) == 0 {
		return nil, fmt.Errorf("当前范围没有数据，请选择报文或先采集数据")
	}
	if len(source) > limit {
		return nil, fmt.Errorf("所选 %d 条超过上下文上限 %d，请缩小范围", len(source), limit)
	}
	size := 0
	out := make([]wincore.Packet, 0, len(source))
	for _, p := range source {
		size += len(p.Raw.Data)
		if size > analysisInputLimit {
			return nil, fmt.Errorf("范围超过 8 MiB，请缩小范围")
		}
		raw := p.Raw
		raw.Data = append([]byte(nil), raw.Data...)
		out = append(out, raw)
	}
	return out, nil
}

func analysisContext(packets []wincore.Packet, mode, state string) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "模式：%s；状态：%s。TCP 数据为采集片段，不能据此假定应用层帧边界。实际串口波特率不能通过解码后的字节反推。\n", mode, state)
	for _, p := range packets {
		fmt.Fprintf(&b, "%s %s %s session=%s peer=%s data=% X\n", p.Timestamp.Format(time.RFC3339Nano), p.Direction, p.Transport, p.ConnectionID, p.Endpoint, p.Data)
		if b.Len() > 64<<10 {
			return "", fmt.Errorf("所选原始数据超过 AI 上下文 64 KiB，请缩小范围")
		}
	}
	return b.String(), nil
}

func (w *assistantPanel) analyze(topic string) {
	if w.busy {
		return
	}
	a := w.app
	from, _ := strconv.Atoi(w.from.Text())
	to, _ := strconv.Atoi(w.to.Text())
	packets, err := selectAnalysisPackets(a.packetModel.all, a.packetModel.visible, a.packetTable.SelectedIndexes(), w.scope.CurrentIndex(), from, to, w.maxPackets)
	if err != nil {
		w.status.SetText(err.Error())
		return
	}
	ctxText, ctxErr := analysisContext(packets, string(a.uiMode()), a.status.Text())
	if ctxErr != nil && w.enabled {
		w.status.SetText(ctxErr.Error())
		return
	}
	cfg := w.config
	cfg.Enabled = w.enabled
	w.start(func(ctx context.Context) (string, []analysisTurn, string, error) {
		report, err := analysisPacketReport(packets)
		if err != nil {
			return "", nil, "", err
		}
		if !cfg.Enabled {
			return report, nil, ctxText, nil
		}
		turns := []analysisTurn{{Role: "user", Content: "请执行" + topic + "。明确区分事实和推测，数据不足时明确说明，不得捏造响应率或连接状态。\n" + ctxText}}
		result, err := analysisAIChat(ctx, cfg, turns)
		if err != nil {
			return report, nil, ctxText, err
		}
		turns = append(turns, analysisTurn{Role: "assistant", Content: result})
		return result, turns, ctxText, nil
	})
}

func (w *assistantPanel) start(job func(context.Context) (string, []analysisTurn, string, error)) {
	if w.busy {
		return
	}
	w.busy = true
	w.run.SetEnabled(false)
	w.send.SetEnabled(false)
	w.status.SetText("分析中…")
	w.requestID++
	id := w.requestID
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	go func() {
		result, turns, contextText, err := job(ctx)
		cancel()
		if w.app.closed.Load() {
			return
		}
		w.app.mw.Synchronize(func() {
			if w.app.closed.Load() || w.panel.IsDisposed() || id != w.requestID {
				return
			}
			w.busy = false
			w.cancel = nil
			w.run.SetEnabled(true)
			w.send.SetEnabled(true)
			if err != nil {
				w.status.SetText("分析未完成：" + err.Error())
				if result != "" {
					w.report.SetText(strings.ReplaceAll(result, "\n", "\r\n"))
					w.contextText = contextText
					w.turns = nil
					w.chat.SetText("")
				}
				return
			}
			w.report.SetText(strings.ReplaceAll(result, "\n", "\r\n"))
			w.contextText = contextText
			w.turns = turns
			var b strings.Builder
			for _, t := range turns {
				label := "用户"
				text := t.Content
				if t.Role == "assistant" {
					label = "AI"
				} else if strings.Contains(text, "session=") {
					text = "分析所选报文"
				}
				fmt.Fprintf(&b, "%s：\r\n%s\r\n\r\n", label, strings.ReplaceAll(text, "\n", "\r\n"))
			}
			w.chat.SetText(b.String())
			w.tabs.SetCurrentIndex(0)
			if len(turns) == 0 {
				w.status.SetText("本地分析完成 · 未上传数据")
			} else {
				w.status.SetText("AI 分析完成")
			}
		})
	}()
}
func (w *assistantPanel) stop() {
	if w.cancel != nil {
		w.cancel()
		w.cancel = nil
	}
	if w.busy {
		w.requestID++
		w.busy = false
		w.run.SetEnabled(true)
		w.send.SetEnabled(true)
		w.status.SetText("已停止分析")
	}
}
func (w *assistantPanel) ask() {
	if w.busy {
		return
	}
	if !w.enabled {
		w.status.SetText("请先在 AI 设置中启用服务")
		return
	}
	q := strings.TrimSpace(w.question.Text())
	if q == "" {
		w.status.SetText("请输入问题")
		return
	}
	turns := append([]analysisTurn(nil), w.turns...)
	if len(turns) == 0 {
		if w.contextText == "" {
			w.status.SetText("请先选择数据并完成分析")
			return
		}
		q = "通信上下文：\n" + w.contextText + "\n问题：" + q
	}
	turns = append(turns, analysisTurn{Role: "user", Content: q})
	cfg := w.config
	cfg.Enabled = true
	contextText := w.contextText
	w.start(func(ctx context.Context) (string, []analysisTurn, string, error) {
		result, err := analysisAIChat(ctx, cfg, turns)
		if err != nil {
			return "", nil, "", err
		}
		return result, append(turns, analysisTurn{Role: "assistant", Content: result}), contextText, nil
	})
}
func (w *assistantPanel) history(duration time.Duration) {
	if w.busy {
		return
	}
	end := time.Now()
	start := end.Add(-duration)
	limit := w.maxPackets
	w.start(func(ctx context.Context) (string, []analysisTurn, string, error) {
		files, err := wincore.ListAnalysisDatabases(w.app.engine.DataDir())
		if err != nil {
			return "", nil, "", err
		}
		if ctx.Err() != nil {
			return "", nil, "", ctx.Err()
		}
		report, err := wincore.AnalyzeDatabases(w.app.engine.DataDir(), files, start.Format(time.RFC3339), end.Format(time.RFC3339), "ALL", limit)
		return report, nil, report, err
	})
}
func (w *assistantPanel) settings() {
	var dlg *walk.Dialog
	var enabled *walk.CheckBox
	var base, key, model *walk.LineEdit
	var timeout, limit *walk.NumberEdit
	if err := (Dialog{AssignTo: &dlg, Title: "AI 设置", Size: Size{Width: 560, Height: 380}, Layout: VBox{}, Children: []Widget{
		CheckBox{AssignTo: &enabled, Text: "启用 AI（主动分析 / 追问时提交所选数据）", Checked: w.enabled},
		Composite{Layout: Grid{Columns: 2}, Children: []Widget{Label{Text: "服务地址"}, LineEdit{AssignTo: &base, Text: w.config.Base}, Label{Text: "API Key"}, LineEdit{AssignTo: &key, Text: w.config.Key, PasswordMode: true}, Label{Text: "模型"}, LineEdit{AssignTo: &model, Text: w.config.Model}, Label{Text: "超时 (s)"}, NumberEdit{AssignTo: &timeout, Value: w.config.Timeout.Seconds(), MinValue: 1, MaxValue: 300, Decimals: 0}, Label{Text: "最大上下文条数"}, NumberEdit{AssignTo: &limit, Value: float64(w.maxPackets), MinValue: 1, MaxValue: 500, Decimals: 0}}},
		Label{Text: "支持 DeepSeek 及兼容 Chat Completions 的服务。\r\nKey 保存在本机设置库；每次启动默认关闭 AI。"},
		PushButton{Text: "保存", OnClicked: func() {
			w.stop()
			w.enabled = enabled.Checked()
			w.config = analysisAIConfig{Enabled: w.enabled, Base: strings.TrimSpace(base.Text()), Key: strings.TrimSpace(key.Text()), Model: strings.TrimSpace(model.Text()), Timeout: time.Duration(timeout.Value()) * time.Second}
			w.maxPackets = int(limit.Value())
			for k, v := range map[string]string{"deepseek.base_url": w.config.Base, "deepseek.api_key": w.config.Key, "deepseek.model": w.config.Model, "ai.max_packets": strconv.Itoa(w.maxPackets)} {
				if err := w.app.engine.SetSetting(k, v); err != nil {
					walk.MsgBox(dlg, "设置", err.Error(), walk.MsgBoxOK)
					return
				}
			}
			if w.enabled {
				w.status.SetText("AI 已启用 · 等待主动分析")
			} else {
				w.status.SetText("AI 未启用 · 可使用本地分析")
			}
			dlg.Accept()
		}},
	}}).Create(w.app.mw); err != nil {
		w.app.showError(err)
		return
	}
	defer dlg.Dispose()
	dlg.Run()
}
