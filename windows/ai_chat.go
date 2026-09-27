//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
)

// aiChat 是 AI 面板与数据库分析窗口共用的对话：分析结果与追问按时间依次排在同一段
// 对话里，回答边生成边显示；输入框单行，Enter 发送，回答期间「发送」变成「停止」。
// 发给服务的是 turns（含报文或报告原文），界面上显示的是 entries（Markdown 转成的
// 可读文本）；复制与导出用 entries 的原文。
type aiChat struct {
	app      *application
	owner    func() walk.Form
	config   func() (analysisAIConfig, bool) // 当前服务配置与是否已启用 AI
	settings func()
	onBusy   func(busy bool) // 让所属界面同步禁用「开始分析」之类的按钮
	status   *walk.Label
	hint     string // 输入框为空、本轮还没提问时使用的默认问题

	view   *walk.TextEdit
	input  *walk.LineEdit
	button *walk.PushButton

	entries   []chatEntry
	turns     []analysisTurn
	context   string // 本轮第一次提问时附带的报文或报告
	busy      bool
	cancel    context.CancelFunc
	requestID uint64
	shown     string // 文本框里当前的内容，用来判断能否只追加新增部分
}

type chatEntry struct {
	role      string // user、assistant、local、error、divider
	text      string // 原文：Markdown 或纯文本
	at        time.Time
	took      time.Duration
	streaming bool
	stopped   bool
}

func (c *aiChat) widgets() []Widget {
	return []Widget{
		TextEdit{AssignTo: &c.view, ReadOnly: true, VScroll: true, StretchFactor: 1, MinSize: Size{Height: 120}, MaxLength: 4 << 20},
		Composite{Layout: HBox{Alignment: AlignHNearVCenter, MarginsZero: true, Spacing: 6}, Children: []Widget{
			LineEdit{AssignTo: &c.input, CueBanner: "继续询问，Enter 发送", StretchFactor: stretchFill, MinSize: Size{Width: 80, Height: rowH}, OnKeyDown: func(key walk.Key) {
				if key == walk.KeyReturn {
					c.submit()
				}
			}},
			PushButton{AssignTo: &c.button, Text: "发送", Image: uiIcon("send"), MinSize: Size{Width: 76, Height: btnH}, MaxSize: Size{Width: 76}, OnClicked: func() {
				if c.busy {
					c.stop()
				} else {
					c.submit()
				}
			}},
		}},
		Composite{Layout: HBox{Alignment: AlignHNearVCenter, MarginsZero: true, Spacing: 6}, Children: []Widget{
			toolButton("清空", "", 56, c.clear),
			toolButton("复制", "", 56, c.copyLast),
			toolButton("导出", "", 56, c.export),
			toolButton("设置", "", 56, func() { c.settings() }),
			HSpacer{},
		}},
	}
}

// setContext 开始新的一轮：之后第一次提问附带 data，此前的追问上下文不再发给服务。
func (c *aiChat) setContext(data, note string) {
	c.turns, c.context = nil, data
	if note != "" {
		c.add(chatEntry{role: "divider", text: note, at: time.Now()})
	}
}

// analyze 做一轮分析：先在后台跑本地分析 job；未启用 AI 时把本地结果放进对话，
// 启用时再把 prompt 返回的提示词发给服务。prompt 同时给出之后追问要附带的数据。
func (c *aiChat) analyze(display string, job func(context.Context) (string, error), prompt func(report string) (question, data string)) {
	if c.busy {
		return
	}
	cfg, enabled := c.config()
	c.setContext("", "")
	if len(c.entries) > 0 {
		c.add(chatEntry{role: "divider", text: "新的分析", at: time.Now()})
	}
	c.add(chatEntry{role: "user", text: display, at: time.Now()})
	c.setBusy(true)
	c.status.SetText("本地分析中…")
	c.requestID++
	id := c.requestID
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	go func() {
		report, err := job(ctx)
		c.app.mw.Synchronize(func() {
			if c.app.closed.Load() || c.view.IsDisposed() || id != c.requestID {
				cancel()
				return
			}
			switch {
			case ctx.Err() != nil: // 本地分析期间点了停止，不再继续请求 AI
				c.setBusy(false)
				c.status.SetText("已停止")
			case err != nil:
				cancel()
				c.setBusy(false)
				c.fail(err)
			case !enabled:
				cancel()
				c.setBusy(false)
				_, c.context = prompt(report)
				c.add(chatEntry{role: "local", text: report, at: time.Now()})
				c.status.SetText("本地分析完成 · 未上传数据")
			default:
				var question string
				question, c.context = prompt(report)
				c.request(ctx, cancel, cfg, []analysisTurn{{Role: "user", Content: question}})
			}
		})
	}()
}

