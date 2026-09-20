//go:build windows

package vcom

import (
	"bytes"
	"errors"
	"math/rand"
	"sync"
	"testing"
)

// openPair 建一对虚拟串口并把两端都打开,返回两个客户端。
func openPair(t *testing.T) (*Manager, *PortClient, *PortClient) {
	t.Helper()
	m := NewManager()
	t.Cleanup(m.CloseAll)

	info, err := m.Create("", "")
	if err != nil {
		t.Fatalf("创建串口对失败: %v", err)
	}
	t.Logf("使用 %s ⇄ %s", info.A.Name, info.B.Name)

	a, err := OpenPort(info.A.Name)
	if err != nil {
		t.Fatalf("打开 %s 失败: %v", info.A.Name, err)
	}
	t.Cleanup(func() { a.Close() })

	b, err := OpenPort(info.B.Name)
	if err != nil {
		t.Fatalf("打开 %s 失败: %v", info.B.Name, err)
	}
	t.Cleanup(func() { b.Close() })
	return m, a, b
}

func TestPairSmallTransfer(t *testing.T) {
	_, a, b := openPair(t)

	payload := []byte("PING-FROM-A")
	got := make([]byte, len(payload))
	errc := make(chan error, 1)
	go func() { errc <- b.ReadFull(got) }()

	if _, err := a.Write(payload); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	if err := <-errc; err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("收到 %q,期望 %q", got, payload)
	}
}

// 回归测试:同一个句柄上同时挂起收和发。
//
// 早期版本用同步(非 overlapped)句柄,内核会把同句柄的 I/O 串行化,
// 结果挂起的读把写堵死,整对串口直接卡住。串口本来就是全双工,
// 这个用例必须一直保持通过。
func TestPairFullDuplexNoDeadlock(t *testing.T) {
	_, a, b := openPair(t)

	const size = 256 * 1024
	var wg sync.WaitGroup
	errs := make([]error, 2)

	wg.Add(2)
	go func() { defer wg.Done(); errs[0] = exchange(a, b, size, 1) }()
	go func() { defer wg.Done(); errs[1] = exchange(b, a, size, 2) }()
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("方向 %d 传输失败: %v", i, err)
		}
	}
}

// 大数据连续传输:任意字节值,校验长度与内容(规格 F04)。
func TestPairLargeTransferIntegrity(t *testing.T) {
	_, a, b := openPair(t)
	if err := exchange(a, b, 1<<20, 7); err != nil {
		t.Fatalf("A→B 1MB 传输失败: %v", err)
	}
	if err := exchange(b, a, 1<<20, 8); err != nil {
		t.Fatalf("B→A 1MB 传输失败: %v", err)
	}
}

// 0x00~0xFF 全字节值必须原样穿透,不被任何转义改写。
func TestPairAllByteValues(t *testing.T) {
	_, a, b := openPair(t)

	payload := make([]byte, 256)
	for i := range payload {
		payload[i] = byte(i)
	}
	got := make([]byte, len(payload))
	errc := make(chan error, 1)
	go func() { errc <- b.ReadFull(got) }()

	if _, err := a.Write(payload); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	if err := <-errc; err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("字节值被改写")
	}
}

// 占用信息必须如实反映:打开后是「使用中」,并带上 PID。
func TestPairReportsOccupancy(t *testing.T) {
	m, _, _ := openPair(t)

	pairs := m.List().Pairs
	if len(pairs) != 1 {
		t.Fatalf("期望 1 对串口,得到 %d", len(pairs))
	}
	for _, port := range []PortInfo{pairs[0].A, pairs[0].B} {
		if port.State != StateInUse {
			t.Errorf("%s 状态是 %q,期望 %q", port.Name, port.State, StateInUse)
		}
		if port.PID == 0 {
			t.Errorf("%s 没有报告占用 PID", port.Name)
		}
	}
}

// 正在使用的串口对不允许删除(规格第 4 节)。
func TestPairRefusesDeleteWhileInUse(t *testing.T) {
	m, _, _ := openPair(t)

	id := m.List().Pairs[0].ID
	if err := m.Delete(id); err == nil {
		t.Fatal("端口正在使用时删除竟然成功了,应当被拒绝")
	} else {
		t.Logf("已按预期拒绝: %v", err)
	}
}

// exchange 从 src 发定量随机数据,在 dst 收齐并逐字节比对。
func exchange(src, dst *PortClient, size int, seed int64) error {
	payload := make([]byte, size)
	rand.New(rand.NewSource(seed)).Read(payload)

	got := make([]byte, size)
	errc := make(chan error, 1)
	go func() { errc <- dst.ReadFull(got) }()

	if _, err := src.Write(payload); err != nil {
		return err
	}
	if err := <-errc; err != nil {
		return err
	}
	if !bytes.Equal(got, payload) {
		return errMismatch
	}
	return nil
}

var errMismatch = errors.New("收到的数据与发送的不一致")

// 多组串口同时传输不同内容,数据只能进各自的配对端口(规格 F05)。
func TestMultiplePairsIsolated(t *testing.T) {
	m := NewManager()
	t.Cleanup(m.CloseAll)

	const pairs = 3
	type link struct{ a, b *PortClient }
	links := make([]link, 0, pairs)

	for i := 0; i < pairs; i++ {
		info, err := m.Create("", "")
		if err != nil {
			t.Fatalf("创建第 %d 对失败: %v", i+1, err)
		}
		a, err := OpenPort(info.A.Name)
		if err != nil {
			t.Fatalf("打开 %s 失败: %v", info.A.Name, err)
		}
		t.Cleanup(func() { a.Close() })
		b, err := OpenPort(info.B.Name)
		if err != nil {
			t.Fatalf("打开 %s 失败: %v", info.B.Name, err)
		}
		t.Cleanup(func() { b.Close() })
		t.Logf("第 %d 对: %s ⇄ %s", i+1, info.A.Name, info.B.Name)
		links = append(links, link{a, b})
	}

	// 三组同时发不同内容
	var wg sync.WaitGroup
	errs := make([]error, pairs)
	for i, l := range links {
		wg.Add(1)
		go func(i int, l link) {
			defer wg.Done()
			errs[i] = exchange(l.a, l.b, 64*1024, int64(100+i))
		}(i, l)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("第 %d 对传输失败(可能串组): %v", i+1, err)
		}
	}
}
