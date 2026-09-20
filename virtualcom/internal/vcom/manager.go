//go:build windows

package vcom

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// comSearchStart 是自动选号的起点。COM1/COM2 常被真实设备或系统占着,
// 从 3 开始找可以少撞一点。
const comSearchStart = 3

// comSearchEnd 是自动选号的上限。Windows 允许的 COM 号远不止这些,
// 但常见串口软件的下拉框普遍只列到 COM255。
const comSearchEnd = 255

// entry 是管理器里的一条记录。禁用时 pair 为 nil,但 COM 号等配置保留,
// 重新启用时按原编号恢复(规格 2.1「禁用后保留配置」)。
type entry struct {
	id       string
	comA     string
	comB     string
	enabled  bool
	pair     *Pair
	lastInfo PairInfo // 禁用期间用于展示的最后一次快照
}

// Manager 管理全部虚拟串口对。
type Manager struct {
	mu      sync.Mutex
	entries map[string]*entry
	order   []string
	seq     int
}

func NewManager() *Manager {
	return &Manager{entries: make(map[string]*entry)}
}

// Create 创建一对虚拟串口。comA / comB 传空表示自动选两个空闲编号。
func (m *Manager) Create(comA, comB string) (PairInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	comA, comB, err := m.resolveNamesLocked(comA, comB)
	if err != nil {
		return PairInfo{}, err
	}

	m.seq++
	id := fmt.Sprintf("pair-%d", m.seq)
	p, err := newPair(id, comA, comB)
	if err != nil {
		m.seq--
		return PairInfo{}, err
	}

	e := &entry{id: id, comA: comA, comB: comB, enabled: true, pair: p}
	m.entries[id] = e
	m.order = append(m.order, id)
	return p.Info(), nil
}

// resolveNamesLocked 校验或自动分配两个 COM 号。
func (m *Manager) resolveNamesLocked(comA, comB string) (string, string, error) {
	used, err := UsedCOMNames()
	if err != nil {
		return "", "", err
	}
	// 把本管理器已经登记(含禁用中)的编号也算作已占用,
	// 禁用中的端口虽然当前没有符号链接,但编号要留着给它。
	for _, e := range m.entries {
		used[strings.ToUpper(e.comA)] = true
		used[strings.ToUpper(e.comB)] = true
	}

	switch {
	case comA == "" && comB == "":
		return m.pickTwoFree(used)
	case comA == "" || comB == "":
		return "", "", fmt.Errorf("两端编号要么都自动分配,要么都手动指定")
	}

	comA, comB = strings.ToUpper(comA), strings.ToUpper(comB)
	if comA == comB {
		return "", "", fmt.Errorf("两端不能使用同一个编号 %s", comA)
	}
	for _, name := range []string{comA, comB} {
		if comNumber(name) == 0 {
			return "", "", fmt.Errorf("%q 不是合法的 COM 名,应形如 COM10", name)
		}
		if used[name] {
			return "", "", fmt.Errorf("%s 已被占用,请换一个编号", name)
		}
	}
	return comA, comB, nil
}

func (m *Manager) pickTwoFree(used map[string]bool) (string, string, error) {
	var free []string
	for n := comSearchStart; n <= comSearchEnd && len(free) < 2; n++ {
		name := fmt.Sprintf("COM%d", n)
		if !used[name] {
			free = append(free, name)
		}
	}
	if len(free) < 2 {
		return "", "", fmt.Errorf("COM%d~COM%d 之间找不到两个空闲编号", comSearchStart, comSearchEnd)
	}
	return free[0], free[1], nil
}

// busyReason 返回「某一端正在被程序使用」的说明,没有被占用时返回空串。
// 修改、禁用、重启、删除都要先过这一关(规格第 4 节)。
func busyReason(p *Pair) string {
	if p == nil {
		return ""
	}
	info := p.Info()
	for _, port := range []PortInfo{info.A, info.B} {
		if port.State != StateInUse {
			continue
		}
		who := port.Process
		if who == "" {
			who = "未知程序"
			if port.ProcessNote != "" {
				who += "(" + port.ProcessNote + ")"
			}
		}
		return fmt.Sprintf("%s 正在被 %s(PID %d)使用,请先关闭该程序", port.Name, who, port.PID)
	}
	return ""
}

func (m *Manager) lookup(id string) (*entry, error) {
	e, ok := m.entries[id]
	if !ok {
		return nil, fmt.Errorf("没有找到串口对 %s", id)
	}
	return e, nil
}

// Delete 删除整对虚拟串口。任一端正在使用时拒绝执行。
func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	e, err := m.lookup(id)
	if err != nil {
		return err
	}
	if reason := busyReason(e.pair); reason != "" {
		return fmt.Errorf("无法删除:%s", reason)
	}
	if e.pair != nil {
		e.pair.Close()
	}
	delete(m.entries, id)
	for i, v := range m.order {
		if v == id {
			m.order = append(m.order[:i], m.order[i+1:]...)
			break
		}
	}
	return nil
}

