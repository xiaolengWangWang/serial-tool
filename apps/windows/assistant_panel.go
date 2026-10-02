//go:build windows

package main

import (
	"context"
	"fmt"
	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"serial-tool/core"
	"strconv"
	"strings"
	"time"
)

type assistantPanel struct {
	app           *application
	panel         *walk.ScrollView
	attach        *walk.CheckBox
	packetOptions *walk.Composite
	scope         *walk.ComboBox
	topic         *walk.ComboBox
	rangeRow      *walk.Composite
	from, to      *walk.LineEdit
	run           *walk.PushButton
	chat          *aiChat
	enabled       bool
	config        analysisAIConfig
	maxPackets    int
}

// 分析范围：前四项取主界面报文，其后取本机历史数据库（最后一项打开数据库分析窗口）。
var (
	analysisScopes = []string{"当前选中数据", "当前全部数据", "最近 100 条", "自定义行号范围",
		"历史 · 最近 5 分钟", "历史 · 最近 30 分钟", "历史 · 最近 1 小时", "历史 · 自定义时间 / 数据库…"}
	analysisTopics = []string{"全面诊断", "协议识别", "Modbus 分析", "CRC 校验", "大小端分析", "通信时序", "异常报文分析", "粘包 / 拆包", "数据类型推测"}
	historyWindows = []time.Duration{5 * time.Minute, 30 * time.Minute, time.Hour}
)

const (
	customRangeScope = 3
	historyScope     = 4
)

func (w *assistantPanel) widget() Widget {
	w.config = analysisAIConfig{Base: w.app.engine.GetSetting("deepseek.base_url"), Key: w.app.engine.GetSetting("deepseek.api_key"), Model: w.app.engine.GetSetting("deepseek.model"), Timeout: 60 * time.Second}
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
	w.chat = &aiChat{app: w.app, owner: func() walk.Form { return w.app.mw }, config: w.aiConfig, settings: w.settings, prepare: w.packetContext, onBusy: func(busy bool) { w.run.SetEnabled(!busy) }}
	// 对话记录在上方，报文选项靠近输入框；设置移到标题行。
	chatWidgets := w.chat.widgets()
	tools := chatWidgets[len(chatWidgets)-1].(Composite)
	tools.Children = append(tools.Children[:3], HSpacer{})
	chatWidgets[len(chatWidgets)-1] = tools
	children := []Widget{
		Composite{Layout: HBox{MarginsZero: true, Spacing: 6}, Children: []Widget{
			Label{Text: "AI 助手", Font: fontSection, TextColor: colorBlue}, HSpacer{},
			PushButton{Text: "设置", MinSize: Size{Width: 52, Height: btnH}, MaxSize: Size{Width: 52}, OnClicked: w.settings},
			PushButton{Text: "×", ToolTipText: "收起助手，正在进行的回答会继续", MinSize: Size{Width: 32, Height: btnH}, MaxSize: Size{Width: 32}, OnClicked: w.app.toggleAssistant},
		}},
		Label{AssignTo: &w.chat.status, Text: "输入问题开始聊天；报文和附件按需添加", TextColor: colorMuted, EllipsisMode: EllipsisEnd},
		chatWidgets[0],
		CheckBox{AssignTo: &w.attach, Text: "附带报文", MinSize: Size{Height: rowH}, ToolTipText: "勾选后，每次发送附带当前所选范围；不勾选时只发送问题和附件。", OnCheckedChanged: func() {
			if w.packetOptions != nil {
				w.packetOptions.SetVisible(w.attach.Checked())
			}
		}},
		Composite{AssignTo: &w.packetOptions, Visible: false, Background: SolidColorBrush{Color: colorCanvas}, Layout: VBox{Margins: Margins{Left: 8, Top: 6, Right: 8, Bottom: 6}, Spacing: 6}, Children: []Widget{
			ComboBox{AssignTo: &w.scope, Model: analysisScopes[:4], CurrentIndex: 2, MinSize: Size{Width: 80, Height: rowH}, ToolTipText: "当前全部数据包含被筛选隐藏的报文；仅上传某些报文时请选择当前选中数据。", OnCurrentIndexChanged: func() {
				if w.rangeRow != nil {
					w.rangeRow.SetVisible(w.scope.CurrentIndex() == customRangeScope)
				}
			}},
			Composite{AssignTo: &w.rangeRow, Visible: false, Layout: HBox{MarginsZero: true, Spacing: 6}, Children: []Widget{
				LineEdit{AssignTo: &w.from, Text: "1", CueBanner: "起始行", MinSize: Size{Height: rowH}}, Label{Text: "至"}, LineEdit{AssignTo: &w.to, Text: "100", CueBanner: "结束行", MinSize: Size{Height: rowH}},
			}},
			Composite{Layout: HBox{MarginsZero: true, Spacing: 6}, Children: []Widget{
				ComboBox{AssignTo: &w.topic, Model: analysisTopics, CurrentIndex: 0, StretchFactor: stretchFill, MinSize: Size{Width: 60, Height: rowH}},
				PushButton{AssignTo: &w.run, Text: "本地检查", MinSize: Size{Width: 84, Height: btnH}, MaxSize: Size{Width: 84}, ToolTipText: "使用内置解析检查报文，不发送给 AI。", OnClicked: func() { w.analyze(w.topic.Text()) }},
			}},
			Label{Text: "报文仅在点击发送时交给 AI。", TextColor: colorMuted},
		}},
	}

	return ScrollView{AssignTo: &w.panel, HorizontalFixed: true, Visible: false, MinSize: Size{Width: assistantMinWidth}, MaxSize: Size{Width: assistantMaxWidth}, Background: SolidColorBrush{Color: colorPanel}, Layout: VBox{Alignment: AlignHNearVNear, Margins: Margins{Left: 10, Top: 8, Right: 10, Bottom: 8}, Spacing: 8}, Children: append(children, chatWidgets[1:]...)}
}

