package wincore

import (
	"strings"
	"testing"
)

func TestAnalyzeHexPacketModbusCRC(t *testing.T) {
	report := AnalyzeHexPacket("01 03 00 00 00 0A C5 CD")
	for _, want := range []string{"长度：8 字节", "读保持寄存器", "CRC：通过"} {
		if !strings.Contains(report, want) {
			t.Fatalf("report %q does not contain %q", report, want)
		}
	}
}

func TestAnalyzeModbusTCP(t *testing.T) {
	report := AnalyzeTransportPacket("TCP", "00 01 00 00 00 06 01 03 00 00 00 0A")
	for _, want := range []string{"Modbus TCP", "单元号 1", "读保持寄存器", "起始地址：0，数量/值：10"} {
		if !strings.Contains(report, want) {
			t.Fatalf("report %q does not contain %q", report, want)
		}
	}
}

func TestAnalyzeDataTypes(t *testing.T) {
	report := AnalyzeHexPacket("43 48 00 00")
	for _, want := range []string{"UInt16 BE=17224", "UInt32/Float32 候选", "ABCD：UInt32=1128792064", "Float32=200"} {
		if !strings.Contains(report, want) {
			t.Fatalf("report %q does not contain %q", report, want)
		}
	}
}

func TestAnalyzeHexPacketInvalidInput(t *testing.T) {
	if !strings.HasPrefix(AnalyzeHexPacket("GG"), "分析失败：") {
		t.Fatal("invalid HEX should return an analysis error")
	}
}

func TestAnalyzeTransportPacketTCPUDP(t *testing.T) {
	tcp := AnalyzeTransportPacket("TCP", "45 00 00 28 00 00 00 00 40 06 00 00 7F 00 00 01 7F 00 00 01 1F 90 23 28 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00")
	if !strings.Contains(tcp, "TCP 报文分析") || !strings.Contains(tcp, "源端口：8080") || !strings.Contains(tcp, "目的端口：9000") {
		t.Fatalf("TCP report = %q", tcp)
	}
	udp := AnalyzeTransportPacket("UDP", "45 00 00 1C 00 00 00 00 40 11 00 00 7F 00 00 01 7F 00 00 01 04 D2 16 2E 00 08 00 00")
	if !strings.Contains(udp, "UDP 报文分析") || !strings.Contains(udp, "源端口：1234") || !strings.Contains(udp, "目的端口：5678") {
		t.Fatalf("UDP report = %q", udp)
	}
}