// SetEnabled 启用或禁用整对串口。禁用会拆掉两个端口但保留编号配置。
func (m *Manager) SetEnabled(id string, on bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	e, err := m.lookup(id)
	if err != nil {
		return err
	}
	if e.enabled == on {
		return nil
	}
	if !on {
		if reason := busyReason(e.pair); reason != "" {
			return fmt.Errorf("无法禁用:%s", reason)
		}
		e.lastInfo = e.pair.Info()
		e.lastInfo.Enabled = false
		e.pair.Close()
		e.pair, e.enabled = nil, false
		return nil
	}

	p, err := newPair(e.id, e.comA, e.comB)
	if err != nil {
		return fmt.Errorf("启用失败(编号 %s/%s): %w", e.comA, e.comB, err)
	}
	e.pair, e.enabled = p, true
	return nil
}

// Restart 重启整对串口,保持原编号与配对关系。会清掉待收发数据。
func (m *Manager) Restart(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	e, err := m.lookup(id)
	if err != nil {
		return err
	}
	if !e.enabled {
		return fmt.Errorf("串口对 %s 当前处于禁用状态,请先启用", id)
	}
	if reason := busyReason(e.pair); reason != "" {
		return fmt.Errorf("无法重启:%s", reason)
	}

	e.pair.Close()
	p, err := newPair(e.id, e.comA, e.comB)
	if err != nil {
		e.pair, e.enabled = nil, false
		return fmt.Errorf("重启失败,串口对已停用(编号 %s/%s): %w", e.comA, e.comB, err)
	}
	e.pair = p
	return nil
}

// Rename 修改一端或两端的编号,保留配对关系。传空表示该端不变。
func (m *Manager) Rename(id, comA, comB string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	e, err := m.lookup(id)
	if err != nil {
		return err
	}
	if reason := busyReason(e.pair); reason != "" {
		return fmt.Errorf("无法修改编号:%s", reason)
	}

	newA, newB := e.comA, e.comB
	if comA != "" {
		newA = strings.ToUpper(comA)
	}
	if comB != "" {
		newB = strings.ToUpper(comB)
	}
	if newA == e.comA && newB == e.comB {
		return nil
	}
	if newA == newB {
		return fmt.Errorf("两端不能使用同一个编号 %s", newA)
	}

	used, err := UsedCOMNames()
	if err != nil {
		return err
	}
	for _, other := range m.entries {
		if other.id == id {
			continue
		}
		used[strings.ToUpper(other.comA)] = true
		used[strings.ToUpper(other.comB)] = true
	}
	// 自己当前占着的编号不算冲突。
	delete(used, e.comA)
	delete(used, e.comB)
	for _, name := range []string{newA, newB} {
		if comNumber(name) == 0 {
			return fmt.Errorf("%q 不是合法的 COM 名,应形如 COM10", name)
		}
		if used[name] {
			return fmt.Errorf("%s 已被占用,请换一个编号", name)
		}
	}

	oldA, oldB, wasEnabled := e.comA, e.comB, e.enabled
	if e.pair != nil {
		e.pair.Close()
		e.pair = nil
	}
	e.comA, e.comB = newA, newB
	if !wasEnabled {
		return nil
	}

	p, err := newPair(e.id, newA, newB)
	if err != nil {
		// 新编号起不来就退回原编号,不留下「改了一半」的状态。
		e.comA, e.comB = oldA, oldB
		e.enabled = false
		return fmt.Errorf("按新编号 %s/%s 重建失败,已退回 %s/%s 并停用: %w",
			newA, newB, oldA, oldB, err)
	}
	e.pair = p
	return nil
}

// PurgeBuffers 清空某对串口的待传数据。
func (m *Manager) PurgeBuffers(id string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, err := m.lookup(id)
	if err != nil {
		return 0, err
	}
	if e.pair == nil {
		return 0, fmt.Errorf("串口对 %s 处于禁用状态", id)
	}
	return e.pair.PurgeBuffers(), nil
}

// ResetStats 清空某对串口的统计,不影响缓存与通信。
func (m *Manager) ResetStats(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, err := m.lookup(id)
	if err != nil {
		return err
	}
	if e.pair == nil {
		return fmt.Errorf("串口对 %s 处于禁用状态", id)
	}
	e.pair.ResetStats()
	return nil
}

// List 返回当前全部串口对的快照,按创建顺序排列。
func (m *Manager) List() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()

	snap := Snapshot{UpdatedAt: time.Now(), Version: Version, Backend: Backend}
	for _, id := range m.order {
		e := m.entries[id]
		if e == nil {
			continue
		}
		if e.pair != nil {
			snap.Pairs = append(snap.Pairs, e.pair.Info())
			continue
		}
		snap.Pairs = append(snap.Pairs, disabledInfo(e))
	}
	return snap
}

