package main

/*
#cgo darwin LDFLAGS: -framework Cocoa
#include <stdlib.h>
#include "app.h"
*/
import "C"

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"serial-tool/internal/wincore"
)

// aiAnalysisGuide 是随工具发布的报文分析指南，作为 DeepSeek 的系统提示，
// 让分析行为由这份可维护的 Markdown 驱动。
//
//go:embed ai_analysis_guide.md
var aiAnalysisGuide string

var engine *wincore.Engine
var hexMode atomic.Bool

var loopCancel chan struct{}
var loopMu sync.Mutex

func main() {
	runtime.LockOSThread()
	configDir, err := os.UserConfigDir()
	if err != nil {
		return
	}
	engine, err = wincore.New(filepath.Join(configDir, "CommBox", "data"), onData, onLog)
	if err != nil {
		return
	}
	engine.SetOnClosed(onClosed)
	defer engine.Close()
	C.RunApp()
}

func timestamp() string { return time.Now().Format("15:04:05.000") }

func toASCII(data []byte) string {
	b := make([]byte, len(data))
	for i, c := range data {
		if c >= 32 && c < 127 {
			b[i] = c
		} else {
			b[i] = '.'
		}
	}
	return string(b)
}

type packetDisplay struct {
	ts, dir, hex, ascii string
	kind                string
	len                 int
}

func packetDisplayFields(ts, dir string, data []byte) packetDisplay {
	kind := "ASCII"
	for _, b := range data {
		if b < 0x20 || b > 0x7e {
			kind = "HEX"
			break
		}
	}
	return packetDisplay{ts: ts, dir: dir, hex: fmt.Sprintf("% X", data), ascii: toASCII(data), kind: kind, len: len(data)}
}

func addPacket(dir string, data []byte) {
	p := packetDisplayFields(timestamp(), dir, data)
	ts := C.CString(p.ts)
	cdir := C.CString(p.dir)
	hex := C.CString(p.hex)
	ascii := C.CString(p.ascii)
	kind := C.CString(p.kind)
	C.UIAddPacket(ts, cdir, hex, ascii, kind, C.int(p.len))
	C.free(unsafe.Pointer(ts))
	C.free(unsafe.Pointer(cdir))
	C.free(unsafe.Pointer(hex))
	C.free(unsafe.Pointer(ascii))
	C.free(unsafe.Pointer(kind))
}

func onData(_ string, data []byte) {
	addPacket("RX", data)
	monLine := C.CString(fmt.Sprintf("[%s 接收] % X\n", timestamp(), data))
	C.UIMonitorAppend(monLine)
	C.free(unsafe.Pointer(monLine))
}

// logSent 把成功发送的数据加入报文表格。
func logSent(input string, asHex bool, eol string) {
	data, err := wincore.ParseData(input, asHex, eol)
	if err != nil {
		return
	}
	addPacket("TX", data)
}

func onClosed() { C.UIConnectionClosed() }

func onLog(text string) {
	s := C.CString("\n[" + text + "]\n")
	C.UIAppendLog(s)
	C.free(unsafe.Pointer(s))
}

//export GoRecentSessions
func GoRecentSessions() *C.char {
	sessions, err := engine.RecentSessions(5)
	if err != nil {
		return C.CString("")
	}
	lines := make([]string, 0, len(sessions))
	for _, s := range sessions {
		// 字段用 \x1f 分隔,记录用 \n 分隔
		lines = append(lines, strings.Join([]string{s.Mode, s.Endpoint, s.Parameters, s.StartedAt}, "\x1f"))
	}
	return C.CString(strings.Join(lines, "\n"))
}

//export GoLocalIP
func GoLocalIP() *C.char {
	return C.CString(wincore.LocalIP())
}

//export GoLocalIPs
func GoLocalIPs() *C.char {
	return C.CString(strings.Join(wincore.LocalIPs(), "\n"))
}

