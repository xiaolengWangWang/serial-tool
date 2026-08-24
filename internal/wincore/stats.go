package wincore

import (
	"fmt"
	"sync/atomic"
	"time"
)

// FormatBytes 把字节数格式化为人类可读的 KB/MB/GB。
func FormatBytes(n uint64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.2f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// FormatDuration 把时长格式化为 HH:MM:SS。
func FormatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	d = d.Round(time.Second)
	h := d / time.Hour
	m := (d % time.Hour) / time.Minute
	s := (d % time.Minute) / time.Second
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}

// ConnState 连接状态。UI 不自行推断连接状态,统一由核心引擎提供。
type ConnState int32

const (
	StateDisconnected ConnState = iota
	StateConnecting
	StateConnected
	StateReconnecting
	StateClosing
	StateError
)

// Stats 是通信统计快照。
type Stats struct {
	State      ConnState
	StartedAt  time.Time
	RXBytes    uint64
	TXBytes    uint64
	RXCount    uint64
	TXCount    uint64
	Reconnects uint64
	Errors     uint64
}

// Stats 返回当前连接状态与通信统计。
func (e *Engine) Stats() Stats {
	return Stats{
		State:      ConnState(atomic.LoadInt32(&e.state)),
		StartedAt:  time.Unix(0, atomic.LoadInt64(&e.startedAt)),
		RXBytes:    atomic.LoadUint64(&e.rxBytes),
		TXBytes:    atomic.LoadUint64(&e.txBytes),
		RXCount:    atomic.LoadUint64(&e.rxCount),
		TXCount:    atomic.LoadUint64(&e.txCount),
		Reconnects: atomic.LoadUint64(&e.reconnects),
		Errors:     atomic.LoadUint64(&e.errCount),
	}
}