// disabledInfo 合成禁用状态下的展示信息。
func disabledInfo(e *entry) PairInfo {
	info := e.lastInfo
	info.ID, info.Enabled = e.id, false
	info.A.Name, info.B.Name = e.comA, e.comB
	info.A.State, info.B.State = StateDisabled, StateDisabled
	info.A.Process, info.B.Process = "", ""
	info.A.PID, info.B.PID = 0, 0
	return info
}

// CloseAll 拆除全部串口对,退出时调用。
func (m *Manager) CloseAll() {
	m.mu.Lock()
	pairs := make([]*Pair, 0, len(m.entries))
	for _, e := range m.entries {
		if e.pair != nil {
			pairs = append(pairs, e.pair)
		}
	}
	m.entries = make(map[string]*entry)
	m.order = nil
	m.mu.Unlock()

	for _, p := range pairs {
		p.Close()
	}
}

// Diagnose 生成一份可直接复制给维护人员的诊断报告(规格 2.9)。
// 只检查和报告,不做任何修复动作。
func (m *Manager) Diagnose() string {
	snap := m.List()

	var b strings.Builder
	fmt.Fprintf(&b, "VirtualCOM 诊断报告\n")
	fmt.Fprintf(&b, "检查时间: %s\n", snap.UpdatedAt.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "软件版本: %s (实现路线: %s)\n", snap.Version, snap.Backend)
	fmt.Fprintf(&b, "串口对数: %d  虚拟端口数: %d\n\n", len(snap.Pairs), len(snap.Pairs)*2)

	if used, err := UsedCOMNames(); err != nil {
		fmt.Fprintf(&b, "系统 COM 占用: 读取失败 (%v)\n\n", err)
	} else {
		names := make([]string, 0, len(used))
		for n := range used {
			names = append(names, n)
		}
		sort.Slice(names, func(i, j int) bool { return comNumber(names[i]) < comNumber(names[j]) })
		fmt.Fprintf(&b, "系统已占用 COM(%d 个): %s\n\n", len(names), strings.Join(names, " "))
	}

	for _, p := range snap.Pairs {
		fmt.Fprintf(&b, "[%s] %s ⇄ %s  启用=%v\n", p.ID, p.A.Name, p.B.Name, p.Enabled)
		if p.Error != "" {
			fmt.Fprintf(&b, "  最近错误: %s\n", p.Error)
		}
		for _, port := range []PortInfo{p.A, p.B} {
			fmt.Fprintf(&b, "  %s 状态=%s", port.Name, port.State)
			if port.State == StateInUse {
				who := port.Process
				if who == "" {
					who = "无法获取"
				}
				fmt.Fprintf(&b, " 占用=%s PID=%d", who, port.PID)
			}
			if port.ProcessNote != "" {
				fmt.Fprintf(&b, " (%s)", port.ProcessNote)
			}
			fmt.Fprintf(&b, "\n    TX=%d RX=%d 读=%d 写=%d 错误=%d 缓存=%d/%d 已丢弃=%d\n",
				port.TX, port.RX, port.Reads, port.Writes, port.Errors,
				port.BufferUsed, port.BufferCapacity, port.BufferDropped)

			// 配对关系检查:COM 名是否还指向本进程建的管道。
			if p.Enabled {
				if target, err := queryDosDevice(port.Name); err != nil {
					fmt.Fprintf(&b, "    配对检查: 符号链接查询失败 (%v)\n", err)
				} else if !strings.Contains(target, "VirtualCOM-") {
					fmt.Fprintf(&b, "    配对检查: 异常,%s 当前指向 %s\n", port.Name, target)
				} else {
					fmt.Fprintf(&b, "    配对检查: 正常,指向 %s\n", target)
				}
			}
		}
		b.WriteString("\n")
	}

	b.WriteString(compatibilityNotice)
	return b.String()
}

// compatibilityNotice 是用户态路线的已知能力边界。
// 诊断报告里必须带上,免得把「串口软件打不开」当成故障来排查。
const compatibilityNotice = `已知能力边界(用户态实现,不安装驱动、不需要签名):
  可用: 字节流双向收发、跨进程打开、占用程序与 PID、缓存与统计、诊断
  不可用: GetCommState/SetCommState 等串口参数 API、RTS/CTS 等控制信号、
          WaitCommEvent 事件、PurgeComm/ClearCommError,设备管理器也不会显示。
  影响: 凡是用 .NET SerialPort、pyserial、go.bug.st/serial 打开端口的软件,
        会在打开阶段直接失败。只有把端口当字节流读写的程序能用。
  要消除以上限制必须安装内核驱动,而驱动必须签名,与当前选型冲突。
`
