//go:build windows

package main

import "serial-tool/core"

func (a *application) updateConnectionHints() {
	if a.connectionHint == nil || a.reconnectPane == nil || a.mode == nil || a.role == nil || a.protocol == nil {
		return
	}
	mode := a.uiMode()
	reconnect := mode == core.ModeTCPClient || (mode == core.ModeSerialServer && a.role.CurrentIndex() == 1 && a.protocol.CurrentIndex() == 0)
	a.reconnectPane.SetVisible(reconnect && a.reconnectExpanded)
	if a.reconnectToggle != nil {
		a.reconnectToggle.SetVisible(reconnect)
	}
	hint := "客户端填写远端地址；服务端填写本机监听地址。"
	switch a.mode.Text() {
	case "串口":
		hint = "参数应与设备说明一致。二进制数据使用 HEX 显示。"
	case "UDP":
		hint = "UDP 不保证送达；已连接不代表对端在线。"
	case "串口服务器":
		hint = "串口与网络之间转发数据。回复策略见连接数与桥接设置。"
	case "HTTP 客户端":
		hint = "URL 包含 http:// 或 https://。准备后可进入 HTTP 工作台。"
	}
	a.connectionHint.SetText(hint)
	a.connectionHint.SetToolTipText(hint)
	showPeers := a.isServer() || a.mode.Text() == "UDP"
	if a.peerList != nil {
		a.peerList.SetVisible(showPeers)
		a.peerTitle.SetVisible(showPeers)
		a.detailsLabel.SetVisible(showPeers)
	}
}
