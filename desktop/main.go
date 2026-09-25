//go:build darwin && cgo

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
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
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
	dataDir := os.Getenv("COMMBOX_DATA_DIR")
	if dataDir == "" {
		dataDir = filepath.Join(configDir, "CommBox", "data")
	}
	engine, err = wincore.New(dataDir, nil, onLog)
	if err != nil {
		return
	}
	engine.SetOnClosed(onClosed)
	engine.SetOnPacket(onPacket)
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

func packetModel(packet wincore.Packet) map[string]any {
	p := packetDisplayFields(packet.Timestamp.Local().Format("15:04:05.000"), packet.Direction, packet.Data)
	return map[string]any{"id": packet.ID, "session_key": packet.SessionKey,
		"ts": p.ts, "dir": p.dir, "hex": p.hex, "ascii": p.ascii, "kind": p.kind,
		"rawLen": p.len, "len": fmt.Sprintf("%d B", p.len), "epoch": float64(packet.Timestamp.UnixNano()) / 1e9,
		"protocol": packet.Transport, "source": packet.Source, "connection_id": packet.ConnectionID,
		"endpoint": packet.Endpoint, "leg": packet.Leg, "status": "未分析", "response": "—"}
}

func onPacket(packet wincore.Packet) {
	data, _ := json.Marshal(packetModel(packet))
	raw := C.CString(string(data))
	C.UIAddPacketJSON(raw)
	// 监控窗口复用同一份报文模型:含 ts/dir/hex/ascii/source,支持 HEX/ASCII 切换与过滤。
	C.UIMonitorAppendJSON(raw)
	C.free(unsafe.Pointer(raw))
}

//export GoConnections
func GoConnections() *C.char {
	data, _ := json.Marshal(append(engine.Connections(), engine.UDPPeers()...))
	return C.CString(string(data))
}

//export GoDisconnectTarget
func GoDisconnectTarget(id *C.char) *C.char {
	if err := engine.DisconnectConnection(C.GoString(id)); err != nil {
		return C.CString(err.Error())
	}
	return C.CString("")
}

//export GoSendTarget
func GoSendTarget(id, address, input *C.char, asHex C.int, eol *C.char) *C.char {
	data, err := wincore.ParseData(C.GoString(input), asHex != 0, C.GoString(eol))
	if err == nil {
		if C.GoString(address) != "" {
			err = engine.SendToUDP(C.GoString(address), data)
		} else {
			err = engine.SendToConnection(C.GoString(id), data)
		}
	}
	if err != nil {
		return C.CString(err.Error())
	}
	return C.CString("")
}

//export GoConnectionPolicy
func GoConnectionPolicy(maximum C.int, latest C.int) {
	engine.SetMaxConnections(int(maximum))
	engine.SetBridgeReplyLatest(latest != 0)
}

//export GoValidateSend
func GoValidateSend(input *C.char, asHex C.int, eol *C.char) *C.char {
	data, err := wincore.ParseData(C.GoString(input), asHex != 0, C.GoString(eol))
	out := map[string]any{"bytes": len(data), "hex": fmt.Sprintf("% X", data), "error": ""}
	if err != nil {
		out["error"] = err.Error()
	}
	raw, _ := json.Marshal(out)
	return C.CString(string(raw))
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

// deepseekChat 单轮对话（一个 user 消息）。
func deepseekChat(userPrompt string) string {
	return deepseekChatMessages([]map[string]string{{"role": "user", "content": userPrompt}})
}

// deepseekChatMessages 发起一次可多轮对话；turns 为不含 system 的用户/助手消息序列，
// 系统提示（分析指南）由本函数统一前置。出错时返回中文错误串。
func deepseekChatMessages(turns []map[string]string) string {
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
	messages := append([]map[string]string{{"role": "system", "content": system}}, turns...)
	body, _ := json.Marshal(map[string]any{"model": model, "temperature": 0.1, "messages": messages})
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

// GoAIChat 多轮对话：messagesJSON 是 [{"role":"user|assistant","content":"..."}] 序列，
// 用于在首次分析后继续追问，保持上下文。
//
//export GoAIChat
func GoAIChat(messagesJSON *C.char) *C.char {
	var turns []map[string]string
	if err := json.Unmarshal([]byte(C.GoString(messagesJSON)), &turns); err != nil || len(turns) == 0 {
		return C.CString("对话内容无效")
	}
	return C.CString(deepseekChatMessages(turns))
}

// GoSaveAnalysisMarkdown 把分析/对话内容写入数据目录下 ai-analysis/<filename>，返回完整路径或错误串。
//
//export GoSaveAnalysisMarkdown
func GoSaveAnalysisMarkdown(filename, content *C.char) *C.char {
	if engine == nil {
		return C.CString("错误:引擎未初始化")
	}
	name := filepath.Base(C.GoString(filename))
	if name == "" || name == "." || name == "/" || strings.ContainsAny(name, "/\\\x00") {
		return C.CString("错误:文件名无效")
	}
	dir := filepath.Join(engine.DataDir(), "ai-analysis")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return C.CString("错误:" + err.Error())
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(C.GoString(content)), 0o644); err != nil {
		return C.CString("错误:" + err.Error())
	}
	return C.CString(path)
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
		"client_ips": st.ClientIPs, "serial_rx": st.SerialRXBytes, "serial_tx": st.SerialTXBytes, "network_rx": st.NetworkRXBytes, "network_tx": st.NetworkTXBytes,
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
	return C.CString("")
}

//export GoNetworkSend
func GoNetworkSend(text *C.char, hex C.int, eol *C.char) *C.char {
	input, asHex, e := C.GoString(text), hex != 0, C.GoString(eol)
	if err := engine.SendNetwork(input, asHex, e); err != nil {
		return C.CString(err.Error())
	}
	return C.CString("")
}

//export GoUDPSend
func GoUDPSend(text *C.char, hex C.int, eol *C.char) *C.char {
	input, asHex, e := C.GoString(text), hex != 0, C.GoString(eol)
	if err := engine.SendUDP(input, asHex, e); err != nil {
		return C.CString(err.Error())
	}
	return C.CString("")
}

// ---- 在线更新 ----

// updateReleasesPage 是检查失败时兜底的发布页地址。
const updateReleasesPage = "https://github.com/xiaolengWangWang/serial-tool/releases"

// settingAutoUpdate 记录是否在启动时检查更新。空值按开启处理:检查只发一个 GET、
// 不带任何用户数据,老用户升级上来无需先去设置里打开。
const settingAutoUpdate = "update.auto_check"

var (
	updateMu     sync.Mutex
	lastUpdate   wincore.UpdateInfo // 最近一次 GoCheckUpdate 的结果,供下载复用
	updateCancel context.CancelFunc // 下载进行中时可取消
)

func jsonCString(m map[string]any) *C.char {
	data, err := json.Marshal(m)
	if err != nil {
		return C.CString(`{"ok":false,"error":"内部错误"}`)
	}
	return C.CString(string(data))
}

// updateDownloadDir 优先放到"下载"文件夹,取不到时退回临时目录。
func updateDownloadDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		dir := filepath.Join(home, "Downloads")
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			return dir
		}
	}
	return filepath.Join(os.TempDir(), "CommBox-update")
}

