package wincore

import (
	"fmt"
	"net"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/creack/pty"
	"golang.org/x/term"
)

type vBridge struct {
	id      int
	addr    string
	link    string
	ptmx    *os.File
	tty     *os.File
	mu      sync.Mutex
	conn    net.Conn
	stop    chan struct{}
	once      sync.Once
	dropped   uint64 // 断线/重连期间被丢弃的字节数(原子)
	sessionID int64  // 该桥接的 SQLite 会话 ID(0 表示不记录)
}

// VSerialInfo 描述一个后台虚拟串口桥接。
type VSerialInfo struct {
	ID   int
	Addr string
	Link string
}

// makeRaw 便于测试注入 term.MakeRaw 的失败路径,默认即 term.MakeRaw。
var makeRaw = term.MakeRaw

// AddVirtualSerial 连接一个 TCP 端点并新建一个后台虚拟串口桥接,
// 与主连接及其它桥接互不影响,可同时存在多个。设备常驻:TCP 断开会自动重连。
func (e *Engine) AddVirtualSerial(addr string) (VSerialInfo, error) {
	if addr == "" {
		return VSerialInfo{}, fmt.Errorf("请输入要桥接的 IP:端口")
	}
	ptmx, tty, err := pty.Open()
	if err != nil {
		return VSerialInfo{}, fmt.Errorf("创建虚拟串口失败(该平台可能不支持): %w", err)
	}
	// raw 模式:关回显与行规程,避免二进制数据被改写或形成回显环路。
	// 失败即终止创建并清理资源,不允许降级运行(二进制透明传输依赖 raw 模式)。
	if _, err := makeRaw(int(tty.Fd())); err != nil {
		name := tty.Name()
		_ = ptmx.Close()
		_ = tty.Close()
		e.emitLog(fmt.Sprintf("虚拟串口创建失败: raw 模式设置失败(%s): %v", name, err))
		return VSerialInfo{}, fmt.Errorf("虚拟串口 raw 模式设置失败(%s): %w", name, err)
	}

	e.Lock()
	e.vseq++
	id := e.vseq
	e.Unlock()
	// 设备名带进程 PID,保证多实例(多进程)不会撞名、互相覆盖软链
	link := fmt.Sprintf("/tmp/CommBox-vserial-%d-%d", os.Getpid(), id)
	_ = os.Remove(link)
	if os.Symlink(tty.Name(), link) != nil {
		link = tty.Name() // 建软链失败则用真实设备路径
	}

	// TCP 连接放到后台:服务端未启动时也允许先创建虚拟串口,
	// 由 vDialLoop 统一负责首次连接与断线重连(conn 初始为 nil)。
	sessionID, _ := e.store.NewSession("虚拟串口", addr, "") // 失败则 sessionID=0,不记录数据
	b := &vBridge{id: id, addr: addr, link: link, ptmx: ptmx, tty: tty, stop: make(chan struct{}), sessionID: sessionID}
	e.Lock()
	e.vbridges[id] = b
	e.Unlock()

	go e.vPtmxReader(b) // 虚拟串口 → 当前网络连接(常驻)
	go e.vDialLoop(b)   // 网络 → 虚拟串口,首次连接与断线重连走同一套机制

	e.emitLog(fmt.Sprintf("虚拟串口 #%d 已创建: %s ↔ %s(后台自动连接)", id, link, addr))
	return VSerialInfo{ID: id, Addr: addr, Link: link}, nil
}

// vPtmxReader 常驻:把虚拟串口写入的数据发往当前 TCP 连接。
// 断线/重连期间无连接时数据会被丢弃:本版本不缓存、不补发,但必须计数并告警,禁止静默丢失。
func (e *Engine) vPtmxReader(b *vBridge) {
	buf := make([]byte, 4096)
	dropping := false
	for {
		n, err := b.ptmx.Read(buf)
		if n > 0 {
			if b.sessionID != 0 {
				_ = e.store.ReceivedForSession(b.sessionID, fmt.Sprintf("虚拟串口 #%d 发送", b.id), append([]byte(nil), buf[:n]...))
			}
			b.mu.Lock()
			c := b.conn
			b.mu.Unlock()
			lost := false
			if c == nil {
				lost = true // 重连中,无连接可写
			} else if _, werr := c.Write(buf[:n]); werr != nil {
				lost = true // 连接刚好断开,写入失败
			}
			if lost {
				atomic.AddUint64(&b.dropped, uint64(n))
				if !dropping {
					e.emitLog(fmt.Sprintf("虚拟串口 #%d 无连接,串口数据被丢弃(累计 %d 字节)", b.id, atomic.LoadUint64(&b.dropped)))
					dropping = true
				}
			} else {
				dropping = false
			}
		}
		if err != nil {
			return // ptmx 关闭,桥接已拆除
		}
	}
}

