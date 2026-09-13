//go:build windows

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"serial-tool/internal/wincore"
	"strings"
	"testing"
	"time"
)

func TestAnalysisScopeUsesVisibleSelectionAndRejectsOversize(t *testing.T) {
	all := []Packet{packetFromCore(wincore.Packet{ID: "hidden", Data: []byte{1}}), packetFromCore(wincore.Packet{ID: "visible", Data: []byte{2}})}
	got, err := selectAnalysisPackets(all, all[1:], []int{0}, 0, 0, 0, 500)
	if err != nil || len(got) != 1 || got[0].ID != "visible" {
		t.Fatalf("selection: %+v %v", got, err)
	}
	all[1].Raw.Data[0] = 9
	if got[0].Data[0] != 2 {
		t.Fatal("selection aliases live bytes")
	}
	if _, err = selectAnalysisPackets(all, all, nil, 1, 0, 0, 1); err == nil {
		t.Fatal("oversize selection silently truncated")
	}
	if _, err = selectAnalysisPackets(all, all, nil, 3, 0, 2, 500); err == nil {
		t.Fatal("invalid custom range accepted")
	}
}

func TestAIContextRetainsTimestampAndSession(t *testing.T) {
	p := wincore.Packet{Timestamp: time.Unix(123, 0), ConnectionID: "peer-1", Endpoint: "127.0.0.1:5000", Data: []byte{0, 255}}
	got, err := analysisContext([]wincore.Packet{p}, "TCP", "connected")
	if err != nil || !strings.Contains(got, "peer-1") || !strings.Contains(got, "00 FF") || !strings.Contains(got, p.Timestamp.Format(time.RFC3339Nano)) {
		t.Fatalf("context lost data: %s %v", got, err)
	}
	p.Data = make([]byte, 30000)
	if _, err = analysisContext([]wincore.Packet{p}, "TCP", ""); err == nil {
		t.Fatal("context cap missing")
	}
}

func TestAIRequestCancellationAndTimeout(t *testing.T) {
	started := make(chan struct{}, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
		}
	}))
	defer srv.Close()
	cfg := analysisAIConfig{Enabled: true, Base: srv.URL, Key: "test", Model: "fake", Timeout: 50 * time.Millisecond}
	begin := time.Now()
	_, err := analysisAIChat(context.Background(), cfg, []analysisTurn{{Role: "user", Content: "hello"}})
	if err == nil || time.Since(begin) > 500*time.Millisecond {
		t.Fatalf("timeout: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = analysisAIChat(ctx, cfg, []analysisTurn{{Role: "user", Content: "hello"}}); err == nil {
		t.Fatal("cancel ignored")
	}
}
