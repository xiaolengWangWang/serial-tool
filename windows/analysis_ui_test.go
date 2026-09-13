//go:build windows

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"serial-tool/internal/wincore"
	"strings"
	"testing"
)

func TestAnalysisSelectionCopiesAndBounds(t *testing.T) {
	p := []wincore.Packet{{ConnectionID: "peer", Data: []byte{1, 2}}}
	got := analysisSelection(p, []int{-1, 0, 8, 0})
	if len(got) != 1 || got[0].ConnectionID != "peer" {
		t.Fatalf("selection: %#v", got)
	}
	p[0].Data[0] = 9
	if got[0].Data[0] != 1 {
		t.Fatal("snapshot aliases live packet")
	}
}
func TestAnalysisReportRejectsOversize(t *testing.T) {
	_, err := analysisPacketReport([]wincore.Packet{{Data: make([]byte, analysisInputLimit+1)}})
	if err == nil {
		t.Fatal("expected input cap")
	}
}
func TestAnalysisAIRequiresOptInAndUsesChatEndpoint(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("incorrect request")
		}
		w.Write([]byte(`{"choices":[{"message":{"content":"local fake answer"}}]}`))
	}))
	defer srv.Close()
	cfg := analysisAIConfig{Base: srv.URL + "/v1", Key: "test-key", Model: "fake"}
	turns := []analysisTurn{{Role: "user", Content: "test"}}
	if _, err := analysisAIChat(context.Background(), cfg, turns); err == nil || calls != 0 {
		t.Fatal("disabled AI sent request")
	}
	cfg.Enabled = true
	reply, err := analysisAIChat(context.Background(), cfg, turns)
	if err != nil || !strings.Contains(reply, "fake answer") || calls != 1 {
		t.Fatalf("%q %v calls=%d", reply, err, calls)
	}
}
