package wincore

import (
	"fmt"
	"net"
	"sync/atomic"
	"time"
)

// backoffDelay 返回第 attempt 次重连的等待时长(基于 base 指数退避,封顶 30s)。
func backoffDelay(base time.Duration, attempt int) time.Duration {
	if base <= 0 {
		base = 2 * time.Second
	}
	d := time.Duration(1<<uint(attempt)) * base
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	return d
}

// reconnectTCP 在 TCP 客户端被动断开后,以指数退避自动重连,直到重连成功或用户断开。
func (e *Engine) reconnectTCP() {
	addr := e.reconnectAddr
	if addr == "" {
		return
	}
	attempt := 0
	for {
		select {
		case <-e.reconnectStop:
			return
		case <-time.After(backoffDelay(e.reconnectInterval, attempt)):
		}
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			attempt++
			atomic.AddUint64(&e.errCount, 1)
			e.emitLog(fmt.Sprintf("TCP 重连 %s 失败: %v", addr, err))
			continue
		}
		e.Lock()
		if e.clients == nil {
			e.clients = map[net.Conn]struct{}{}
		}
		e.clients[conn] = struct{}{}
		e.Unlock()
		atomic.AddUint64(&e.reconnects, 1)
		atomic.StoreInt32(&e.state, int32(StateConnected))
		e.emitLog(fmt.Sprintf("TCP 已重连 %s", addr))
		go e.readTCP(conn)
		return
	}
}