// vDialLoop 把 TCP 数据泵入虚拟串口;首次连接与断线重连走同一套机制,
// 连不上则每 2s 重试,直到桥接被移除。
func (e *Engine) vDialLoop(b *vBridge) {
	for {
		conn := e.vConnect(b)
		if conn == nil {
			return // stop 已关闭,桥接被移除
		}
		b.mu.Lock()
		b.conn = conn
		b.mu.Unlock()
		e.emitLog(fmt.Sprintf("虚拟串口 #%d 已连接 %s", b.id, b.addr))

		// 网络 → 虚拟串口,逐块写入并记录到 SQLite
		rbuf := make([]byte, 4096)
		for {
			n, rerr := conn.Read(rbuf)
			if n > 0 {
				_, _ = b.ptmx.Write(rbuf[:n])
				if b.sessionID != 0 {
					_ = e.store.ReceivedForSession(b.sessionID, fmt.Sprintf("虚拟串口 #%d 接收", b.id), append([]byte(nil), rbuf[:n]...))
				}
			}
			if rerr != nil {
				break
			}
		}

		b.mu.Lock()
		if b.conn == conn {
			b.conn = nil
		}
		b.mu.Unlock()
		_ = conn.Close()

		select {
		case <-b.stop:
			return
		default:
		}
		e.emitLog(fmt.Sprintf("虚拟串口 #%d 到 %s 的连接断开,重连中...", b.id, b.addr))
	}
}

// vConnect 建立到 b.addr 的 TCP 连接;失败则每 2s 重试。返回 nil 表示桥接已被移除。
func (e *Engine) vConnect(b *vBridge) net.Conn {
	for {
		c, err := net.Dial("tcp", b.addr)
		if err == nil {
			return c
		}
		e.emitLog(fmt.Sprintf("虚拟串口 #%d 连接 %s 失败: %v", b.id, b.addr, err))
		select {
		case <-b.stop:
			return nil
		case <-time.After(2 * time.Second):
		}
	}
}

// RemoveVirtualSerial 停止并清理指定桥接,幂等。
func (e *Engine) RemoveVirtualSerial(id int) {
	e.Lock()
	b := e.vbridges[id]
	delete(e.vbridges, id)
	e.Unlock()
	if b == nil {
		return
	}
	b.once.Do(func() { close(b.stop) })
	b.mu.Lock()
	c := b.conn
	b.conn = nil
	b.mu.Unlock()
	if c != nil {
		_ = c.Close()
	}
	if b.ptmx != nil {
		_ = b.ptmx.Close()
	}
	if b.tty != nil {
		_ = b.tty.Close()
	}
	if b.link != "" {
		_ = os.Remove(b.link)
	}
	if b.sessionID != 0 {
		e.store.EndSessionID(b.sessionID)
	}
	e.emitLog(fmt.Sprintf("虚拟串口 #%d 已停止", id))
}

// ListVirtualSerials 返回当前所有后台虚拟串口桥接(按 id 升序)。
func (e *Engine) ListVirtualSerials() []VSerialInfo {
	e.Lock()
	out := make([]VSerialInfo, 0, len(e.vbridges))
	for _, b := range e.vbridges {
		out = append(out, VSerialInfo{ID: b.id, Addr: b.addr, Link: b.link})
	}
	e.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (e *Engine) removeAllVirtualSerials() {
	e.Lock()
	ids := make([]int, 0, len(e.vbridges))
	for id := range e.vbridges {
		ids = append(ids, id)
	}
	e.Unlock()
	for _, id := range ids {
		e.RemoveVirtualSerial(id)
	}
}
