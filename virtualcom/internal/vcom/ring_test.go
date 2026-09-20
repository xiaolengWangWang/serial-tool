package vcom

import (
	"bytes"
	"math/rand"
	"sync"
	"testing"
	"time"
)

func TestRingRoundTrip(t *testing.T) {
	r := NewRing(16)
	if _, err := r.Write([]byte("hello")); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	buf := make([]byte, 5)
	n, err := r.Read(buf)
	if err != nil || n != 5 || string(buf) != "hello" {
		t.Fatalf("读出 %q n=%d err=%v,期望 hello", buf[:n], n, err)
	}
}

// 环形缓冲必须能正确处理跨越末尾回绕的读写。
func TestRingWrapAround(t *testing.T) {
	r := NewRing(8)
	r.Write([]byte("abcdef"))
	buf := make([]byte, 4)
	r.Read(buf) // 读走 abcd,start 移到 4
	r.Write([]byte("ghijk"))

	got := make([]byte, 7)
	if err := readFull(r, got); err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if string(got) != "efghijk" {
		t.Fatalf("回绕后读到 %q,期望 efghijk", got)
	}
}

// 写满时必须阻塞等待,不能丢数据(规格 2.7)。
func TestRingBlocksWhenFull(t *testing.T) {
	r := NewRing(4)
	done := make(chan int, 1)
	go func() {
		n, _ := r.Write([]byte("12345678"))
		done <- n
	}()

	select {
	case n := <-done:
		t.Fatalf("缓冲区满时写入不应完成,却返回了 %d", n)
	case <-time.After(50 * time.Millisecond):
	}

	got := make([]byte, 8)
	if err := readFull(r, got); err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if n := <-done; n != 8 {
		t.Fatalf("写入返回 %d,期望 8", n)
	}
	if string(got) != "12345678" {
		t.Fatalf("读到 %q,期望 12345678", got)
	}
}

// 关闭后要能把残留数据读完,读空才报错——这样才分得清「部分完成」和「未完成」。
func TestRingCloseDrainsThenErrors(t *testing.T) {
	r := NewRing(8)
	r.Write([]byte("abc"))
	r.Close()

	buf := make([]byte, 8)
	n, err := r.Read(buf)
	if err != nil || n != 3 {
		t.Fatalf("关闭后首次读取 n=%d err=%v,期望读出 3 字节", n, err)
	}
	if _, err := r.Read(buf); err != ErrClosed {
		t.Fatalf("读空后期望 ErrClosed,得到 %v", err)
	}
}

func TestRingCloseUnblocksWriter(t *testing.T) {
	r := NewRing(2)
	done := make(chan error, 1)
	go func() {
		_, err := r.Write([]byte("abcdefgh"))
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	r.Close()

	select {
	case err := <-done:
		if err != ErrClosed {
			t.Fatalf("期望 ErrClosed,得到 %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("关闭后写入方没有被唤醒")
	}
}

func TestRingPurgeCountsDropped(t *testing.T) {
	r := NewRing(16)
	r.Write([]byte("0123456789"))
	if n := r.Purge(); n != 10 {
		t.Fatalf("Purge 返回 %d,期望 10", n)
	}
	if st := r.Stats(); st.Used != 0 || st.Dropped != 10 {
		t.Fatalf("Purge 后 Used=%d Dropped=%d,期望 0/10", st.Used, st.Dropped)
	}
}

// 大数据连续传输不能截断、乱序或重复(规格 F04 的缓冲层部分)。
func TestRingStreamIntegrity(t *testing.T) {
	const total = 512 * 1024
	src := make([]byte, total)
	rand.New(rand.NewSource(1)).Read(src)

	r := NewRing(4096)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		r.Write(src)
		r.Close()
	}()

	var got bytes.Buffer
	buf := make([]byte, 777) // 故意用不整除的块大小
	for {
		n, err := r.Read(buf)
		got.Write(buf[:n])
		if err != nil {
			break
		}
	}
	wg.Wait()

	if got.Len() != total {
		t.Fatalf("收到 %d 字节,期望 %d", got.Len(), total)
	}
	if !bytes.Equal(got.Bytes(), src) {
		t.Fatal("数据内容与写入不一致")
	}
}

func TestRingStatsTracksCounters(t *testing.T) {
	r := NewRing(64)
	r.Write([]byte("abcdef"))
	buf := make([]byte, 2)
	r.Read(buf)

	st := r.Stats()
	if st.Written != 6 || st.Read != 2 || st.Used != 4 || st.Capacity != 64 {
		t.Fatalf("统计异常: %+v", st)
	}
}

func readFull(r *Ring, p []byte) error {
	got := 0
	for got < len(p) {
		n, err := r.Read(p[got:])
		got += n
		if err != nil {
			return err
		}
	}
	return nil
}
