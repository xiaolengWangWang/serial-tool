//go:build windows

package vcom

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// pipeNamePrefix 是本项目所有管道的统一前缀,清理时据此认领自己的残留。
const pipeNamePrefix = "VirtualCOM-"

// StaleLink 描述一条残留的 COM 符号链接。
type StaleLink struct {
	COM    string // COM12
	Target string // \Device\NamedPipe\VirtualCOM-1234-1
}

// FindStaleLinks 找出本机残留的 VirtualCOM 符号链接。
//
// 进程崩溃或被强杀时,拆除逻辑来不及执行,COM 名会留在设备映射里而管道早已不在,
// 表现为「这个编号被占着,但任何程序打开它都报找不到」。这里用列举命名管道目录的
// 办法判断管道是否还活着——不去打开它,免得把正在运行的实例的唯一连接名额吃掉。
func FindStaleLinks() ([]StaleLink, error) {
	names, err := listDosDevices()
	if err != nil {
		return nil, fmt.Errorf("枚举系统设备名失败: %w", err)
	}
	live, err := livePipeNames()
	if err != nil {
		return nil, err
	}

	var stale []StaleLink
	for _, name := range names {
		if comNumber(name) == 0 {
			continue
		}
		target, err := queryDosDevice(name)
		if err != nil {
			continue
		}
		pipe, ok := strings.CutPrefix(target, `\Device\NamedPipe\`)
		if !ok || !strings.HasPrefix(pipe, pipeNamePrefix) {
			continue // 不是我们建的,不碰
		}
		if live[strings.ToLower(pipe)] {
			continue // 管道还在,说明有实例正在用
		}
		stale = append(stale, StaleLink{COM: strings.ToUpper(name), Target: target})
	}
	sort.Slice(stale, func(i, j int) bool {
		return comNumber(stale[i].COM) < comNumber(stale[j].COM)
	})
	return stale, nil
}

// livePipeNames 列出当前存在的命名管道名(小写)。
func livePipeNames() (map[string]bool, error) {
	entries, err := os.ReadDir(`\\.\pipe\`)
	if err != nil {
		return nil, fmt.Errorf("列举命名管道失败: %w", err)
	}
	live := make(map[string]bool, len(entries))
	for _, e := range entries {
		live[strings.ToLower(e.Name())] = true
	}
	return live, nil
}

// RemoveStaleLinks 删除残留链接,返回已清理的编号。
// 只删目标指向本项目管道、且管道确实已不存在的链接。
func RemoveStaleLinks() ([]string, error) {
	stale, err := FindStaleLinks()
	if err != nil {
		return nil, err
	}
	var removed []string
	var failed []string
	for _, s := range stale {
		if err := removeDosDevice(s.COM, s.Target); err != nil {
			failed = append(failed, fmt.Sprintf("%s(%v)", s.COM, err))
			continue
		}
		removed = append(removed, s.COM)
	}
	if len(failed) > 0 {
		return removed, fmt.Errorf("以下编号清理失败: %s", strings.Join(failed, " "))
	}
	return removed, nil
}