// submit 发送输入框里的追问；本轮第一次提问时带上报文或报告。
func (c *aiChat) submit() {
	if c.busy {
		return
	}
	cfg, enabled := c.config()
	question := strings.TrimSpace(c.input.Text())
	if question == "" && len(c.turns) == 0 {
		question = c.hint
	}
	switch {
	case question == "":
		c.status.SetText("请输入问题")
		return
	case !enabled:
		c.status.SetText("AI 未启用：点「设置」勾选启用后再提问")
		return
	case len(c.turns) == 0 && c.context == "":
		c.status.SetText("请先分析数据，再针对结果提问")
		return
	}
	content := question
	if len(c.turns) == 0 {
		content = c.context + "\n\n问题：" + question
	}
	c.input.SetText("")
	c.add(chatEntry{role: "user", text: question, at: time.Now()})
	c.requestID++
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.setBusy(true)
	c.request(ctx, cancel, cfg, append(append([]analysisTurn(nil), c.turns...), analysisTurn{Role: "user", Content: content}))
}

// request 流式请求 turns，回答写进新的一条 AI 消息。成功后 turns 连同回答成为下次追问的
// 上下文；失败或停止时不记入，下次提问会重新带上上下文。
func (c *aiChat) request(ctx context.Context, cancel context.CancelFunc, cfg analysisAIConfig, turns []analysisTurn) {
	id := c.requestID
	start := time.Now()
	c.add(chatEntry{role: "assistant", at: start, streaming: true})
	index := len(c.entries) - 1
	var (
		mu      sync.Mutex
		pending strings.Builder
		posted  bool
	)
	// 流式片段在后台到达，攒一小会儿再回界面线程刷新，避免每个字都整框重绘。
	flush := func() {
		mu.Lock()
		delta := pending.String()
		pending.Reset()
		posted = false
		mu.Unlock()
		if delta != "" && id == c.requestID && !c.view.IsDisposed() {
			c.entries[index].text += delta
			c.refresh()
		}
	}
	onDelta := func(s string) {
		mu.Lock()
		pending.WriteString(s)
		post := !posted
		posted = true
		mu.Unlock()
		if post {
			time.AfterFunc(120*time.Millisecond, func() { c.app.mw.Synchronize(flush) })
		}
	}
	ticker := time.NewTicker(time.Second)
	go func() {
		for {
			select {
			case <-ticker.C:
				c.app.mw.Synchronize(func() {
					if id == c.requestID && c.busy {
						c.status.SetText(fmt.Sprintf("AI 回答中 · %d s", int(time.Since(start).Seconds())))
					}
				})
			case <-ctx.Done():
				return
			}
		}
	}()
	c.status.SetText("AI 回答中 · 0 s")
	go func() {
		answer, err := analysisAIStream(ctx, cfg, turns, onDelta)
		ticker.Stop()
		cancel()
		if c.app.closed.Load() {
			return
		}
		c.app.mw.Synchronize(func() {
			if c.view.IsDisposed() || id != c.requestID {
				return
			}
			flush()
			e := &c.entries[index]
			e.text, e.streaming, e.took = answer, false, time.Since(start)
			c.setBusy(false)
			switch {
			case errors.Is(err, errAIStopped):
				e.stopped = true
				c.refresh()
				c.status.SetText("已停止 · 保留已生成的部分")
			case err != nil:
				if strings.TrimSpace(answer) == "" {
					c.entries = append(c.entries[:index], c.entries[index+1:]...)
				} else {
					e.stopped = true
				}
				c.fail(err)
			default:
				c.turns = append(turns, analysisTurn{Role: "assistant", Content: answer})
				c.refresh()
				c.status.SetText(fmt.Sprintf("AI 回答完成 · %.0f s", e.took.Seconds()))
			}
		})
	}()
}

func (c *aiChat) fail(err error) {
	c.add(chatEntry{role: "error", text: err.Error(), at: time.Now()})
	c.status.SetText("AI 请求失败：" + err.Error())
}

func (c *aiChat) stop() {
	if c.cancel != nil {
		c.cancel()
	}
}

func (c *aiChat) setBusy(busy bool) {
	c.busy = busy
	if !busy {
		c.cancel = nil
	}
	if busy {
		c.button.SetText("停止")
		c.button.SetImage(uiIcon("stop"))
	} else {
		c.button.SetText("发送")
		c.button.SetImage(uiIcon("send"))
	}
	if c.onBusy != nil {
		c.onBusy(busy)
	}
}

