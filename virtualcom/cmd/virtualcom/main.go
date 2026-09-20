//go:build windows

// VirtualCOM —— 自研 Windows 虚拟串口。
//
// 用户态实现:只用 Win32 API,不安装驱动、不需要任何签名、不依赖任何第三方组件。
// 能力边界见 README,诊断报告里也会带上。
package main

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"time"

	"virtualcom/internal/vcom"
)

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}

	var err error
	switch args[0] {
	case "run":
		err = cmdRun(args[1:])
	case "ports":
		err = cmdPorts()
	case "selftest":
		err = cmdSelfTest()
	case "cleanup":
		err = cmdCleanup()
	case "version":
		fmt.Printf("VirtualCOM %s (实现路线: %s)\n", vcom.Version, vcom.Backend)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "未知命令: %s\n\n", args[0])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`VirtualCOM ` + vcom.Version + ` —— 自研 Windows 虚拟串口

用法:
  virtualcom run [COM10:COM11 ...]   创建串口对并常驻,Ctrl+C 退出并清理
                                     不带参数则自动挑两个空闲编号
  virtualcom ports                   列出系统当前已占用的 COM 编号
  virtualcom selftest                自检:建一对口并跑通双向、全双工、重开验证
  virtualcom cleanup                 清理进程异常退出后残留的 COM 编号
  virtualcom version                 显示版本

说明:
  进程退出时会拆掉虚拟端口,通信随之中断。
`)
}

func cmdPorts() error {
	used, err := vcom.UsedCOMNames()
	if err != nil {
		return err
	}
	names := make([]string, 0, len(used))
	for n := range used {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool { return len(names[i]) < len(names[j]) || names[i] < names[j] })
	fmt.Printf("系统已占用 %d 个 COM 编号:\n%s\n", len(names), strings.Join(names, " "))
	return nil
}

// cmdCleanup 清掉进程崩溃或被强杀后残留的 COM 编号。
func cmdCleanup() error {
	stale, err := vcom.FindStaleLinks()
	if err != nil {
		return err
	}
	if len(stale) == 0 {
		fmt.Println("没有发现残留编号。")
		return nil
	}
	fmt.Printf("发现 %d 个残留编号(管道已不存在,编号还占着):\n", len(stale))
	for _, s := range stale {
		fmt.Printf("  %s -> %s\n", s.COM, s.Target)
	}
	removed, err := vcom.RemoveStaleLinks()
	if len(removed) > 0 {
		fmt.Printf("已清理: %s\n", strings.Join(removed, " "))
	}
	return err
}

func cmdRun(specs []string) error {
	// 先清掉自家残留编号:上次被强杀时 COM 名没来得及释放,
	// 否则这次自动选号会跳过那些其实已经死掉的编号。
	if removed, err := vcom.RemoveStaleLinks(); err != nil {
		fmt.Fprintln(os.Stderr, "清理残留编号时出错:", err)
	} else if len(removed) > 0 {
		fmt.Printf("已清理上次遗留的编号: %s\n", strings.Join(removed, " "))
	}

	m := vcom.NewManager()
	defer m.CloseAll()

	if len(specs) == 0 {
		specs = []string{":"} // 一对自动编号
	}
	for _, spec := range specs {
		a, b, err := parsePair(spec)
		if err != nil {
			return err
		}
		info, err := m.Create(a, b)
		if err != nil {
			return err
		}
		fmt.Printf("已创建 %s: %s ⇄ %s\n", info.ID, info.A.Name, info.B.Name)
	}

	fmt.Println("\n串口对已就绪。用串口软件打开上面两个编号即可互通。")
	fmt.Print("按 Ctrl+C 退出并清理。\n\n")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-sig:
			fmt.Println("\n正在拆除虚拟串口...")
			return nil
		case <-ticker.C:
			printSnapshot(m.List())
		}
	}
}

// parsePair 解析 "COM10:COM11";两边留空表示自动分配。
func parsePair(spec string) (string, string, error) {
	parts := strings.Split(spec, ":")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("串口对格式应为 COM10:COM11,收到 %q", spec)
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), nil
}

