package vcom

import "time"

// 端口状态取值,与规格 2.2 的四态一致。
const (
	StateIdle     = "空闲"
	StateInUse    = "使用中"
	StateDisabled = "禁用"
	StateError    = "异常"
)

// PortInfo 是一个虚拟端口的对外快照。
//
// 关于「取不到」的约定(规格 2.2 / 第 4 节):拿不到的信息一律留空并在 Note
// 里写明原因,绝不用 0 或「空闲」冒充未知。
type PortInfo struct {
	Name        string `json:"name"`         // COM10
	State       string `json:"state"`        // 空闲 / 使用中 / 禁用 / 异常
	Process     string `json:"process"`      // 占用程序名,取不到时为空
	ProcessNote string `json:"process_note"` // 取不到程序名的原因
	PID         uint32 `json:"pid"`          // 占用进程 PID,0 表示无或取不到

	// 统计口径(规格 2.8):
	// TX = 该端已成功提交发送的字节数,不代表对端软件已经读走;
	// RX = 已成功送达该端的字节数。
	TX     uint64 `json:"tx"`
	RX     uint64 `json:"rx"`
	Reads  uint64 `json:"reads"`
	Writes uint64 `json:"writes"`
	Errors uint64 `json:"errors"`

	// 待送往该端的缓存占用情况(规格 2.7)。
	BufferUsed     int    `json:"buffer_used"`
	BufferCapacity int    `json:"buffer_capacity"`
	BufferDropped  uint64 `json:"buffer_dropped"`
}

// PairInfo 是一对虚拟串口的对外快照。
type PairInfo struct {
	ID        string    `json:"id"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	A         PortInfo  `json:"a"`
	B         PortInfo  `json:"b"`
	Error     string    `json:"error,omitempty"`
}

// Snapshot 是一次整体状态快照。
type Snapshot struct {
	UpdatedAt time.Time  `json:"updated_at"`
	Version   string     `json:"version"`
	Backend   string     `json:"backend"`
	Pairs     []PairInfo `json:"pairs"`
}