func (c *aiChat) clear() {
	if c.busy {
		return
	}
	c.entries, c.turns = nil, nil
	c.refresh()
	c.status.SetText("对话已清空；再提问会重新附带当前数据")
}

func (c *aiChat) copyLast() {
	for i := len(c.entries) - 1; i >= 0; i-- {
		if e := c.entries[i]; (e.role == "assistant" || e.role == "local") && e.text != "" {
			_ = walk.Clipboard().SetText(strings.ReplaceAll(e.text, "\n", "\r\n"))
			c.status.SetText("已复制最近一条回答（Markdown 原文）")
			return
		}
	}
	c.status.SetText("还没有可复制的回答")
}

func (c *aiChat) export() {
	if len(c.entries) == 0 {
		c.status.SetText("还没有可导出的对话")
		return
	}
	c.app.exportText(chatMarkdown(c.entries), "commbox-ai", c.owner())
}

func (c *aiChat) add(e chatEntry) {
	c.entries = append(c.entries, e)
	c.refresh()
}

// refresh 把对话重新排版后写回文本框。新内容只是在末尾追加时直接追加，
// 不整框重写，流式输出时不闪；用户往上翻看时不把视图拽回底部。
func (c *aiChat) refresh() {
	if c.view == nil || c.view.IsDisposed() {
		return
	}
	text := renderChat(c.entries)
	if text == c.shown {
		return
	}
	h := c.view.Handle()
	atBottom := editAtBottom(h)
	if atBottom && strings.HasPrefix(text, c.shown) && c.shown != "" {
		c.view.AppendText(text[len(c.shown):])
	} else {
		first := win.SendMessage(h, win.EM_GETFIRSTVISIBLELINE, 0, 0)
		c.view.SetText(text)
		if atBottom {
			n := c.view.TextLength()
			c.view.SetTextSelection(n, n)
			c.view.ScrollToCaret()
		} else {
			win.SendMessage(h, win.EM_LINESCROLL, 0, first)
		}
	}
	c.shown = text
}

// editAtBottom 判断多行文本框是否已滚到底（或内容不足一屏）。
func editAtBottom(h win.HWND) bool {
	var si win.SCROLLINFO
	si.CbSize = uint32(unsafe.Sizeof(si))
	si.FMask = win.SIF_ALL
	if !win.GetScrollInfo(h, win.SB_VERT, &si) || si.NPage == 0 {
		return true
	}
	return int(si.NPos)+int(si.NPage) >= int(si.NMax)
}

// renderChat 把对话排成纯文本：每条消息一行标题（谁、几点、用时），正文是
// Markdown 转成的可读文本。换行用 CRLF，Windows 文本框才会换行。
func renderChat(entries []chatEntry) string {
	var b strings.Builder
	for i, e := range entries {
		if i > 0 {
			b.WriteString("\n")
		}
		at := e.at.Format("15:04")
		body := e.text
		switch e.role {
		case "divider":
			fmt.Fprintf(&b, "════ %s · %s ════\n", e.text, at)
			continue
		case "user":
			fmt.Fprintf(&b, "── 你 · %s ──\n", at)
		case "local":
			fmt.Fprintf(&b, "── 本地分析 · %s · 未上传 ──\n", at)
			body = markdownPlain(body)
		case "error":
			fmt.Fprintf(&b, "── 出错 · %s ──\n", at)
		case "assistant":
			switch {
			case e.streaming:
				fmt.Fprintf(&b, "── AI · %s · 生成中… ──\n", at)
			case e.stopped:
				fmt.Fprintf(&b, "── AI · %s · 已中止，以下为已生成部分 ──\n", at)
			default:
				fmt.Fprintf(&b, "── AI · %s · %.0f s ──\n", at, e.took.Seconds())
			}
			body = markdownPlain(body)
		}
		b.WriteString(strings.TrimRight(body, "\n"))
		b.WriteString("\n")
	}
	return strings.ReplaceAll(b.String(), "\n", "\r\n")
}

// chatMarkdown 是导出用的原文：标题标出发言人与时间，正文保留 Markdown。
func chatMarkdown(entries []chatEntry) string {
	var b strings.Builder
	b.WriteString("# CommBox AI 对话\n")
	for _, e := range entries {
		at := e.at.Format("2006-01-02 15:04:05")
		switch e.role {
		case "divider":
			fmt.Fprintf(&b, "\n---\n\n*%s · %s*\n", e.text, at)
			continue
		case "user":
			fmt.Fprintf(&b, "\n## 你 · %s\n\n", at)
		case "local":
			fmt.Fprintf(&b, "\n## 本地分析 · %s\n\n", at)
		case "error":
			fmt.Fprintf(&b, "\n## 出错 · %s\n\n", at)
		case "assistant":
			fmt.Fprintf(&b, "\n## AI · %s\n\n", at)
		}
		b.WriteString(strings.TrimRight(e.text, "\n") + "\n")
	}
	return b.String()
}