func printSnapshot(snap vcom.Snapshot) {
	for _, p := range snap.Pairs {
		fmt.Printf("[%s] %s %s TX=%s | %s %s TX=%s",
			p.ID,
			p.A.Name, p.A.State, humanBytes(p.A.TX),
			p.B.Name, p.B.State, humanBytes(p.B.TX))
		if p.A.Process != "" || p.B.Process != "" {
			fmt.Printf("  占用: %s / %s", orDash(p.A.Process), orDash(p.B.Process))
		}
		if p.Error != "" {
			fmt.Printf("  异常: %s", p.Error)
		}
		fmt.Println()
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func humanBytes(n uint64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// cmdSelfTest 在真机上验证核心功能,失败即非零退出。
func cmdSelfTest() error {
	m := vcom.NewManager()
	defer m.CloseAll()

	info, err := m.Create("", "")
	if err != nil {
		return fmt.Errorf("创建串口对失败: %w", err)
	}
	comA, comB := info.A.Name, info.B.Name
	fmt.Printf("自检使用 %s ⇄ %s\n\n", comA, comB)

	a, err := vcom.OpenPort(comA)
	if err != nil {
		return err
	}
	defer a.Close()
	b, err := vcom.OpenPort(comB)
	if err != nil {
		return err
	}
	defer b.Close()

	steps := []struct {
		name string
		run  func() error
	}{
		{"单向 A→B 1MB", func() error { return transfer(a, b, 1<<20, 1) }},
		{"单向 B→A 1MB", func() error { return transfer(b, a, 1<<20, 2) }},
		{"全双工同时收发 各 512KB", func() error { return fullDuplex(a, b, 512<<10) }},
		{"任意字节值穿透(0x00~0xFF)", func() error { return allByteValues(a, b) }},
	}
	for _, s := range steps {
		start := time.Now()
		if err := s.run(); err != nil {
			fmt.Printf("  [失败] %s: %v\n", s.name, err)
			return fmt.Errorf("自检未通过")
		}
		fmt.Printf("  [通过] %s  用时 %v\n", s.name, time.Since(start).Round(time.Millisecond))
	}

	// 重复打开关闭:关掉 A 再重开,应当还能通(规格 F03)。
	a.Close()
	reopened, err := reopen(comA)
	if err != nil {
		fmt.Printf("  [失败] 关闭后重新打开 %s: %v\n", comA, err)
		return fmt.Errorf("自检未通过")
	}
	defer reopened.Close()
	if err := transfer(reopened, b, 64<<10, 3); err != nil {
		fmt.Printf("  [失败] 重开后再通信: %v\n", err)
		return fmt.Errorf("自检未通过")
	}
	fmt.Printf("  [通过] 关闭后重新打开 %s 并继续通信\n", comA)

	fmt.Print("\n自检全部通过。\n\n")
	fmt.Println(m.Diagnose())
	return nil
}

// reopen 重新打开端口。服务端复位需要一点时间,这里给几次重试。
func reopen(com string) (*vcom.PortClient, error) {
	var last error
	for i := 0; i < 50; i++ {
		c, err := vcom.OpenPort(com)
		if err == nil {
			return c, nil
		}
		last = err
		time.Sleep(20 * time.Millisecond)
	}
	return nil, last
}

// transfer 从 src 写入定量随机数据,在 dst 读回并逐字节校验。
func transfer(src, dst *vcom.PortClient, size int, seed int64) error {
	payload := make([]byte, size)
	rand.New(rand.NewSource(seed)).Read(payload)

	got := make([]byte, size)
	errc := make(chan error, 1)
	go func() { errc <- dst.ReadFull(got) }()

	if _, err := src.Write(payload); err != nil {
		return fmt.Errorf("写入失败: %w", err)
	}
	if err := <-errc; err != nil {
		return err
	}
	if !bytes.Equal(got, payload) {
		return fmt.Errorf("收到的数据与发送的不一致")
	}
	return nil
}

// fullDuplex 两个方向同时传输,验证互不干扰。
func fullDuplex(a, b *vcom.PortClient, size int) error {
	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)
	go func() { defer wg.Done(); errs[0] = transfer(a, b, size, 11) }()
	go func() { defer wg.Done(); errs[1] = transfer(b, a, size, 22) }()
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// allByteValues 验证 0x00~0xFF 全部字节值都能原样穿透,不被任何转义改写。
func allByteValues(src, dst *vcom.PortClient) error {
	payload := make([]byte, 256)
	for i := range payload {
		payload[i] = byte(i)
	}
	got := make([]byte, len(payload))
	errc := make(chan error, 1)
	go func() { errc <- dst.ReadFull(got) }()

	if _, err := src.Write(payload); err != nil {
		return err
	}
	if err := <-errc; err != nil {
		return err
	}
	if !bytes.Equal(got, payload) {
		return fmt.Errorf("字节值被改写")
	}
	return nil
}