//export GoAutoUpdateEnabled
func GoAutoUpdateEnabled() C.int {
	if engine != nil && engine.GetSetting(settingAutoUpdate) == "0" {
		return 0
	}
	return 1
}

//export GoSetAutoUpdate
func GoSetAutoUpdate(on C.int) {
	if engine == nil {
		return
	}
	v := "1"
	if on == 0 {
		v = "0"
	}
	_ = engine.SetSetting(settingAutoUpdate, v)
}

//export GoReleasesPage
func GoReleasesPage() *C.char { return C.CString(updateReleasesPage) }

// GoCheckUpdate 查最新发布并与当前版本比较,结果以 JSON 返回。缓存 UpdateInfo
// 供随后的 GoDownloadUpdate 复用,避免下载前再查一次。由后台队列调用(会阻塞)。
//
//export GoCheckUpdate
func GoCheckUpdate() *C.char {
	ctx, cancel := context.WithTimeout(context.Background(), wincore.UpdateCheckBudget)
	defer cancel()
	info, err := wincore.CheckUpdate(ctx, wincore.Version)
	m := map[string]any{"current": wincore.Version}
	if err != nil {
		m["ok"] = false
		m["error"] = err.Error()
		return jsonCString(m)
	}
	updateMu.Lock()
	lastUpdate = info
	updateMu.Unlock()
	m["ok"] = true
	m["newer"] = info.Newer
	m["version"] = info.Version
	m["name"] = info.Name
	m["notes"] = info.Notes
	m["pageURL"] = info.PageURL
	m["hasAsset"] = info.AssetURL != ""
	m["assetName"] = info.AssetName
	m["assetSizeText"] = wincore.FormatBytes(uint64(info.AssetSize))
	return jsonCString(m)
}

// GoDownloadUpdate 下载缓存的安装包到"下载"文件夹并校验,成功后 open DMG 让
// Finder 弹出挂载,用户把 CommBox 拖入 Applications。进度经 UIUpdateProgress 回报。
// 由后台队列调用(会阻塞),GoCancelUpdateDownload 可中途取消。
//
//export GoDownloadUpdate
func GoDownloadUpdate() *C.char {
	updateMu.Lock()
	info := lastUpdate
	ctx, cancel := context.WithCancel(context.Background())
	updateCancel = cancel
	updateMu.Unlock()
	defer func() {
		updateMu.Lock()
		updateCancel = nil
		updateMu.Unlock()
		cancel()
	}()

	if info.AssetURL == "" {
		return jsonCString(map[string]any{"ok": false, "error": "本次发布没有可下载的 macOS 安装包"})
	}
	path, err := wincore.DownloadUpdate(ctx, info, updateDownloadDir(), func(done, total int64) {
		C.UIUpdateProgress(C.longlong(done), C.longlong(total))
	})
	if err != nil {
		return jsonCString(map[string]any{"ok": false, "error": err.Error()})
	}
	// DMG 双击挂载即用:直接 open 让 Finder 弹出,失败也不致命,包已经下好了。
	opened := exec.Command("open", path).Start() == nil
	return jsonCString(map[string]any{"ok": true, "path": path, "opened": opened})
}