//export GoDatabaseInfo
func GoDatabaseInfo() *C.char {
	return C.CString(engine.DataDir())
}

//export GoListAnalysisDatabases
func GoListAnalysisDatabases() *C.char {
	result := struct {
		Files []string `json:"files"`
		Error string   `json:"error"`
	}{Files: []string{}}
	if engine == nil {
		result.Error = "数据库引擎尚未初始化"
	} else {
		files, err := wincore.ListAnalysisDatabases(engine.DataDir())
		if err != nil {
			result.Error = err.Error()
		} else {
			result.Files = files
		}
	}
	data, _ := json.Marshal(result)
	return C.CString(string(data))
}

//export GoAnalyzeDatabases
func GoAnalyzeDatabases(filenamesJSON, start, end, direction *C.char, limit C.int) *C.char {
	if engine == nil {
		return C.CString("错误:数据库引擎尚未初始化")
	}
	var filenames []string
	if err := json.Unmarshal([]byte(C.GoString(filenamesJSON)), &filenames); err != nil {
		return C.CString("错误:数据库文件列表无效")
	}
	report, err := wincore.AnalyzeDatabases(engine.DataDir(), filenames, C.GoString(start), C.GoString(end), C.GoString(direction), int(limit))
	if err != nil {
		return C.CString("错误:" + err.Error())
	}
	return C.CString(report)
}

//export GoVersion
func GoVersion() *C.char {
	return C.CString(wincore.Version)
}

//export GoGetAISetting
func GoGetAISetting(key *C.char) *C.char { return C.CString(engine.GetSetting(C.GoString(key))) }

//export GoSetAISetting
func GoSetAISetting(key, value *C.char) *C.char {
	if err := engine.SetSetting(C.GoString(key), C.GoString(value)); err != nil {
		return C.CString(err.Error())
	}
	return C.CString("")
}

// deepseekChat 用已保存的 DeepSeek 配置发起一次对话，返回助手回复；出错时返回中文错误串。
// 由 GoAIAnalyze（实时报文）与 GoAIAnalyzeReport（数据库报告）共用。
func deepseekChat(userPrompt string) string {
	if engine == nil || engine.GetSetting("deepseek.enabled") != "true" {
		return "AI 未启用，请先在 AI 增强分析设置中启用"
	}
	key := strings.TrimSpace(engine.GetSetting("deepseek.api_key"))
	if key == "" {
		return "AI Key 为空，请先配置 API Key"
	}
	// API Key 会放入 Authorization 头，含空格或控制字符会被 net/http 拒绝并抛出晦涩错误；
	// 这里提前给出清晰提示，通常是粘贴时带进了换行或多余内容。
	for _, r := range key {
		if r < 0x20 || r == 0x7f || r == ' ' {
			return "AI Key 含非法字符（空格、换行或控制字符），请在 AI 设置中重新粘贴 Key"
		}
	}
	base := strings.TrimRight(engine.GetSetting("deepseek.base_url"), "/")
	if base == "" {
		base = "https://api.deepseek.com"
	}
	parsed, err := neturl.Parse(base)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "AI Base URL 无效，请使用 http:// 或 https:// 地址"
	}
	if !strings.HasSuffix(base, "/chat/completions") {
		base += "/chat/completions"
	}
	model := engine.GetSetting("deepseek.model")
	if model == "" {
		model = "deepseek-chat"
	}
	system := strings.TrimSpace(aiAnalysisGuide)
	if system == "" {
		system = "你是工业通信现场诊断助手。结论仅作排查建议，优先建议查阅设备协议文档。"
	}
	body, _ := json.Marshal(map[string]any{"model": model, "temperature": 0.1, "messages": []map[string]string{{"role": "system", "content": system}, {"role": "user", "content": userPrompt}}})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base, bytes.NewReader(body))
	if err != nil {
		return "AI 请求失败：" + err.Error()
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "AI 请求失败：" + err.Error()
	}
	defer resp.Body.Close()
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return "AI 响应解析失败"
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := result.Error.Message
		if message == "" {
			message = resp.Status
		}
		return "AI 请求失败：" + message
	}
	if len(result.Choices) == 0 || result.Choices[0].Message.Content == "" {
		return "AI 未返回分析结果"
	}
	return result.Choices[0].Message.Content
}

