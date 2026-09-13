//go:build windows

package main

import (
	"strings"
	"testing"
	"time"

	"serial-tool/internal/wincore"
)

func TestPacketSnapshotPreservesMetadataAndBytes(t *testing.T) {
	raw := wincore.Packet{ID: "packet-1", Timestamp: time.Unix(123, 456000000), Direction: "RX", Transport: "TCP", ConnectionID: "peer-1", Endpoint: "127.0.0.1:9000", Source: "source", Data: []byte{'A', 0, 255}}
	p := packetFromCore(raw)
	raw.Data[0] = 'Z'
	if p.Hex != "41 00 FF" || p.ASCII != "A.." || p.Raw.Data[0] != 'A' || p.Raw.ConnectionID != "peer-1" || !p.TS.Equal(raw.Timestamp) {
		t.Fatalf("lost capture metadata: %+v", p)
	}
}

func TestPacketFiltersKeepSourceAndTimeConstraints(t *testing.T) {
	m := new(packetTableModel)
	now := time.Now()
	m.refilter("peer-1", "RX", now.Add(-time.Minute))
	m.add(packetFromCore(wincore.Packet{Timestamp: now.Add(-2 * time.Minute), Direction: "RX", ConnectionID: "peer-1", Data: []byte{1}}), "peer-1", "RX")
	m.add(packetFromCore(wincore.Packet{Timestamp: now, Direction: "RX", ConnectionID: "peer-2", Data: []byte{2}}), "peer-1", "RX")
	m.add(packetFromCore(wincore.Packet{Timestamp: now, Direction: "RX", ConnectionID: "peer-1", Data: []byte{3}}), "peer-1", "RX")
	if m.RowCount() != 1 || m.visible[0].Hex != "03" {
		t.Fatalf("unexpected filtered rows: %+v", m.visible)
	}
	if !strings.Contains(m.exportText(), "peer-1") {
		t.Fatal("export lost connection identity")
	}
}

func TestPacketRetentionAndInvalidRows(t *testing.T) {
	m := new(packetTableModel)
	for i := 0; i < 10010; i++ {
		m.add(Packet{Direction: "RX", TS: time.Now()}, "", "全部")
	}
	if len(m.all) > 10000 || len(m.visible) > len(m.all) {
		t.Fatalf("retention inconsistent: %d/%d", len(m.all), len(m.visible))
	}
	if m.Value(-1, 0) != "" || m.Value(m.RowCount(), 0) != "" {
		t.Fatal("invalid row access")
	}
}
