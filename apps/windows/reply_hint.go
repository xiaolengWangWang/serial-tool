//go:build windows

package main

import (
	"fmt"
	"time"

	"serial-tool/core"
)

// replyWait 是手动发送后等待应答的时间。Modbus 等设备通常几十毫秒内应答，1 秒足够宽松。
const replyWait = time.Second

// noReplyHint 按工作模式给出没有应答时的排查方向。服务端（可能多个客户端）、
// 串口服务器和 HTTP 不一定有应答或另有响应显示，返回空串表示不提示。
func noReplyHint(mode core.Mode) string {
	switch mode {
	case core.ModeSerial:
		return "核对站号、串口参数、A/B 线和 CRC"
	case core.ModeTCPClient:
		return "核对设备地址、站号和报文格式"
	case core.ModeUDPClient:
		return "UDP 不保证送达，核对设备地址、端口和报文格式"
	}
	return ""
}

// watchReply 在手动发送后等待 replyWait，期间没收到任何数据就在发送状态行提示。
// 只看有没有收到数据，不判断收到的是不是这一条的应答。startedAt 取发送开始的时刻，
// 应答比「发送完成」先到也算收到。
func (a *application) watchReply(startedAt time.Time) {
	a.replySeq++
	hint := noReplyHint(a.uiMode())
	if hint == "" {
		return
	}
	seq := a.replySeq
	time.AfterFunc(replyWait, func() {
		if a.closed.Load() {
			return
		}
		a.mw.Synchronize(func() {
			if a.closed.Load() || seq != a.replySeq || !a.connected || a.lastRX.After(startedAt) {
				return
			}
			a.setSendFeedbackColor(fmt.Sprintf("发送完成 · %s · %d 秒内未收到应答：%s",
				startedAt.Format("15:04:05"), int(replyWait/time.Second), hint), colorYellow)
			a.replyHintShown = true // 在 setSendFeedbackColor 之后置位，它会清掉这个标记
		})
	})
}

// noteRX 记下收到数据的时刻。收到 Modbus 异常应答时在发送状态行说明含义；
// 无应答提示已经显示、数据随后才到时，改为说明数据晚到了。
func (a *application) noteRX(p Packet) {
	a.lastRX = p.TS
	switch {
	case p.Note != "":
		a.setSendFeedbackColor("收到 Modbus "+p.Note+" · "+p.TS.Format("15:04:05"), colorRed)
	case a.replyHintShown:
		a.setSendFeedbackColor("收到数据 · "+p.TS.Format("15:04:05")+"（超过 1 秒才到）", colorMuted)
	}
}