//export GoAIAnalyze
func GoAIAnalyze(transport, hex *C.char) *C.char {
	input := strings.TrimSpace(C.GoString(hex))
	if len(input) == 0 {
		return C.CString("没有可分析的报文")
	}
	if len(input) > 16*1024 {
		return C.CString("分析数据超过 16KB，请先筛选或减少报文")
	}
	prompt := fmt.Sprintf("请分析以下 %s 通信报文。只根据给定数据说明协议、异常、风险和现场排查建议；不确定时明确说明，不要臆测串口参数。报文为 HEX：\n%s", C.GoString(transport), input)
	return C.CString(deepseekChat(prompt))
}

//export GoAIAnalyzeReport
func GoAIAnalyzeReport(report *C.char) *C.char {
	text := strings.TrimSpace(C.GoString(report))
	if len(text) == 0 {
		return C.CString("没有可分析的数据库数据")
	}
	const maxReport = 32 * 1024
	if len(text) > maxReport {
		text = text[:maxReport] + "\n……（报告过长，已截断）"
	}
	prompt := fmt.Sprintf("以下是本地 SQLite 采集数据的分析报告，含统计与逐条 HEX 报文解析。请据此做协议识别、异常定位、风险评估与现场排查建议；只依据报告内容，不确定时明确说明，不臆测未给出的参数。\n\n%s", text)
	return C.CString(deepseekChat(prompt))
}

//export GoAnalyzePacket
func GoAnalyzePacket(transport, hex *C.char) *C.char {
	return C.CString(wincore.AnalyzeTransportPacket(C.GoString(transport), C.GoString(hex)))
}

//export GoStats
func GoStats() *C.char {
	st := engine.Stats()
	elapsed := "—"
	if (st.State == wincore.StateConnected || st.State == wincore.StateReconnecting) && st.StartedAt.UnixNano() > 0 {
		elapsed = wincore.FormatDuration(time.Since(st.StartedAt))
	}
	data, _ := json.Marshal(map[string]any{
		"state": st.State, "mode": st.Mode, "listening": st.Listening, "datagram": st.Datagram,
		"endpoint": st.Endpoint, "peers": st.Peers, "peer_count": st.PeerCount,
		"rx":      fmt.Sprintf("%d 条 · %s", st.RXCount, wincore.FormatBytes(st.RXBytes)),
		"tx":      fmt.Sprintf("%d 条 · %s", st.TXCount, wincore.FormatBytes(st.TXBytes)),
		"elapsed": elapsed, "reconnects": st.Reconnects, "errors": st.Errors,
	})
	return C.CString(string(data))
}

//export GoFavoriteNames
func GoFavoriteNames() *C.char {
	return C.CString(strings.Join(engine.FavoriteNames(), "\n"))
}

//export GoSaveFavorite
func GoSaveFavorite(name, value *C.char) *C.char {
	if err := engine.SaveFavorite(C.GoString(name), C.GoString(value)); err != nil {
		return C.CString("错误:" + err.Error())
	}
	return C.CString("")
}

//export GoDeleteFavorite
func GoDeleteFavorite(name *C.char) {
	_ = engine.DeleteFavorite(C.GoString(name))
}

//export GoFavorite
func GoFavorite(name *C.char) *C.char {
	return C.CString(engine.Favorite(C.GoString(name)))
}

//export GoRecentSends
func GoRecentSends() *C.char {
	return C.CString(strings.Join(engine.RecentSends(), "\n"))
}