var (
	mdBold  = regexp.MustCompile(`\*\*(.+?)\*\*|__(.+?)__`)
	mdCode  = regexp.MustCompile("`([^`]+)`")
	mdLink  = regexp.MustCompile(`!?\[([^\]]*)\]\(([^)\s]+)\)`)
	mdOrder = regexp.MustCompile(`^\d+[.)]\s`)
)

// markdownPlain 把模型回答里的 Markdown 转成纯文本框里好读的样子：去掉 # * ` 等符号，
// 标题前加 ■ / ▸，列表用 •，代码块缩进，表格每行转成「表头：值」。文本框用比例字体，
// 靠空格对齐表格列没有意义，所以不排成列。
func markdownPlain(md string) string {
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	var out []string
	var table [][]string
	inCode := false
	blankBefore := func() {
		if len(out) > 0 && out[len(out)-1] != "" {
			out = append(out, "")
		}
	}
	flushTable := func() {
		if len(table) == 0 {
			return
		}
		head, rows := table[0], table[1:]
		if len(rows) == 0 {
			out = append(out, strings.Join(head, "　"))
		}
		for _, row := range rows {
			if len(head) == 2 && len(row) == 2 {
				out = append(out, "• "+row[0]+"："+row[1])
				continue
			}
			parts := make([]string, 0, len(row))
			for i, cell := range row {
				if i < len(head) && head[i] != "" {
					cell = head[i] + "：" + cell
				}
				parts = append(parts, cell)
			}
			out = append(out, "• "+strings.Join(parts, "；"))
		}
		table = nil
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			flushTable()
			inCode = !inCode
			continue
		}
		if inCode {
			out = append(out, "    "+strings.TrimRight(line, " \t"))
			continue
		}
		if strings.HasPrefix(trimmed, "|") && strings.HasSuffix(trimmed, "|") && len(trimmed) > 1 {
			cells := strings.Split(strings.Trim(trimmed, "|"), "|")
			separator := true
			for i := range cells {
				cells[i] = markdownInline(strings.TrimSpace(cells[i]))
				if strings.Trim(cells[i], ":- ") != "" {
					separator = false
				}
			}
			if !separator {
				table = append(table, cells)
			}
			continue
		}
		flushTable()
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		switch {
		case strings.HasPrefix(trimmed, "#"):
			level := len(trimmed) - len(strings.TrimLeft(trimmed, "#"))
			text := markdownInline(strings.TrimSpace(trimmed[level:]))
			blankBefore()
			if level <= 2 {
				out = append(out, "■ "+text)
			} else {
				out = append(out, "▸ "+text)
			}
		case trimmed == "---" || trimmed == "***" || trimmed == "___":
			out = append(out, "────────")
		case strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") || strings.HasPrefix(trimmed, "+ "):
			out = append(out, strings.Repeat("  ", indent/2)+"• "+markdownInline(trimmed[2:]))
		case mdOrder.MatchString(trimmed):
			out = append(out, strings.Repeat("  ", indent/2)+markdownInline(trimmed))
		case strings.HasPrefix(trimmed, ">"):
			out = append(out, "│ "+markdownInline(strings.TrimSpace(strings.TrimPrefix(trimmed, ">"))))
		case trimmed == "":
			if len(out) > 0 && out[len(out)-1] != "" {
				out = append(out, "")
			}
		default:
			out = append(out, markdownInline(strings.TrimRight(line, " \t")))
		}
	}
	flushTable()
	for len(out) > 0 && out[0] == "" {
		out = out[1:]
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}

// markdownInline 去掉行内的粗体、行内代码与链接标记，链接保留地址。
func markdownInline(s string) string {
	s = mdBold.ReplaceAllString(s, "$1$2")
	s = mdCode.ReplaceAllString(s, "$1")
	return mdLink.ReplaceAllStringFunc(s, func(m string) string {
		parts := mdLink.FindStringSubmatch(m)
		if strings.HasPrefix(m, "!") || parts[1] == parts[2] {
			return parts[1]
		}
		return parts[1] + "（" + parts[2] + "）"
	})
}
