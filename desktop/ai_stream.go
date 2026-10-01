//go:build darwin && cgo

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

type aiTurn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type aiStreamConfig struct {
	Enabled      bool
	BaseURL      string
	Model        string
	Key          string
	SystemPrompt string
	IdleTimeout  time.Duration
}

func aiPacketPrompt(transport, input string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", errors.New("没有可分析的报文")
	}
	if len(input) > 16*1024 {
		return "", errors.New("分析数据超过 16 KiB，请先筛选或减少报文")
	}
	return fmt.Sprintf("请分析以下 %s 通信报文。只根据给定数据说明协议、异常、风险和现场排查建议；不确定时明确说明，不要臆测串口参数。报文为 HEX：\n%s", transport, input), nil
}

func aiReportPrompt(report string) (string, error) {
	report = strings.TrimSpace(report)
	if report == "" {
		return "", errors.New("没有可分析的数据库数据")
	}
	const maxReport = 32 * 1024
	if len(report) > maxReport {
		report = truncateUTF8(report, maxReport) + "\n……（报告过长，已截断）"
	}
	return "以下是本地 SQLite 采集数据的分析报告，含统计与逐条 HEX 报文解析。请据此做协议识别、异常定位、风险评估与现场排查建议；只依据报告内容，不确定时明确说明，不臆测未给出的参数。\n\n" + report, nil
}

// truncateUTF8 keeps the byte limit without splitting a multibyte character.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

type aiReaderFunc func([]byte) (int, error)

func (f aiReaderFunc) Read(p []byte) (int, error) { return f(p) }

type aiStreamSnapshot struct {
	Delta   string `json:"delta"`
	Answer  string `json:"answer"`
	Error   string `json:"error"`
	Done    bool   `json:"done"`
	Stopped bool   `json:"stopped"`
}

type aiStreamSession struct {
	mu      sync.Mutex
	pending strings.Builder
	result  aiStreamSnapshot
	cancel  context.CancelFunc
}

var aiStreamSessions sync.Map
var aiStreamNextID atomic.Uint64

func startAIStreamSession(cfg aiStreamConfig, turns []aiTurn) (uint64, error) {
	if len(turns) == 0 {
		return 0, errors.New("对话内容为空")
	}
	ctx, cancel := context.WithCancel(context.Background())
	session := &aiStreamSession{cancel: cancel}
	id := aiStreamNextID.Add(1)
	aiStreamSessions.Store(id, session)
	go func() {
		answer, err := aiStreamChat(ctx, cfg, turns, func(delta string) {
			session.mu.Lock()
			session.pending.WriteString(delta)
			session.mu.Unlock()
		})
		session.mu.Lock()
		session.result.Answer = answer
		if err != nil {
			session.result.Error = err.Error()
			session.result.Stopped = errors.Is(err, context.Canceled)
		}
		session.result.Done = true
		session.mu.Unlock()
		cancel()
	}()
	return id, nil
}

func pollAIStreamSession(id uint64) (aiStreamSnapshot, bool) {
	v, ok := aiStreamSessions.Load(id)
	if !ok {
		return aiStreamSnapshot{}, false
	}
	session := v.(*aiStreamSession)
	session.mu.Lock()
	result := session.result
	result.Delta = session.pending.String()
	session.pending.Reset()
	session.mu.Unlock()
	if result.Done {
		aiStreamSessions.Delete(id)
	}
	return result, true
}

func stopAIStreamSession(id uint64) bool {
	v, ok := aiStreamSessions.Load(id)
	if ok {
		v.(*aiStreamSession).cancel()
	}
	return ok
}

