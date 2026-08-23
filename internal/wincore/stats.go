package wincore

import (
	"sync/atomic"
	"time"
)

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