//export GoCancelUpdateDownload
func GoCancelUpdateDownload() {
	updateMu.Lock()
	if updateCancel != nil {
		updateCancel()
	}
	updateMu.Unlock()
}

// ---- HTTP 工作区 ----

// httpUISpec 是 HTTP 工作区与 Go 之间的请求参数,字段与界面控件一一对应。
type httpUISpec struct {
	Method     string  `json:"method"`
	URL        string  `json:"url"`
	Headers    string  `json:"headers"` // 每行 "Name: Value"
	Body       string  `json:"body"`
	TimeoutSec float64 `json:"timeoutSec"`
	ConnectSec float64 `json:"connectSec"`
	Follow     bool    `json:"follow"`
	Insecure   bool    `json:"insecure"`
}

func parseHeaderLines(s string) http.Header {
	h := http.Header{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		i := strings.IndexByte(line, ':')
		if i <= 0 {
			continue
		}
		h.Add(strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+1:]))
	}
	return h
}

func headerLines(h http.Header) string {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		for _, v := range h[k] {
			fmt.Fprintf(&b, "%s: %s\n", k, v)
		}
	}
	return b.String()
}

func uiSpecToRequest(u httpUISpec) wincore.HTTPRequestSpec {
	spec := wincore.HTTPRequestSpec{
		Method:          u.Method,
		URL:             u.URL,
		Headers:         parseHeaderLines(u.Headers),
		Body:            []byte(u.Body),
		FollowRedirects: u.Follow,
		Insecure:        u.Insecure,
	}
	if u.TimeoutSec > 0 {
		spec.Timeout = time.Duration(u.TimeoutSec * float64(time.Second))
	}
	if u.ConnectSec > 0 {
		spec.ConnectTimeout = time.Duration(u.ConnectSec * float64(time.Second))
	}
	return spec
}

// GoHTTPSend 用工作区参数执行一次请求(复用已连接 HTTP 客户端的 Cookie),
// 结果以 JSON 返回。由后台队列调用(会阻塞)。
//
//export GoHTTPSend
func GoHTTPSend(specJSON *C.char) *C.char {
	var u httpUISpec
	if err := json.Unmarshal([]byte(C.GoString(specJSON)), &u); err != nil {
		return jsonCString(map[string]any{"ok": false, "error": "参数解析失败: " + err.Error()})
	}
	spec := uiSpecToRequest(u)
	timeout := spec.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout+10*time.Second)
	defer cancel()
	res, err := engine.DoHTTPRequest(ctx, spec)
	if err != nil {
		return jsonCString(map[string]any{"ok": false, "error": err.Error()})
	}
	body := res.PrettyBody
	if len(body) == 0 {
		body = res.RawBody
	}
	return jsonCString(map[string]any{
		"ok": true, "statusCode": res.StatusCode, "status": res.Status,
		"durationMs": res.Duration.Milliseconds(), "size": res.ByteSize, "url": res.URL,
		"headers": headerLines(res.Headers), "body": string(body), "rawBody": string(res.RawBody),
	})
}

// GoParseCURL 解析 cURL 命令(仅解析,不执行 shell),把字段回填工作区。
//
//export GoParseCURL
func GoParseCURL(cmd *C.char) *C.char {
	spec, err := wincore.ParseCURL(C.GoString(cmd))
	if err != nil {
		return jsonCString(map[string]any{"ok": false, "error": err.Error()})
	}
	body := string(spec.Body)
	if body == "" && len(spec.Data) > 0 {
		parts := make([]string, 0, len(spec.Data))
		for _, d := range spec.Data {
			parts = append(parts, d.Value)
		}
		body = strings.Join(parts, "&")
	}
	u := map[string]any{
		"ok": true, "method": spec.Method, "url": spec.URL,
		"headers": headerLines(spec.Headers), "body": body,
		"timeoutSec": spec.Timeout.Seconds(), "connectSec": spec.ConnectTimeout.Seconds(),
		"follow": spec.FollowRedirects, "insecure": spec.Insecure,
	}
	if len(spec.Form) > 0 {
		u["note"] = "该 cURL 含 form/文件上传,请求体未导入,请手动处理"
	}
	return jsonCString(u)
}

// GoFormatCURL 把工作区参数导出成 cURL 命令。导出内容可能含认证信息,只返回给界面显示。
//
//export GoFormatCURL
func GoFormatCURL(specJSON *C.char) *C.char {
	var u httpUISpec
	if err := json.Unmarshal([]byte(C.GoString(specJSON)), &u); err != nil {
		return jsonCString(map[string]any{"ok": false, "error": err.Error()})
	}
	cmd, err := wincore.FormatCURL(uiSpecToRequest(u))
	if err != nil {
		return jsonCString(map[string]any{"ok": false, "error": err.Error()})
	}
	return jsonCString(map[string]any{"ok": true, "curl": cmd})
}
