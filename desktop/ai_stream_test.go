//go:build darwin && cgo

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func testAIConfig(url string) aiStreamConfig {
	return aiStreamConfig{Enabled: true, BaseURL: url, Model: "test-model", Key: "test-key", SystemPrompt: "测试指南", IdleTimeout: time.Second}
}

func TestAIStreamChatShowsDeltasAndKeepsContext(t *testing.T) {
	var sent struct {
		Model    string   `json:"model"`
		Stream   bool     `json:"stream"`
		Messages []aiTurn `json:"messages"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("missing API key")
		}
		if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range []string{"你", "好"} {
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"" + chunk + "\"}}]}\n\n"))
			w.(http.Flusher).Flush()
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()
	turns := []aiTurn{{Role: "user", Content: "分析 01 03"}, {Role: "assistant", Content: "已有结论"}, {Role: "user", Content: "继续解释"}}
	var deltas []string
	got, err := aiStreamChat(context.Background(), testAIConfig(server.URL), turns, func(s string) { deltas = append(deltas, s) })
	if err != nil || got != "你好" || !reflect.DeepEqual(deltas, []string{"你", "好"}) {
		t.Fatalf("stream = %q, %v, deltas %q", got, err, deltas)
	}
	if sent.Model != "test-model" || !sent.Stream || !reflect.DeepEqual(sent.Messages, append([]aiTurn{{Role: "system", Content: "测试指南"}}, turns...)) {
		t.Fatalf("request lost context: %#v", sent)
	}
}

func TestAIStreamChatAcceptsJSONReply(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"完整回答"}}]}`))
	}))
	defer server.Close()
	var delta string
	got, err := aiStreamChat(context.Background(), testAIConfig(server.URL), []aiTurn{{Role: "user", Content: "问题"}}, func(s string) { delta += s })
	if err != nil || got != "完整回答" || delta != got {
		t.Fatalf("JSON fallback = %q, %v, delta %q", got, err, delta)
	}
}

func TestAIStreamChatStopPreservesPartialReply(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"已收到\"}}]}\n\n"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got, err := aiStreamChat(ctx, testAIConfig(server.URL), []aiTurn{{Role: "user", Content: "问题"}}, func(string) { cancel() })
	if got != "已收到" || !errors.Is(err, context.Canceled) {
		t.Fatalf("stopped stream = %q, %v", got, err)
	}
}

func TestAIStreamChatReportsHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
	}))
	defer server.Close()
	_, err := aiStreamChat(context.Background(), testAIConfig(server.URL), []aiTurn{{Role: "user", Content: "问题"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "bad key") {
		t.Fatalf("HTTP error = %v", err)
	}
}

func TestAIStreamSessionPollAndStop(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"部分回答\"}}]}\n\n"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	id, err := startAIStreamSession(testAIConfig(server.URL), []aiTurn{{Role: "user", Content: "问题"}})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(2 * time.Second)
	var partial string
	for partial == "" {
		select {
		case <-deadline:
			t.Fatal("stream delta never reached polling")
		default:
		}
		state, ok := pollAIStreamSession(id)
		if !ok {
			t.Fatal("session disappeared before completion")
		}
		partial += state.Delta
		time.Sleep(5 * time.Millisecond)
	}
	if partial != "部分回答" || !stopAIStreamSession(id) {
		t.Fatalf("poll/stop = %q", partial)
	}
	for {
		select {
		case <-deadline:
			t.Fatal("stopped session never finished")
		default:
		}
		state, ok := pollAIStreamSession(id)
		if !ok {
			t.Fatal("session disappeared before final poll")
		}
		if state.Done {
			if state.Answer != "部分回答" || !state.Stopped {
				t.Fatalf("final state = %#v", state)
			}
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, ok := pollAIStreamSession(id); ok {
		t.Fatal("completed session was not removed")
	}
}

func TestAIPromptsKeepAnalysisLimits(t *testing.T) {
	packet, err := aiPacketPrompt("TCP", "01 03")
	if err != nil || !strings.Contains(packet, "TCP 通信报文") || !strings.Contains(packet, "01 03") {
		t.Fatalf("packet prompt = %q, %v", packet, err)
	}
	if _, err := aiPacketPrompt("TCP", strings.Repeat("A", 16*1024+1)); err == nil {
		t.Fatal("packet data over 16 KiB accepted")
	}
	report, err := aiReportPrompt(strings.Repeat("记录", 12*1024))
	if err != nil || !utf8.ValidString(report) || !strings.Contains(report, "已截断") {
		t.Fatalf("report prompt = %q, %v", report[:min(len(report), 100)], err)
	}
}
