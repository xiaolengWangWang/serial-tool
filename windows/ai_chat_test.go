//go:build windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lxn/win"
	"serial-tool/internal/wincore"
)

// sseServer 按 OpenAI 兼容的流式格式逐段返回 parts，每段之间停 gap；记录收到的请求体。
func sseServer(t *testing.T, gap time.Duration, parts ...string) (*httptest.Server, *[]string) {
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Stream   bool           `json:"stream"`
			Messages []analysisTurn `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		bodies = append(bodies, req.Messages[len(req.Messages)-1].Content)
		mu.Unlock()
		if !req.Stream {
			t.Error("request did not ask for streaming")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		fmt.Fprint(w, ": keep-alive\n\n")
		fl.Flush()
		for _, p := range parts {
			time.Sleep(gap)
			b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": p}}}})
			fmt.Fprintf(w, "data: %s\n\n", b)
			fl.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	return srv, &bodies
}

func TestAIStreamAssemblesDeltas(t *testing.T) {
	srv, _ := sseServer(t, 0, "## 结论\n", "CRC ", "不匹配")
	cfg := analysisAIConfig{Enabled: true, Base: srv.URL, Key: "k", Model: "m", Timeout: time.Second}
	var got []string
	answer, err := analysisAIStream(context.Background(), cfg, []analysisTurn{{Role: "user", Content: "q"}}, func(s string) { got = append(got, s) })
	if err != nil || answer != "## 结论\nCRC 不匹配" || len(got) != 3 {
		t.Fatalf("answer=%q err=%v deltas=%q", answer, err, got)
	}
}

// 超时按「多久没有新内容」计：总时长超过 Timeout、但一直在输出的回答不能被截断；
// 真正卡住时报出等待的秒数。
func TestAIStreamIdleTimeoutNotTotal(t *testing.T) {
	srv, _ := sseServer(t, 120*time.Millisecond, "a", "b", "c", "d", "e")
	cfg := analysisAIConfig{Enabled: true, Base: srv.URL, Key: "k", Model: "m", Timeout: 400 * time.Millisecond}
	answer, err := analysisAIStream(context.Background(), cfg, []analysisTurn{{Role: "user", Content: "q"}}, nil)
	if err != nil || answer != "abcde" {
		t.Fatalf("slow but steady stream cut off: %q %v", answer, err)
	}
	stall, _ := sseServer(t, 2*time.Second, "x")
	cfg.Base = stall.URL
	_, err = analysisAIStream(context.Background(), cfg, []analysisTurn{{Role: "user", Content: "q"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "没有收到 AI 回复") {
		t.Fatalf("stalled stream: %v", err)
	}
}

func TestAIStreamStopKeepsPartial(t *testing.T) {
	srv, _ := sseServer(t, 150*time.Millisecond, "已生成", "部分", "尾巴")
	cfg := analysisAIConfig{Enabled: true, Base: srv.URL, Key: "k", Model: "m", Timeout: time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	answer, err := analysisAIStream(ctx, cfg, []analysisTurn{{Role: "user", Content: "q"}}, func(s string) {
		if s == "部分" {
			cancel()
		}
	})
	if !errors.Is(err, errAIStopped) || !strings.HasPrefix(answer, "已生成部分") {
		t.Fatalf("stop: %q %v", answer, err)
	}
}

func TestAIHTTPErrorExplainsReason(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(`{"error":{"message":"Insufficient Balance","type":"unknown_error"}}`))
	}))
	defer srv.Close()
	cfg := analysisAIConfig{Enabled: true, Base: srv.URL, Key: "k", Model: "m"}
	_, err := analysisAIStream(context.Background(), cfg, []analysisTurn{{Role: "user", Content: "q"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "402") || !strings.Contains(err.Error(), "余额不足") || !strings.Contains(err.Error(), "Insufficient Balance") {
		t.Fatalf("error: %v", err)
	}
}

func TestMarkdownPlainReadable(t *testing.T) {
	md := "# 诊断报告\n\n## 一、**协议**识别\n\n- 从站 `0x01`\n  - 功能码 **03**\n1. 先检查接线\n\n| 项目 | 结论 |\n|---|---|\n| CRC | 不匹配 |\n\n| 帧 | 方向 | 结果 |\n|:-:|---|--|\n| 4 | RX | 错 |\n\n```\n01 03 08\n```\n> 提示：[手册](http://x/m)\n---"
	got := markdownPlain(md)
	for _, want := range []string{"■ 诊断报告", "■ 一、协议识别", "• 从站 0x01", "  • 功能码 03", "1. 先检查接线", "• CRC：不匹配", "• 帧：4；方向：RX；结果：错", "    01 03 08", "│ 提示：手册（http://x/m）", "────────"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	for _, bad := range []string{"**", "`", "|", "##", "```"} {
		if strings.Contains(got, bad) {
			t.Errorf("leftover %q in:\n%s", bad, got)
		}
	}
}

