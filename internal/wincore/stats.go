package wincore

import (
	"fmt"
	"net"
	"sort"
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
	Mode           Mode
	Listening      bool
	Datagram       bool
	Endpoint       string
	PeerCount      int
	Peers          []string
	State          ConnState
	StartedAt      time.Time
	RXBytes        uint64
	TXBytes        uint64
	RXCount        uint64
	TXCount        uint64
	Reconnects     uint64
	Errors         uint64
	SerialRXBytes  uint64
	SerialTXBytes  uint64
	NetworkRXBytes uint64
	NetworkTXBytes uint64
	ClientIPs      int
}

// Stats 返回当前连接状态与通信统计。
func (e *Engine) Stats() Stats {
	e.Lock()
	defer e.Unlock()
	s := Stats{
		Mode:           e.mode,
		SerialRXBytes:  atomic.LoadUint64(&e.serialRXBytes),
		SerialTXBytes:  atomic.LoadUint64(&e.serialTXBytes),
		NetworkRXBytes: atomic.LoadUint64(&e.networkRXBytes),
		NetworkTXBytes: atomic.LoadUint64(&e.networkTXBytes),
		State:          ConnState(atomic.LoadInt32(&e.state)),
		StartedAt:      time.Unix(0, atomic.LoadInt64(&e.startedAt)),
		RXBytes:        atomic.LoadUint64(&e.rxBytes),
		TXBytes:        atomic.LoadUint64(&e.txBytes),
		RXCount:        atomic.LoadUint64(&e.rxCount),
		TXCount:        atomic.LoadUint64(&e.txCount),
		Reconnects:     atomic.LoadUint64(&e.reconnects),
		Errors:         atomic.LoadUint64(&e.errCount),
	}
	if s.State == StateDisconnected || s.State == StateError {
		return s
	}
	if e.listener != nil {
		s.Listening = true
		s.Endpoint = e.listener.Addr().String()
	}
	uniqueIPs := map[string]bool{}
	for client := range e.clients {
		host, _, _ := net.SplitHostPort(client.RemoteAddr().String())
		uniqueIPs[host] = true
		s.Peers = append(s.Peers, client.RemoteAddr().String())
	}
	s.ClientIPs = len(uniqueIPs)
	sort.Strings(s.Peers)
	s.PeerCount = len(s.Peers)
	if s.Endpoint == "" && len(s.Peers) > 0 {
		s.Endpoint = s.Peers[0]
	}
	if len(s.Peers) > 3 {
		s.Peers = s.Peers[:3]
	}
	if e.udp != nil {
		s.Datagram = true
		s.Endpoint = e.udp.LocalAddr().String()
		if e.udpDialed && e.udpPeer != nil {
			s.Endpoint = e.udpPeer.String()
		}
	}
	if e.httpURL != "" {
		s.Endpoint = e.httpURL
	}
	return s
}
