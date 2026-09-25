//go:build windows

package vcom

import (
	"fmt"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// relayChunk 是一次中继搬运的最大字节数。
const relayChunk = 64 * 1024

// retryDelay 是「有数据要送,但本端还没有程序打开」时的重试间隔。
// 这种情况下数据留在缓存里等待,不丢弃(规格 2.7)。
const retryDelay = 20 * time.Millisecond

// side 是串口对的一端。
type side struct {
	dev   *device
	inbox *Ring // 等待送给「本端」的数据,即对端写过来的内容

	tx     atomic.Uint64 // 本端写进虚拟口的字节数
	rx     atomic.Uint64 // 已成功送达本端的字节数
	reads  atomic.Uint64
	writes atomic.Uint64
	errs   atomic.Uint64

	// pending 是已从 inbox 取出、但还没成功写给本端的数据。
	// 写失败时保留在这里等下次重试,避免「取出即丢失」。
	pending []byte

	// purgeGen 每次「清空 Buffer」加一。pending 只归 writeLoop 访问,清空时
	// 不能直接动它,writeLoop 发现代数变了就自己丢掉 pending,记入 pendingDropped。
	purgeGen       atomic.Uint64
	pendingDropped atomic.Uint64
}

func (s *side) stats() (used, capacity int, dropped uint64) {
	st := s.inbox.Stats()
	return st.Used, st.Capacity, st.Dropped + s.pendingDropped.Load()
}

// Pair 是一对相互连通的虚拟串口,例如 COM10 ⇄ COM11。
type Pair struct {
	id        string
	createdAt time.Time

	a, b *side

	wg       sync.WaitGroup
	stop     chan struct{}
	stopOnce sync.Once

	mu      sync.Mutex
	enabled bool
	lastErr string
}

// newPair 建立一对虚拟串口并启动双向中继。
// 任一端建立失败都会把已建好的那端拆干净,不会留下「只创建了一端」的半成品
// (规格第 4 节:不能把只创建了一端显示为成功)。
func newPair(id, comA, comB string) (*Pair, error) {
	devA, err := newDevice(comA)
	if err != nil {
		return nil, err
	}
	devB, err := newDevice(comB)
	if err != nil {
		devA.destroy()
		return nil, err
	}

	p := &Pair{
		id:        id,
		createdAt: time.Now(),
		a:         &side{dev: devA, inbox: NewRing(pipeBufferBytes)},
		b:         &side{dev: devB, inbox: NewRing(pipeBufferBytes)},
		stop:      make(chan struct{}),
		enabled:   true,
	}

	// 四个协程:每端各一个「读出→塞给对端」和一个「从缓存取→写给本端」。
	p.wg.Add(4)
	go p.readLoop(p.a, p.b)
	go p.readLoop(p.b, p.a)
	go p.writeLoop(p.a)
	go p.writeLoop(p.b)
	return p, nil
}

func (p *Pair) stopping() bool {
	select {
	case <-p.stop:
		return true
	default:
		return false
	}
}

// readLoop 等待串口软件打开 from 端,把它写入的数据搬进对端的收件缓存。
// 软件关闭端口后复位,继续等下一次打开(规格 F03:支持重复打开关闭)。
func (p *Pair) readLoop(from, peer *side) {
	defer p.wg.Done()
	buf := make([]byte, relayChunk)

	for !p.stopping() {
		if err := from.dev.accept(); err != nil {
			if p.stopping() || isTeardownError(err) {
				return
			}
			p.setError(fmt.Sprintf("%s 等待连接失败: %v", from.dev.com, err))
			from.dev.dropClient()
			continue
		}

		for !p.stopping() {
			n, err := from.dev.read(buf)
			if n > 0 {
				from.tx.Add(uint64(n))
				from.reads.Add(1)
				// 对端缓存满时这里会阻塞等待,而不是丢数据。
				if _, werr := peer.inbox.Write(buf[:n]); werr != nil {
					return // 缓存已关闭,说明串口对正在拆除
				}
			}
			if err != nil {
				if !isTeardownError(err) {
					from.errs.Add(1)
					p.setError(fmt.Sprintf("%s 读取失败: %v", from.dev.com, err))
				}
				break
			}
		}
		from.dev.dropClient()
	}
}

// writeLoop 把收件缓存里的数据写给本端打开着的串口软件。
// 本端暂时没有程序打开时,数据留在 pending 里等待,不丢弃。
func (p *Pair) writeLoop(to *side) {
	defer p.wg.Done()
	buf := make([]byte, relayChunk)
	var gen uint64 // pending 取出时的清空代数

	for !p.stopping() {
		if len(to.pending) == 0 {
			n, err := to.inbox.Read(buf)
			if err != nil {
				return // 缓存关闭,拆除中
			}
			to.pending = buf[:n]
			gen = to.purgeGen.Load()
		}
		if g := to.purgeGen.Load(); g != gen {
			// 取出后、送达前被「清空 Buffer」:这部分也属于待传数据,一并丢弃。
			to.pendingDropped.Add(uint64(len(to.pending)))
			to.pending = nil
			gen = g
			continue
		}

		n, err := to.dev.write(to.pending)
		if n > 0 {
			to.rx.Add(uint64(n))
			to.writes.Add(1)
			to.pending = to.pending[n:]
		}
		if err == nil {
			continue
		}

		if p.stopping() || isFatalWriteError(err) {
			return
		}
		if !isNotConnected(err) {
			to.errs.Add(1)
			p.setError(fmt.Sprintf("%s 写入失败: %v", to.dev.com, err))
		}
		// 没有程序打开本端,或对端刚关闭:留着 pending 稍后重试。
		select {
		case <-p.stop:
			return
		case <-time.After(retryDelay):
		}
	}
}

// isNotConnected 判断「本端当前没有程序打开」,这不是错误,不计入错误统计。
func isNotConnected(err error) bool {
	errno, ok := err.(syscall.Errno)
	if !ok {
		return false
	}
	return errno == errPipeListening || errno == errNoData || errno == errBrokenPipe
}

// isFatalWriteError 判断句柄已经拆掉,协程该退出了。
func isFatalWriteError(err error) bool {
	errno, ok := err.(syscall.Errno)
	if !ok {
		return false
	}
	return errno == errOperationAborted || errno == errInvalidHandle
}

func (p *Pair) setError(msg string) {
	p.mu.Lock()
	p.lastErr = msg
	p.mu.Unlock()
}

// Close 拆除整对虚拟串口:先叫醒所有阻塞中的协程,等它们退出,再关句柄删符号链接。
// 顺序不能反,否则会在别的协程还在用句柄时把它关掉。
func (p *Pair) Close() {
	p.stopOnce.Do(func() {
		close(p.stop)
		p.a.inbox.Close()
		p.b.inbox.Close()
		p.a.dev.wake()
		p.b.dev.wake()
	})
	p.wg.Wait()
	p.a.dev.destroy()
	p.b.dev.destroy()
}

// PurgeBuffers 清空两个方向的待传数据,返回丢弃的总字节数(规格 2.7「清空 Buffer」)。
// 已取出等待重试的 pending 由 writeLoop 随后丢弃,计入统计但不在返回值里。
func (p *Pair) PurgeBuffers() int {
	p.a.purgeGen.Add(1)
	p.b.purgeGen.Add(1)
	return p.a.inbox.Purge() + p.b.inbox.Purge()
}

// ResetStats 清空统计,但不影响缓存与通信(规格 2.8)。
func (p *Pair) ResetStats() {
	for _, s := range []*side{p.a, p.b} {
		s.tx.Store(0)
		s.rx.Store(0)
		s.reads.Store(0)
		s.writes.Store(0)
		s.errs.Store(0)
	}
	p.mu.Lock()
	p.lastErr = ""
	p.mu.Unlock()
}

// Info 返回这对串口的当前快照。
func (p *Pair) Info() PairInfo {
	p.mu.Lock()
	enabled, lastErr := p.enabled, p.lastErr
	p.mu.Unlock()

	return PairInfo{
		ID:        p.id,
		Enabled:   enabled,
		CreatedAt: p.createdAt,
		A:         p.portInfo(p.a, enabled, lastErr),
		B:         p.portInfo(p.b, enabled, lastErr),
		Error:     lastErr,
	}
}

func (p *Pair) portInfo(s *side, enabled bool, lastErr string) PortInfo {
	connected, pid, proc, procErr := s.dev.occupancy()
	used, capacity, dropped := s.stats()

	state := StateIdle
	switch {
	case !enabled:
		state = StateDisabled
	case lastErr != "":
		state = StateError
	case connected:
		state = StateInUse
	}

	return PortInfo{
		Name:           s.dev.com,
		State:          state,
		Process:        proc,
		ProcessNote:    procErr,
		PID:            pid,
		TX:             s.tx.Load(),
		RX:             s.rx.Load(),
		Reads:          s.reads.Load(),
		Writes:         s.writes.Load(),
		Errors:         s.errs.Load(),
		BufferUsed:     used,
		BufferCapacity: capacity,
		BufferDropped:  dropped,
	}
}