// packetContext is evaluated only after the user clicks Send.
func (w *assistantPanel) packetContext() (string, error) {
	if w.attach == nil || !w.attach.Checked() {
		return "", nil
	}
	a := w.app
	from, _ := strconv.Atoi(w.from.Text())
	to, _ := strconv.Atoi(w.to.Text())
	packets, err := selectAnalysisPackets(a.packetModel.all, a.packetModel.visible, a.packetTable.SelectedIndexes(), w.scope.CurrentIndex(), from, to, w.maxPackets)
	if err != nil {
		return "", err
	}
	return analysisContext(packets, string(a.uiMode()), a.status.Text())
}

func (w *assistantPanel) aiConfig() (analysisAIConfig, bool) {
	cfg := w.config
	cfg.Enabled = w.enabled
	return cfg, w.enabled
}

func selectAnalysisPackets(all, visible []Packet, selected []int, scope, from, to, limit int) ([]core.Packet, error) {
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
	out := make([]core.Packet, 0, len(source))
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

// analysisContext 是发给 AI 的报文清单。像 Modbus 的帧附上本地解析（CRC 对错、功能码、
// 字节数），这类要精确计算的结论不交给模型自己算。
func analysisContext(packets []core.Packet, mode, state string) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "模式：%s；状态：%s。TCP 数据为采集片段，不能据此假定应用层帧边界。实际串口波特率不能通过解码后的字节反推。「本地解析」由程序计算。\n", mode, state)
	for _, p := range packets {
		fmt.Fprintf(&b, "%s %s %s session=%s peer=%s data=% X", p.Timestamp.Format(time.RFC3339Nano), p.Direction, p.Transport, p.ConnectionID, p.Endpoint, p.Data)
		if s := core.ModbusSummary(p.Transport, p.Data); s != "" {
			b.WriteString(" | 本地解析：" + s)
		}
		b.WriteByte('\n')
		if b.Len() > 64<<10 {
			return "", fmt.Errorf("所选原始数据超过 AI 上下文 64 KiB，请缩小范围")
		}
	}
	return b.String(), nil
}

// analyze 按当前范围做一轮分析：主界面报文或本机历史数据库。
func (w *assistantPanel) analyze(topic string) {
	if w.chat.busy {
		return
	}
	scope := w.scope.CurrentIndex()
	if scope >= historyScope {
		w.analyzeHistory(topic, scope-historyScope)
		return
	}
	a := w.app
	from, _ := strconv.Atoi(w.from.Text())
	to, _ := strconv.Atoi(w.to.Text())
	packets, err := selectAnalysisPackets(a.packetModel.all, a.packetModel.visible, a.packetTable.SelectedIndexes(), scope, from, to, w.maxPackets)
	if err != nil {
		w.chat.status.SetText(chineseError(err))
		return
	}
	ctxText, _ := analysisContext(packets, string(a.uiMode()), a.status.Text())
	display := fmt.Sprintf("%s · %s（%d 条报文）", topic, w.scope.Text(), len(packets))
	w.chat.analyze(display, func(context.Context) (string, error) { return analysisPacketReport(packets) }, func(string) (string, string) {
		return "请执行" + topic + "。明确区分事实和推测，数据不足时明确说明，不得捏造响应率或连接状态。\n" + ctxText, ctxText
	})
}

