package wincore

import (
	"fmt"
	"io"
	"net"
	"os"
	"sort"

	"github.com/creack/pty"
	"golang.org/x/term"
)

type vBridge struct {
	id   int
	addr string
	link string
	ptmx *os.File
	tty  *os.File
	conn net.Conn
}

// VSerialInfo 描述一个后台虚拟串口桥接。
type VSerialInfo struct {
	ID   int
	Addr string
	Link string
}

// AddVirtualSerial 连接一个 TCP 端点并新建一个后台虚拟串口桥接,
// 与主连接及其它桥接互不影响,可同时存在多个。返回设备信息。
func (e *Engine) AddVirtualSerial(addr string) (VSerialInfo, error) {
	if addr == "" {
		return VSerialInfo{}, fmt.Errorf("请输入要桥接的 IP:端口")
	}
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return VSerialInfo{}, err
	}
	ptmx, tty, err := pty.Open()
	if err != nil {
		_ = conn.Close()
		return VSerialInfo{}, fmt.Errorf("创建虚拟串口失败(该平台可能不支持): %w", err)
	}
	// raw 模式:关回显与行规程,避免二进制数据被改写或形成回显环路
	if _, err := term.MakeRaw(int(tty.Fd())); err != nil {
		e.emitLog("虚拟串口 raw 模式设置失败: " + err.Error())
	}

	e.Lock()
	e.vseq++
	id := e.vseq
	e.Unlock()
	link := fmt.Sprintf("/tmp/GoSerialTool-vserial-%d", id)
	_ = os.Remove(link)
	if os.Symlink(tty.Name(), link) != nil {
		link = tty.Name() // 建软链失败则用真实设备路径
	}

	b := &vBridge{id: id, addr: addr, link: link, ptmx: ptmx, tty: tty, conn: conn}
	e.Lock()
	e.vbridges[id] = b
	e.Unlock()

	// 双向透传;任一方向结束即拆除该桥
	go func() { _, _ = io.Copy(ptmx, conn); e.RemoveVirtualSerial(id) }()
	go func() { _, _ = io.Copy(conn, ptmx); e.RemoveVirtualSerial(id) }()

	e.emitLog(fmt.Sprintf("虚拟串口 #%d 已创建: %s ↔ %s", id, link, addr))
	return VSerialInfo{ID: id, Addr: addr, Link: link}, nil
}

// RemoveVirtualSerial 停止并清理指定桥接,幂等(可被两个方向的 goroutine 重复调用)。
func (e *Engine) RemoveVirtualSerial(id int) {
	e.Lock()
	b := e.vbridges[id]
	delete(e.vbridges, id)
	e.Unlock()
	if b == nil {
		return
	}
	if b.ptmx != nil {
		_ = b.ptmx.Close()
	}
	if b.tty != nil {
		_ = b.tty.Close()
	}
	if b.conn != nil {
		_ = b.conn.Close()
	}
	if b.link != "" {
		_ = os.Remove(b.link)
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
