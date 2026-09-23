package wincore

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"syscall"
	"time"
)

// DisconnectSide 说明一次断开是哪一端发起的。
type DisconnectSide int

const (
	SideUnknown DisconnectSide = iota // 无法判断
	SideLocal                         // 本端(本程序)发起
	SideRemote                        // 对端发起
	SideLink                          // 两端都没主动断,链路本身出问题
)

// Windows 的 socket 错误码不落在 syscall.E* 常量上,这里按数值匹配。
// 在 Unix 上这些数值不会出现,与 syscall.E* 同时比较不会误判。
const (
	wsaeNetReset    = syscall.Errno(10052)
	wsaeConnAborted = syscall.Errno(10053)
	wsaeConnReset   = syscall.Errno(10054)
	wsaeTimedOut    = syscall.Errno(10060)
)

// DisconnectReason 描述一次断开:哪一端发起、以什么方式,以及底层错误。
type DisconnectReason struct {
	Side   DisconnectSide
	Detail string // 断开方式,例如"对端正常关闭(收到 FIN)"
	Err    error  // 读循环拿到的原始错误,可能为 nil
}

// classifyDisconnect 根据读循环返回的错误判断这次断开由谁发起。
// manual 为真表示本端调用过关闭(整体断开或单独断开某个连接)。
func classifyDisconnect(err error, manual bool) DisconnectReason {
	switch {
	case manual:
		return DisconnectReason{Side: SideLocal, Detail: "本端调用了断开", Err: err}
	case err == nil:
		return DisconnectReason{Side: SideUnknown, Detail: "读取结束但没有错误"}
	case errors.Is(err, net.ErrClosed), errors.Is(err, os.ErrClosed):
		return DisconnectReason{Side: SideLocal, Detail: "本端已关闭连接", Err: err}
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return DisconnectReason{Side: SideRemote, Detail: "对端正常关闭(收到 FIN)", Err: err}
	case isErrno(err, syscall.ECONNRESET, wsaeConnReset):
		return DisconnectReason{Side: SideRemote, Detail: "对端异常断开(连接被重置,通常是对端进程退出或崩溃)", Err: err}
	case isErrno(err, syscall.ECONNABORTED, wsaeConnAborted):
		return DisconnectReason{Side: SideLink, Detail: "连接被本机网络栈中止", Err: err}
	case isErrno(err, syscall.ENETRESET, wsaeNetReset):
		return DisconnectReason{Side: SideLink, Detail: "链路中断,连接已失效", Err: err}
	case isErrno(err, syscall.ETIMEDOUT, wsaeTimedOut), isTimeout(err):
		return DisconnectReason{Side: SideLink, Detail: "读取超时,对端无响应", Err: err}
	default:
		return DisconnectReason{Side: SideUnknown, Detail: "未识别的错误", Err: err}
	}
}

// classifySerialDisconnect 判断串口断开:串口没有"对端进程"的概念,
// 不是本端关的就只能是设备或驱动侧断的,不能含糊说成"对端断开"。
func classifySerialDisconnect(err error, manual bool) DisconnectReason {
	switch {
	case manual:
		return DisconnectReason{Side: SideLocal, Detail: "本端调用了断开", Err: err}
	case err == nil:
		return DisconnectReason{Side: SideUnknown, Detail: "读取结束但没有错误"}
	case errors.Is(err, os.ErrClosed), errors.Is(err, net.ErrClosed):
		return DisconnectReason{Side: SideLocal, Detail: "本端已关闭串口", Err: err}
	default:
		return DisconnectReason{Side: SideRemote, Detail: "串口设备断开(拔出、驱动移除或提供端退出)", Err: err}
	}
}

func isErrno(err error, want ...syscall.Errno) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	for _, w := range want {
		if errno == w {
			return true
		}
	}
	return false
}

func isTimeout(err error) bool {
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// peerName 给对端一个明确的称呼:我们接受的连接,对端就是客户端;
// 我们拨出去的连接,对端就是服务端。
func peerName(inbound bool) string {
	if inbound {
		return "客户端"
	}
	return "服务端"
}

func localName(inbound bool) string {
	if inbound {
		return "本端(服务端)"
	}
	return "本端(客户端)"
}

// Who 回答"是谁断的",例如"服务端断开"。
func (r DisconnectReason) Who(inbound bool) string {
	switch r.Side {
	case SideLocal:
		return localName(inbound) + "主动断开"
	case SideRemote:
		return peerName(inbound) + "断开"
	case SideLink:
		return "链路中断"
	default:
		return "断开来源未知"
	}
}

// disconnectStats 是断开时要一并写进日志的连接用量。
type disconnectStats struct {
	RXBytes uint64
	TXBytes uint64
	RXCount uint64
	TXCount uint64
}

// disconnectLine 拼出写进日志和数据库的详细断开报文。
func disconnectLine(prefix, remote string, inbound bool, duration time.Duration, s disconnectStats, r DisconnectReason) string {
	parts := []string{
		fmt.Sprintf("%s:%s", prefix, r.Who(inbound)),
		r.Detail,
	}
	if remote != "" {
		parts = append(parts, peerName(inbound)+" "+remote)
	}
	parts = append(parts,
		"持续 "+FormatDuration(duration),
		fmt.Sprintf("收 %s/%d 帧 发 %s/%d 帧", FormatBytes(s.RXBytes), s.RXCount, FormatBytes(s.TXBytes), s.TXCount),
	)
	if r.Err != nil {
		parts = append(parts, "底层错误 "+r.Err.Error())
	}
	return strings.Join(parts, " · ")
}

// serialDisconnectLine 是串口版本:没有对端角色,也不分收发帧数。
func serialDisconnectLine(endpoint string, duration time.Duration, rx, tx uint64, r DisconnectReason) string {
	who := "串口设备断开"
	if r.Side == SideLocal {
		who = "本端主动断开"
	} else if r.Side == SideUnknown {
		who = "断开来源未知"
	}
	parts := []string{"串口已断开:" + who, r.Detail}
	if endpoint != "" {
		parts = append(parts, "端口 "+endpoint)
	}
	parts = append(parts,
		"持续 "+FormatDuration(duration),
		fmt.Sprintf("收 %s 发 %s", FormatBytes(rx), FormatBytes(tx)),
	)
	if r.Err != nil {
		parts = append(parts, "底层错误 "+r.Err.Error())
	}
	return strings.Join(parts, " · ")
}