// analyzeHistory 分析本机历史数据库最近一段时间的数据；AI 拿到的是本地数据库报告。
func (w *assistantPanel) analyzeHistory(topic string, window int) {
	if window >= len(historyWindows) {
		w.app.openDatabaseAnalysis()
		return
	}
	end := time.Now()
	start := end.Add(-historyWindows[window])
	limit := w.maxPackets
	dir := w.app.engine.DataDir()
	label := strings.TrimPrefix(w.scope.Text(), "历史 · ")
	display := fmt.Sprintf("%s · 历史数据库 %s", topic, label)
	w.chat.analyze(display, func(ctx context.Context) (string, error) {
		files, err := core.ListAnalysisDatabases(dir)
		if err != nil {
			return "", err
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return core.AnalyzeDatabases(dir, files, start.Format(time.RFC3339), end.Format(time.RFC3339), "ALL", limit)
	}, func(report string) (string, string) {
		data := analysisReportForAI(report)
		return "以下是本机历史数据库" + label + "的本地分析报告。请执行" + topic + "。明确区分事实和推测，数据不足时明确说明。\n" + data, data
	})
}

func (w *assistantPanel) stop() {
	if w.chat != nil {
		w.chat.stop()
	}
}

func (w *assistantPanel) settings() { w.settingsFor(w.app.mw) }

// settingsFor 以 owner 为父窗口打开 AI 设置，数据库分析窗口里也能直接改。
func (w *assistantPanel) settingsFor(owner walk.Form) {
	var dlg *walk.Dialog
	var enabled *walk.CheckBox
	var base, key, model *walk.LineEdit
	var timeout, limit *walk.NumberEdit
	if err := (Dialog{AssignTo: &dlg, Title: "AI 设置", Size: Size{Width: 580, Height: 400}, MinSize: Size{Width: 500, Height: 360}, Font: Font{Family: fontUI, PointSize: sizeBody}, Layout: VBox{Alignment: AlignHNearVNear, Margins: Margins{Left: 12, Top: 10, Right: 12, Bottom: 12}, Spacing: 8}, Children: []Widget{
		CheckBox{AssignTo: &enabled, Text: "启用 AI（点击发送时提交问题及所选内容）", Checked: w.enabled},
		Composite{Layout: Grid{Alignment: AlignHNearVCenter, Columns: 2}, Children: []Widget{Label{Text: "服务地址"}, LineEdit{AssignTo: &base, Text: w.config.Base}, Label{Text: "API Key"}, LineEdit{AssignTo: &key, Text: w.config.Key, PasswordMode: true}, Label{Text: "模型"}, LineEdit{AssignTo: &model, Text: w.config.Model}, Label{Text: "无响应超时 (s)"}, NumberEdit{AssignTo: &timeout, Value: w.config.Timeout.Seconds(), MinValue: 15, MaxValue: 300, Decimals: 0}, Label{Text: "最大上下文条数"}, NumberEdit{AssignTo: &limit, Value: float64(w.maxPackets), MinValue: 1, MaxValue: 500, Decimals: 0}}},
		Label{Text: "支持 DeepSeek 及兼容 Chat Completions 的服务，回答边生成边显示。\r\n图片需服务和模型支持视觉输入。\r\n超时按「多久没有收到新内容」计算，超过此时间未收到内容会停止等待。\r\nKey 保存在 Windows 凭据管理器；每次启动默认关闭 AI。"},
		PushButton{Text: "保存", OnClicked: func() {
			w.stop()
			w.enabled = enabled.Checked()
			w.config = analysisAIConfig{Enabled: w.enabled, Base: strings.TrimSpace(base.Text()), Key: strings.TrimSpace(key.Text()), Model: strings.TrimSpace(model.Text()), Timeout: time.Duration(timeout.Value()) * time.Second}
			w.maxPackets = int(limit.Value())
			for k, v := range map[string]string{"deepseek.base_url": w.config.Base, "deepseek.api_key": w.config.Key, "deepseek.model": w.config.Model, "ai.max_packets": strconv.Itoa(w.maxPackets)} {
				if err := w.app.engine.SetSetting(k, v); err != nil {
					walk.MsgBox(dlg, "设置", chineseError(err), walk.MsgBoxOK)
					return
				}
			}
			if w.enabled {
				w.chat.status.SetText("AI 已启用 · 点击发送时才上传数据")
			} else {
				w.chat.status.SetText("AI 未启用 · 可使用本地分析")
			}
			dlg.Accept()
		}},
	}}).Create(owner); err != nil {
		w.app.showError(err)
		return
	}
	defer dlg.Dispose()
	dlg.Run()
}