// AI 面板的完整对话：分析与追问排在同一段对话里，追问不覆盖分析结果；Enter 发送后清空
// 输入框；发给服务的报文附带本地解析（改坏 CRC 的帧标为不匹配）。
func TestAssistantConversationFlow(t *testing.T) {
	srv, bodies := sseServer(t, 30*time.Millisecond, "## 结论\n", "第 2 条 **CRC 不匹配**。")
	a := newWorkbenchForTestWith(t, nil)
	var errs []string
	driveWorkbench(a, func(onUI func(func())) {
		onUI(func() {
			win.ShowWindow(a.mw.Handle(), win.SW_SHOWNOACTIVATE)
			now := time.Now()
			for i, hexData := range []string{"01 03 00 00 00 04 44 09", "01 03 08 00 67 01 F4 FF 9C 00 0A 43 D7"} {
				data, _ := wincore.ParseData(hexData, true, "无")
				dir := []string{"TX", "RX"}[i]
				p := wincore.Packet{Timestamp: now.Add(time.Duration(i) * time.Second), Direction: dir, Transport: "SERIAL", ConnectionID: "COM3", Data: data}
				a.packetModel.add(Packet{Raw: p, TS: p.Timestamp, Direction: dir, Hex: hexData, Length: len(data)}, "", dirAll)
			}
			a.showAssistant()
			w := a.assistant
			w.enabled = true
			w.config.Base, w.config.Key, w.config.Model = srv.URL, "test", "fake"
			w.scope.SetCurrentIndex(1)
			w.analyze("全面诊断")
		})
		wait := func() {
			for i := 0; i < 100; i++ {
				time.Sleep(50 * time.Millisecond)
				var busy bool
				onUI(func() { busy = a.assistant.chat.busy })
				if !busy {
					return
				}
			}
			errs = append(errs, "AI 请求超时未结束")
		}
		wait()
		var first string
		onUI(func() {
			c := a.assistant.chat
			if len(c.entries) != 2 || c.entries[0].role != "user" || c.entries[1].role != "assistant" {
				errs = append(errs, fmt.Sprintf("分析后应有 你 + AI 两条: %+v", c.entries))
				return
			}
			first = c.entries[1].text
			view := c.view.Text()
			if !strings.Contains(view, "── 你 · ") || !strings.Contains(view, "■ 结论") || strings.Contains(view, "**") {
				errs = append(errs, "对话显示不对:\n"+view)
			}
			if !strings.Contains(c.status.Text(), "AI 回答完成") {
				errs = append(errs, "状态: "+c.status.Text())
			}
			c.input.SetText("第 2 条怎么修？")
			c.submit()
			if c.input.Text() != "" {
				errs = append(errs, "发送后输入框没有清空")
			}
		})
		wait()
		onUI(func() {
			c := a.assistant.chat
			if len(c.entries) != 4 || c.entries[1].text != first || c.entries[2].text != "第 2 条怎么修？" {
				errs = append(errs, fmt.Sprintf("追问应追加在后面且不覆盖分析结果: %+v", c.entries))
			}
			if len(c.turns) != 4 {
				errs = append(errs, fmt.Sprintf("追问上下文应有 4 轮，实际 %d", len(c.turns)))
			}
		})
	})
	if len(*bodies) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(*bodies))
	}
	if !strings.Contains((*bodies)[0], "本地解析：Modbus RTU 从站 1 功能码 0x03 读保持寄存器 数据字节数 8，CRC 不匹配（收到 0xD743，应为 0x2843）") {
		t.Errorf("first request lacks local parse:\n%s", (*bodies)[0])
	}
	if (*bodies)[1] != "第 2 条怎么修？" {
		t.Errorf("follow-up should send only the question, got %q", (*bodies)[1])
	}
	for _, e := range errs {
		t.Error(e)
	}
}
