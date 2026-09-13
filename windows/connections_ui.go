//go:build windows

package main

import (
	"fmt"
	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"serial-tool/internal/wincore"
	"strings"
	"time"
)

// Keep the selected identity even after it disappears. A stale selection must
// fail explicitly instead of silently turning a targeted send into broadcast.
func (a *application) selectedSendTarget() (string, string) {
	if a.sendTarget != nil && a.sendTarget.CurrentIndex() > 0 {
		i := a.sendTarget.CurrentIndex() - 1
		if i < len(a.targets) {
			p := a.targets[i]
			if p.Transport == "UDP" {
				return "", p.RemoteAddress
			}
			return p.ID, ""
		}
		return "missing-connection", ""
	}
	if a.uiMode() == wincore.ModeUDPServer || a.uiMode() == wincore.ModeUDPClient {
		return "", strings.TrimSpace(a.udpTarget.Text())
	}
	return "", ""
}

func (a *application) sendCaptured(input string, asHex bool, eol, id, address string) error {
	data, err := wincore.ParseData(input, asHex, eol)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return fmt.Errorf("请输入要发送的数据")
	}
	if id != "" {
		return a.engine.SendInputToConnection(id, input, asHex, eol)
	}
	if address != "" {
		return a.engine.SendInputToUDP(address, input, asHex, eol)
	}
	return a.engine.Send(input, asHex, eol)
}

func (a *application) validateSend() {
	data, err := wincore.ParseData(a.sendEdit.Text(), a.hexSend.Checked(), a.eol.Text())
	if err != nil {
		a.sendPreview.SetText(err.Error())
		return
	}
	a.sendPreview.SetText(fmt.Sprintf("%d 字节  ·  % X", len(data), data))
}

func (a *application) refreshConnections() {
	if a.sendTarget == nil {
		return
	}
	id, address := a.selectedSendTarget()
	peers := append(a.engine.Connections(), a.engine.UDPPeers()...)
	labels := []string{"默认目标 / TCP 全部客户端"}
	selected := 0
	for i, p := range peers {
		labels = append(labels, p.Transport+"  "+p.RemoteAddress+"  ["+p.ID+"]")
		if id == p.ID || address != "" && address == p.RemoteAddress {
			selected = i + 1
		}
	}
	if id != "" && selected == 0 {
		peers = append(peers, wincore.ConnectionInfo{ID: id, Transport: "TCP"})
		labels = append(labels, "已断开 ["+id+"]")
		selected = len(peers)
	}
	a.targets = peers
	a.sendTarget.SetModel(labels)
	a.sendTarget.SetCurrentIndex(selected)
	if a.peerList != nil {
		old := a.peerList.CurrentIndex()
		list := []string{}
		for _, p := range peers {
			if p.Active {
				list = append(list, p.RemoteAddress)
			}
		}
		a.peerList.SetModel(list)
		if old >= 0 && old < len(list) {
			a.peerList.SetCurrentIndex(old)
		}
		a.peerTitle.SetText(fmt.Sprintf("客户端 / 对端 (%d)", len(list)))
	}
}

func (a *application) selectedPeer() (wincore.ConnectionInfo, bool) {
	i := a.peerList.CurrentIndex()
	if i < 0 || i >= len(a.targets) {
		return wincore.ConnectionInfo{}, false
	}
	return a.targets[i], true
}

func (a *application) peerAction(action string) {
	p, ok := a.selectedPeer()
	if !ok {
		return
	}
	switch action {
	case "send":
		a.sendTarget.SetCurrentIndex(a.peerList.CurrentIndex() + 1)
		a.sendEdit.SetFocus()
	case "filter":
		a.connectionFilter.SetText("id:" + p.ID)
		a.applyFilter()
	case "copy":
		walk.Clipboard().SetText(p.RemoteAddress)
	case "disconnect":
		if p.Transport != "TCP" {
			a.sendPreview.SetText("UDP 对端不建立连接")
			return
		}
		if err := a.engine.DisconnectConnection(p.ID); err != nil {
			a.showError(err)
		}
	case "reset":
		a.peerBaseline[p.ID] = p
	}
}

func (a *application) openConnections() {
	a.refreshConnections()
	var dlg *walk.Dialog
	var maximum *walk.NumberEdit
	var latest *walk.CheckBox
	lines := []string{}
	for _, p := range a.targets {
		if !p.Active {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s  %s\r\nID: %s\r\n本地: %s\r\nRX %s / TX %s · 已连接 %s", p.Transport, p.RemoteAddress, p.ID, p.LocalAddress, wincore.FormatBytes(p.RXBytes), wincore.FormatBytes(p.TXBytes), wincore.FormatDuration(time.Since(p.ConnectedAt))))
	}
	if err := (Dialog{AssignTo: &dlg, Title: "连接管理", Size: Size{Width: 700, Height: 480}, Layout: VBox{}, Children: []Widget{
		Label{Text: "左侧客户端列表可定向发送、过滤、断开和复制地址；同 IP 不同端口按独立会话显示。"},
		TextEdit{ReadOnly: true, VScroll: true, Text: strings.Join(lines, "\r\n\r\n"), StretchFactor: 1},
		Composite{Layout: HBox{}, Children: []Widget{Label{Text: "TCP 最大连接数（0 不限）"}, NumberEdit{AssignTo: &maximum, MinValue: 0, MaxValue: 10000, Value: float64(a.maxConnections), Decimals: 0}}},
		CheckBox{AssignTo: &latest, Text: "串口桥接：仅回复最近请求的 TCP 会话", Checked: a.bridgeLatest},
		PushButton{Text: "应用", OnClicked: func() {
			a.maxConnections = int(maximum.Value())
			a.bridgeLatest = latest.Checked()
			a.engine.SetMaxConnections(a.maxConnections)
			a.engine.SetBridgeReplyLatest(a.bridgeLatest)
			dlg.Accept()
		}},
	}}).Create(a.mw); err != nil {
		a.showError(err)
		return
	}
	defer dlg.Dispose()
	dlg.Run()
}
