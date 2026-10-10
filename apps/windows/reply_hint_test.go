//go:build windows

package main

import (
	"strings"
	"testing"
	"time"

	"serial-tool/core"
)

func TestNoReplyHintOnlyForRequestResponseModes(t *testing.T) {
	for mode, want := range map[core.Mode]string{
		core.ModeSerial:       "A/B 线",
		core.ModeTCPClient:    "设备地址",
		core.ModeUDPClient:    "UDP",
		core.ModeTCPServer:    "",
		core.ModeUDPServer:    "",
		core.ModeSerialServer: "",
		core.ModeHTTPClient:   "",
	} {
		got := noReplyHint(mode)
		if want == "" && got != "" || want != "" && !strings.Contains(got, want) {
			t.Errorf("%s: noReplyHint = %q, want contains %q", mode, got, want)
		}
	}
}

// Modbus 异常应答在数据表 HEX 列的字节后显示含义，导出与复制仍用原始字节。
func TestPacketNotesModbusException(t *testing.T) {
	m := new(packetTableModel)
	ex := packetFromCore(core.Packet{Timestamp: time.Now(), Direction: "RX", Transport: "SERIAL", Data: []byte{0x01, 0x83, 0x02, 0xC0, 0xF1}})
	ok := packetFromCore(core.Packet{Timestamp: time.Now(), Direction: "RX", Transport: "SERIAL", Data: []byte{0x01, 0x03, 0x02, 0x00, 0x64, 0xB9, 0xAF}})
	ev := packetFromCore(core.Packet{Timestamp: time.Now(), Direction: "EVENT", Transport: "SERIAL", Data: []byte{0x01, 0x83, 0x02, 0xC0, 0xF1}})
	m.add(ex, "", dirAll)
	m.add(ok, "", dirAll)
	if ex.Note != "异常应答 02：非法数据地址" || m.Value(0, 2) != "01 83 02 C0 F1 → "+ex.Note || ex.Hex != "01 83 02 C0 F1" || m.Value(0, 3) != "....." {
		t.Fatalf("exception row: note=%q hex cell=%v ascii cell=%v", ex.Note, m.Value(0, 2), m.Value(0, 3))
	}
	if ok.Note != "" || m.Value(1, 2) != ok.Hex || ev.Note != "" {
		t.Fatalf("normal rows must not be annotated: %q / %q", ok.Note, ev.Note)
	}
	m.refilter("非法数据地址", dirAll, time.Time{})
	if m.RowCount() != 1 {
		t.Fatalf("search by exception meaning matched %d rows", m.RowCount())
	}
}

func TestPortLabelAndName(t *testing.T) {
	label := portLabel(core.SerialPortInfo{Name: "COM5", Description: "USB-SERIAL CH340"})
	if label != "COM5  USB-SERIAL CH340" || portName(label) != "COM5" {
		t.Fatalf("label %q -> name %q", label, portName(label))
	}
	if portLabel(core.SerialPortInfo{Name: "COM7"}) != "COM7" || portName("  COM7 ") != "COM7" || portName("") != "" {
		t.Fatal("plain port names must round-trip")
	}
}
