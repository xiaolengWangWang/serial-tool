package vcom

import (
	"errors"
	"sync"
)

// ErrClosed 表示缓冲区已关闭,读写不再继续。
var ErrClosed = errors.New("虚拟串口缓冲区已关闭")

// Ring 是一个定容环形缓冲,对应真实串口的收发缓存。
//
// 写满时阻塞等待对端读走,而不是丢弃数据(规格 2.7「缓冲区满时等待」);
// 关闭时唤醒所有等待者,并允许把已有数据读完再返回 ErrClosed,
// 这样「正常完成 / 部分完成 / 未完成」才能被区分出来。
type Ring struct {
	mu       sync.Mutex
	notFull  *sync.Cond
	notEmpty *sync.Cond

	buf    []byte
	start  int // 下一个可读位置
	count  int // 已用字节数
	closed bool

	written uint64 // 累计写入
	readOut uint64 // 累计读出
	dropped uint64 // 因 Purge 丢弃
}

// NewRing 创建容量为 capacity 字节的环形缓冲。
func NewRing(capacity int) *Ring {
	if capacity <= 0 {
		panic("vcom: 缓冲区容量必须为正数")
	}
	r := &Ring{buf: make([]byte, capacity)}
	r.notFull = sync.NewCond(&r.mu)
	r.notEmpty = sync.NewCond(&r.mu)
	return r
}

// Write 把 p 全部写入缓冲,空间不足时阻塞等待。
// 返回实际写入的字节数;缓冲区在写入过程中被关闭时返回已写入数与 ErrClosed,
// 调用方据此判断是「全部写完」还是「写了一半」。
func (r *Ring) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	total := 0
	for total < len(p) {
		for r.count == len(r.buf) && !r.closed {
			r.notFull.Wait()
		}
		if r.closed {
			return total, ErrClosed
		}
		n := r.writeLocked(p[total:])
		total += n
		r.written += uint64(n)
		r.notEmpty.Broadcast()
	}
	return total, nil
}

// Read 读出最多 len(p) 字节,无数据时阻塞等待。
// 缓冲区关闭后仍可把残留数据读完,读空后才返回 ErrClosed。
func (r *Ring) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	for r.count == 0 && !r.closed {
		r.notEmpty.Wait()
	}
	if r.count == 0 {
		return 0, ErrClosed
	}
	n := r.readLocked(p)
	r.readOut += uint64(n)
	r.notFull.Broadcast()
	return n, nil
}

// writeLocked 尽可能多地拷入,返回写入字节数。调用前必须持有锁。
func (r *Ring) writeLocked(p []byte) int {
	space := len(r.buf) - r.count
	if space > len(p) {
		space = len(p)
	}
	end := (r.start + r.count) % len(r.buf)
	n := copy(r.buf[end:], p[:space])
	if n < space {
		n += copy(r.buf, p[n:space])
	}
	r.count += n
	return n
}

// readLocked 尽可能多地拷出,返回读出字节数。调用前必须持有锁。
func (r *Ring) readLocked(p []byte) int {
	n := copy(p, r.buf[r.start:min(r.start+r.count, len(r.buf))])
	if n < len(p) && n < r.count {
		n += copy(p[n:], r.buf[:r.count-n])
	}
	r.start = (r.start + n) % len(r.buf)
	r.count -= n
	return n
}

// Purge 丢弃全部待传数据,返回被丢弃的字节数(规格 2.7「清空 Buffer」)。
func (r *Ring) Purge() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := r.count
	r.start, r.count = 0, 0
	r.dropped += uint64(n)
	r.notFull.Broadcast()
	return n
}

// Close 关闭缓冲并唤醒所有等待者。重复调用无副作用。
func (r *Ring) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.closed = true
	r.notFull.Broadcast()
	r.notEmpty.Broadcast()
}

// RingStats 是缓冲区的一次快照,供详情页与诊断报告使用。
type RingStats struct {
	Used     int    `json:"used"`
	Capacity int    `json:"capacity"`
	Written  uint64 `json:"written"`
	Read     uint64 `json:"read"`
	Dropped  uint64 `json:"dropped"`
}

// Stats 返回当前缓冲区快照。
func (r *Ring) Stats() RingStats {
	r.mu.Lock()
	defer r.mu.Unlock()
	return RingStats{
		Used:     r.count,
		Capacity: len(r.buf),
		Written:  r.written,
		Read:     r.readOut,
		Dropped:  r.dropped,
	}
}
