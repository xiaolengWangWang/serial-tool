//go:build darwin && cgo

package main

import (
	"serial-tool/internal/wincore"
	"testing"
	"time"
	"unicode/utf8"
)

func TestPacketDisplayFields(t *testing.T) {
	got := packetDisplayFields("12:34:56.789", "RX", []byte{'A', 0, 0xff, ' '})
	want := packetDisplay{
		ts:    "12:34:56.789",
		dir:   "RX",
		hex:   "41 00 FF 20",
		ascii: "A.. ",
		kind:  "HEX",
		len:   4,
	}
	if got != want {
		t.Fatalf("packet display fields = %#v, want %#v", got, want)
	}
}

func TestPacketModelPreservesEvidence(t *testing.T) {
	p := wincore.Packet{ID: "packet-1", SessionKey: "session-2", Timestamp: time.Unix(123, 0), Direction: "TX", Transport: "TCP", ConnectionID: "conn-3", Endpoint: "127.0.0.1:9000", Source: "SERIAL→NET", Leg: "network", Data: []byte{0, 255, 65}}
	got := packetModel(p)
	if got["id"] != p.ID || got["connection_id"] != p.ConnectionID || got["session_key"] != p.SessionKey || got["source"] != p.Source || got["hex"] != "00 FF 41" || got["rawLen"] != 3 || got["epoch"] != float64(123) {
		t.Fatalf("lost packet evidence: %#v", got)
	}
	if got["status"] != "未分析" {
		t.Fatalf("unverified packet status: %#v", got)
	}
}

func TestTruncateUTF8KeepsWholeCharacters(t *testing.T) {
	s := "报告ABC" // 报、告各 3 字节
	for n, want := range map[int]string{0: "", 1: "", 3: "报", 4: "报", 5: "报", 6: "报告", 7: "报告A", 100: s} {
		if got := truncateUTF8(s, n); got != want || !utf8.ValidString(got) {
			t.Errorf("truncateUTF8(%q, %d) = %q, want %q", s, n, got, want)
		}
	}
}
