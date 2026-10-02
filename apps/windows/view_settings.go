//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"serial-tool/core"
)

type viewSettings struct{ Connection, Data, Log, Send, AI bool }

func defaultViewSettings() viewSettings {
	return viewSettings{Connection: true, Data: true, Log: true, Send: true}
}
func loadViewSettings(engine *core.Engine) viewSettings {
	v := defaultViewSettings()
	if raw := engine.GetSetting("windows.view"); raw != "" {
		var saved viewSettings
		if json.Unmarshal([]byte(raw), &saved) == nil && (saved.Connection || saved.Data || saved.Log || saved.Send || saved.AI) {
			v = saved
		}
	}
	return v
}

func (a *application) applyViewSettings(v viewSettings) error {
	if !v.Connection && !v.Data && !v.Log && !v.Send && !v.AI {
		return fmt.Errorf("请至少选择一项显示内容")
	}
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err = a.engine.SetSetting("windows.view", string(data)); err != nil {
		return err
	}
	a.views = v
	a.applyViews()
	return nil
}

func (a *application) applyViews() {
	a.connectionPage = false
	a.viewData.SetVisible(a.views.Data)
	a.viewLog.SetVisible(a.views.Log)
	a.dataPane.SetVisible(a.views.Data || a.views.Log)
	a.sendPane.SetVisible(a.views.Send)
	a.showLogView(!a.views.Data || (a.views.Log && a.viewLog.Checked()))
	if a.views.AI {
		a.showAssistant()
	} else {
		a.assistant.panel.SetVisible(false)
		a.arrangePanes(false)
	}
	a.balanceSendArea()
}

func (a *application) showViewSettings() {
	var dlg *walk.Dialog
	var connection, data, log, send, ai *walk.CheckBox
	var feedback *walk.Label
	v := a.views
	if err := (Dialog{AssignTo: &dlg, Title: "视图设置", Size: Size{Width: 440, Height: 420}, MinSize: Size{Width: 380, Height: 380}, Font: Font{Family: fontUI, PointSize: sizeBody}, Layout: VBox{Margins: Margins{Left: 18, Top: 16, Right: 18, Bottom: 16}, Spacing: 12}, Children: []Widget{
		Label{Text: "选择主页面显示的内容", Font: fontSection, TextColor: colorBlue},
		Label{Text: "隐藏区域只改变显示，不会断开连接、停止收发或删除数据。"},
		CheckBox{AssignTo: &connection, Text: "连接配置", MinSize: Size{Height: 26}, Checked: v.Connection},
		CheckBox{AssignTo: &data, Text: "数据表格", MinSize: Size{Height: 26}, Checked: v.Data},
		CheckBox{AssignTo: &log, Text: "运行日志", MinSize: Size{Height: 26}, Checked: v.Log},
		CheckBox{AssignTo: &send, Text: "发送区", MinSize: Size{Height: 26}, Checked: v.Send},
		CheckBox{AssignTo: &ai, Text: "AI 聊天助手", MinSize: Size{Height: 26}, Checked: v.AI},
		Label{Text: "数据与日志都勾选时，可在标题栏切换。\r\n小窗口按空间自动切换分区；设置会在下次启动时保留。", TextColor: colorMuted},
		Label{AssignTo: &feedback, TextColor: colorRed, EllipsisMode: EllipsisEnd},
		Composite{Layout: HBox{MarginsZero: true, Spacing: 8}, Children: []Widget{
			PushButton{Text: "恢复默认", OnClicked: func() {
				d := defaultViewSettings()
				connection.SetChecked(d.Connection)
				data.SetChecked(d.Data)
				log.SetChecked(d.Log)
				send.SetChecked(d.Send)
				ai.SetChecked(d.AI)
			}}, HSpacer{},
			PushButton{Text: "应用", MinSize: Size{Width: 84, Height: 32}, OnClicked: func() {
				if err := a.applyViewSettings(viewSettings{connection.Checked(), data.Checked(), log.Checked(), send.Checked(), ai.Checked()}); err != nil {
					feedback.SetText(chineseError(err))
					return
				}
				dlg.Accept()
			}},
		}},
	}}).Create(a.mw); err != nil {
		a.showError(err)
		return
	}
	defer dlg.Dispose()
	dlg.Run()
}