//export GoChecksum
func GoChecksum(kind, input *C.char) *C.char {
	return C.CString(wincore.ParseToolbox(C.GoString(kind), C.GoString(input)))
}

//export GoListPorts
func GoListPorts() *C.char {
	ports, err := wincore.ListPorts()
	if err != nil {
		return C.CString("错误: " + err.Error())
	}
	return C.CString(strings.Join(ports, "\n"))
}

//export GoConnect
func GoConnect(name *C.char, baud, dataBits, stopBits C.int, parity *C.char, hex C.int) *C.char {
	hexMode.Store(hex != 0)
	if err := engine.Connect(wincore.Config{
		Mode: wincore.ModeSerial, SerialName: C.GoString(name),
		Baud: int(baud), DataBits: int(dataBits), StopBits: int(stopBits),
		Parity: C.GoString(parity),
	}); err != nil {
		return C.CString(err.Error())
	}
	return C.CString("")
}

//export GoListen
func GoListen(address *C.char, hex C.int) *C.char {
	hexMode.Store(hex != 0)
	if err := engine.Connect(wincore.Config{Mode: wincore.ModeTCPServer, Address: C.GoString(address)}); err != nil {
		return C.CString(err.Error())
	}
	return C.CString("")
}

//export GoConnectTCP
func GoConnectTCP(address *C.char, hex C.int) *C.char {
	hexMode.Store(hex != 0)
	if err := engine.Connect(wincore.Config{Mode: wincore.ModeTCPClient, Address: C.GoString(address), AutoReconnect: true}); err != nil {
		return C.CString(err.Error())
	}
	return C.CString("")
}

//export GoListenUDP
func GoListenUDP(address *C.char, hex C.int) *C.char {
	hexMode.Store(hex != 0)
	if err := engine.Connect(wincore.Config{Mode: wincore.ModeUDPServer, Address: C.GoString(address)}); err != nil {
		return C.CString(err.Error())
	}
	return C.CString("")
}

//export GoConnectUDP
func GoConnectUDP(address *C.char, hex C.int) *C.char {
	hexMode.Store(hex != 0)
	if err := engine.Connect(wincore.Config{Mode: wincore.ModeUDPClient, Address: C.GoString(address)}); err != nil {
		return C.CString(err.Error())
	}
	return C.CString("")
}

//export GoStartSerialServer
func GoStartSerialServer(serialName *C.char, baud, dataBits, stopBits C.int, parity, protocol, role, address *C.char, hex C.int) *C.char {
	hexMode.Store(hex != 0)
	if err := engine.Connect(wincore.Config{
		Mode: wincore.ModeSerialServer, SerialName: C.GoString(serialName),
		Baud: int(baud), DataBits: int(dataBits), StopBits: int(stopBits),
		Parity: C.GoString(parity), Protocol: C.GoString(protocol),
		Role: C.GoString(role), Address: C.GoString(address),
	}); err != nil {
		return C.CString(err.Error())
	}
	return C.CString("")
}

//export GoConnectHTTP
func GoConnectHTTP(url *C.char) *C.char {
	if err := engine.Connect(wincore.Config{Mode: wincore.ModeHTTPClient, Address: C.GoString(url)}); err != nil {
		return C.CString(err.Error())
	}
	return C.CString("")
}

// vserialInfoString 把虚拟串口信息序列化成返回给 Objective-C 的 "id\x1f端点\x1f设备路径" 字符串。
func vserialInfoString(info wincore.VSerialInfo) string {
	return fmt.Sprintf("%d\x1f%s\x1f%s", info.ID, info.Addr, info.Link)
}

//export GoAddVSerial
func GoAddVSerial(address *C.char) *C.char {
	info, err := engine.AddVirtualSerial(C.GoString(address))
	if err != nil {
		return C.CString("错误:" + err.Error())
	}
	return C.CString(vserialInfoString(info))
}

