//go:build windows

package main

import (
	"strings"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"serial-tool/docs"
)

type manualChapter struct{ title, body string }

func manualChapters(query string) []manualChapter {
	query = strings.ToLower(strings.TrimSpace(query))
	var chapters []manualChapter
	for i, part := range strings.Split(docs.Manual, "\n## ") {
		title, _, _ := strings.Cut(part, "\n")
		title = strings.TrimSpace(strings.TrimPrefix(title, "# "))
		if query != "" && !strings.Contains(strings.ToLower(part), query) {
			continue
		}
		if i > 0 {
			part = "## " + part
		}
		chapters = append(chapters, manualChapter{title, markdownPlain(part)})
	}
	return chapters
}

func (a *application) showHelp() {
	if a.manualWindow != nil && !a.manualWindow.IsDisposed() {
		a.manualWindow.Show()
		a.manualWindow.SetFocus()
		return
	}
	var query *walk.LineEdit
	var chapters *walk.ListBox
	var body *walk.TextEdit
	var status *walk.Label
	var matches []manualChapter
	refresh := func() {
		if chapters == nil || body == nil {
			return
		}
		matches = manualChapters(query.Text())
		titles := make([]string, len(matches))
		for i, chapter := range matches {
			titles[i] = chapter.title
		}
		chapters.SetModel(titles)
		if len(matches) == 0 {
			body.SetText("没有找到匹配的章节。请换一个关键词，或清空搜索查看全部内容。")
			status.SetText("未找到匹配章节")
		} else {
			chapters.SetCurrentIndex(0)
			status.SetText("搜索匹配章节标题与正文；选择左侧章节阅读。")
		}
	}
	err := (MainWindow{
		AssignTo: &a.manualWindow, Title: "CommBox · 完整使用手册", Size: Size{Width: 980, Height: 720}, MinSize: Size{Width: 660, Height: 440},
		Font: Font{Family: fontUI, PointSize: sizeBody}, Layout: VBox{Margins: Margins{Left: 16, Top: 14, Right: 16, Bottom: 14}, Spacing: 10},
		Children: []Widget{
			Label{Text: "使用手册", Font: fontSection, TextColor: colorBlue},
			Composite{Layout: HBox{MarginsZero: true, Spacing: 8}, Children: []Widget{
				Label{Text: "搜索章节"}, LineEdit{AssignTo: &query, CueBanner: "例如：串口、定时、附件、错误", OnTextChanged: refresh},
				PushButton{Text: "清空搜索", OnClicked: func() { query.SetText("") }},
			}},
			Composite{Layout: HBox{MarginsZero: true, Spacing: 12}, StretchFactor: 1, Children: []Widget{
				ListBox{AssignTo: &chapters, MinSize: Size{Width: 190}, MaxSize: Size{Width: 220}, OnCurrentIndexChanged: func() {
					i := chapters.CurrentIndex()
					if i >= 0 && i < len(matches) {
						body.SetText(strings.ReplaceAll(matches[i].body, "\n", "\r\n"))
						body.SetTextSelection(0, 0)
					}
				}},
				TextEdit{AssignTo: &body, ReadOnly: true, VScroll: true, Background: SolidColorBrush{Color: walk.RGB(255, 255, 255)}, StretchFactor: 1},
			}},
			Label{AssignTo: &status, TextColor: colorMuted},
		},
	}).Create()
	if err != nil {
		a.showError(err)
		return
	}
	if icon := uiIcon("app"); icon != nil {
		a.manualWindow.SetIcon(icon)
	}
	a.manualWindow.Closing().Attach(func(canceled *bool, _ walk.CloseReason) {
		*canceled = true
		a.manualWindow.Hide()
	})
	a.mw.Disposing().Attach(func() { a.manualWindow.Dispose() })
	refresh()
	a.manualWindow.Show()
	if rc, ok := workAreaFor(a.manualWindow.Handle()); ok {
		b := a.manualWindow.BoundsPixels()
		w, h := min(b.Width, int(rc.Right-rc.Left)), min(b.Height, int(rc.Bottom-rc.Top))
		a.manualWindow.SetBoundsPixels(walk.Rectangle{X: int(rc.Left) + (int(rc.Right-rc.Left)-w)/2, Y: int(rc.Top) + (int(rc.Bottom-rc.Top)-h)/2, Width: w, Height: h})
	}
}
