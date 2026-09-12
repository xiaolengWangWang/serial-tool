package wincore

import (
	"context"
	"fmt"
	"net"
	"sync/atomic"
	"time"
)

func backoffDelay(base time.Duration, attempt int) time.Duration {
	if base <= 0 {
		base = 2 * time.Second
	}
	if base >= 30*time.Second {
		return 30 * time.Second
	}
	for i := 0; i < attempt; i++ {
		if base >= 15*time.Second {
			return 30 * time.Second
		}
		base *= 2
	}
	return base
}

func (e *Engine) reconnectTCP(epoch uint64, stop chan struct{}) {
	e.Lock()
	addr, interval := e.reconnectAddr, e.reconnectInterval
	e.Unlock()
	if addr == "" || stop == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-stop:
			cancel()
		case <-ctx.Done():
		}
	}()
	for attempt := 0; ; attempt++ {
		timer := time.NewTimer(backoffDelay(interval, attempt))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", addr)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			atomic.AddUint64(&e.errCount, 1)
			e.emitLog(fmt.Sprintf("TCP 重连 %s 失败: %v", addr, err))
			continue
		}
		e.Lock()
		if e.epoch != epoch || e.reconnectStop != stop {
			e.Unlock()
			conn.Close()
			return
		}
		tracked := e.addTCPConnection(conn, epoch)
		atomic.AddUint64(&e.reconnects, 1)
		atomic.StoreInt32(&e.state, int32(StateConnected))
		e.Unlock()
		e.emitLog(fmt.Sprintf("TCP 已重连 %s", addr))
		go e.readTCP(tracked)
		return
	}
}