// aiStreamChat returns the complete reply and emits each received piece. On stop or error it
// returns the partial reply too, so the conversation can keep what was already visible.
func aiStreamChat(parent context.Context, cfg aiStreamConfig, turns []aiTurn, onDelta func(string)) (string, error) {
	if !cfg.Enabled {
		return "", errors.New("AI 未启用，请先在 AI 设置中启用")
	}
	key := strings.TrimSpace(cfg.Key)
	if key == "" || key != cfg.Key || strings.ContainsAny(key, " \t\r\n") {
		return "", errors.New("AI Key 为空或包含空白字符")
	}
	for _, r := range key {
		if r < 32 || r == 127 {
			return "", errors.New("AI Key 包含控制字符")
		}
	}
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		base = "https://api.deepseek.com"
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("AI Base URL 无效，请使用 HTTP(S) 地址")
	}
	if !strings.HasSuffix(base, "/chat/completions") {
		base += "/chat/completions"
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		model = "deepseek-chat"
	}
	if len(turns) == 0 {
		return "", errors.New("对话内容为空")
	}
	var size int
	for _, turn := range turns {
		if (turn.Role != "user" && turn.Role != "assistant") || strings.TrimSpace(turn.Content) == "" {
			return "", errors.New("对话内容无效")
		}
		size += len(turn.Content)
	}
	if size > 512<<10 {
		return "", errors.New("对话内容超过 512 KiB，请开始新对话")
	}
	system := strings.TrimSpace(cfg.SystemPrompt)
	if system == "" {
		system = "你是工业通信现场诊断助手。结论仅作排查建议，优先建议查阅设备协议文档。"
	}
	messages := append([]aiTurn{{Role: "system", Content: system}}, turns...)
	body, err := json.Marshal(map[string]any{"model": model, "temperature": 0.1, "stream": true, "messages": messages})
	if err != nil {
		return "", err
	}
	idle := cfg.IdleTimeout
	if idle <= 0 {
		idle = 60 * time.Second
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	var idleHit atomic.Bool
	timer := time.AfterFunc(idle, func() { idleHit.Store(true); cancel() })
	defer timer.Stop()
	failure := func(err error) error {
		if parent.Err() != nil {
			return parent.Err()
		}
		if idleHit.Load() {
			return fmt.Errorf("超过 %d 秒没有收到 AI 回复，已中止", int(idle.Seconds()))
		}
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream, application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return "", failure(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var payload struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&payload)
		if payload.Error.Message != "" {
			return "", fmt.Errorf("AI 服务返回 HTTP %d：%s", resp.StatusCode, payload.Error.Message)
		}
		return "", fmt.Errorf("AI 服务返回 HTTP %d", resp.StatusCode)
	}
	reader := io.LimitReader(aiReaderFunc(func(p []byte) (int, error) {
		n, err := resp.Body.Read(p)
		if n > 0 {
			timer.Reset(idle)
		}
		return n, err
	}), 4<<20)
	if !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		var payload struct {
			Choices []struct {
				Message aiTurn `json:"message"`
			} `json:"choices"`
		}
		if err := json.NewDecoder(io.LimitReader(reader, 1<<20)).Decode(&payload); err != nil {
			return "", failure(fmt.Errorf("AI 响应解析失败：%w", err))
		}
		if len(payload.Choices) == 0 || strings.TrimSpace(payload.Choices[0].Message.Content) == "" {
			return "", errors.New("AI 未返回分析结果")
		}
		answer := payload.Choices[0].Message.Content
		if onDelta != nil {
			onDelta(answer)
		}
		return answer, nil
	}
	var answer strings.Builder
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(data), &chunk) != nil {
			continue
		}
		if chunk.Error != nil {
			return answer.String(), fmt.Errorf("AI 服务中途报错：%s", chunk.Error.Message)
		}
		if len(chunk.Choices) == 0 || chunk.Choices[0].Delta.Content == "" {
			continue
		}
		delta := chunk.Choices[0].Delta.Content
		if answer.Len()+len(delta) > 1<<20 {
			return answer.String(), errors.New("AI 回答超过 1 MiB，已截断")
		}
		answer.WriteString(delta)
		if onDelta != nil {
			onDelta(delta)
		}
	}
	if err := failure(scanner.Err()); err != nil {
		return answer.String(), err
	}
	if strings.TrimSpace(answer.String()) == "" {
		return "", errors.New("AI 未返回分析结果")
	}
	return answer.String(), nil
}