//export GoRemoveVSerial
func GoRemoveVSerial(id C.int) {
	engine.RemoveVirtualSerial(int(id))
}

//export GoListVSerialLinks
func GoListVSerialLinks() *C.char {
	infos := engine.ListVirtualSerials()
	links := make([]string, 0, len(infos))
	for _, i := range infos {
		links = append(links, i.Link)
	}
	return C.CString(strings.Join(links, "\n"))
}

//export GoHTTPRequest
func GoHTTPRequest(spec *C.char) *C.char {
	if err := engine.HTTPRequest(C.GoString(spec)); err != nil {
		return C.CString(err.Error())
	}
	return C.CString("")
}

//export GoDisconnect
func GoDisconnect() { engine.Disconnect() }

//export GoSetHexView
func GoSetHexView(enabled C.int) { hexMode.Store(enabled != 0) }

//export GoToggleLoop
func GoToggleLoop(input *C.char, asHex C.int, eol *C.char, count C.int, intervalMs C.int) *C.char {
	loopMu.Lock()
	if loopCancel != nil {
		close(loopCancel)
		loopCancel = nil
		loopMu.Unlock()
		return C.CString("stopped")
	}
	if engine == nil {
		loopMu.Unlock()
		return C.CString("error:未连接")
	}
	cancel := make(chan struct{})
	loopCancel = cancel
	loopMu.Unlock()
	inp := C.GoString(input)
	hex := asHex != 0
	e := C.GoString(eol)
	n := int(count)
	ms := int(intervalMs)
	if strings.TrimSpace(inp) == "" {
		loopMu.Lock()
		loopCancel = nil
		loopMu.Unlock()
		return C.CString("error:请输入要发送的数据")
	}
	if ms < 10 {
		loopMu.Lock()
		loopCancel = nil
		loopMu.Unlock()
		return C.CString("error:发送间隔不能小于 10 ms")
	}
	if _, err := wincore.ParseData(inp, hex, e); err != nil {
		loopMu.Lock()
		loopCancel = nil
		loopMu.Unlock()
		return C.CString("error:" + err.Error())
	}
	go func() {
		cnt := 0
		for {
			select {
			case <-cancel:
				return
			default:
			}
			if n > 0 && cnt >= n {
				loopMu.Lock()
				if loopCancel == cancel {
					loopCancel = nil
				}
				loopMu.Unlock()
				C.UILoopDone()
				return
			}
			if err := engine.Send(inp, hex, e); err != nil {
				loopMu.Lock()
				if loopCancel == cancel {
					loopCancel = nil
				}
				loopMu.Unlock()
				C.UILoopDone()
				return
			}
			logSent(inp, hex, e)
			cnt++
			select {
			case <-cancel:
				return
			case <-time.After(time.Duration(ms) * time.Millisecond):
			}
		}
	}()
	return C.CString("started")
}

//export GoSend
func GoSend(text *C.char, hex C.int, eol *C.char) *C.char {
	input, asHex, e := C.GoString(text), hex != 0, C.GoString(eol)
	if err := engine.Send(input, asHex, e); err != nil {
		return C.CString(err.Error())
	}
	logSent(input, asHex, e)
	return C.CString("")
}

//export GoNetworkSend
func GoNetworkSend(text *C.char, hex C.int, eol *C.char) *C.char {
	input, asHex, e := C.GoString(text), hex != 0, C.GoString(eol)
	if err := engine.SendNetwork(input, asHex, e); err != nil {
		return C.CString(err.Error())
	}
	logSent(input, asHex, e)
	return C.CString("")
}

//export GoUDPSend
func GoUDPSend(text *C.char, hex C.int, eol *C.char) *C.char {
	input, asHex, e := C.GoString(text), hex != 0, C.GoString(eol)
	if err := engine.SendUDP(input, asHex, e); err != nil {
		return C.CString(err.Error())
	}
	logSent(input, asHex, e)
	return C.CString("")
}
