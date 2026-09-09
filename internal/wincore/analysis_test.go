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

func TestAnalyzeHexPacketInvalidInput(t *testing.T) {
	if !strings.HasPrefix(AnalyzeHexPacket("GG"), "分析失败：") {
		t.Fatal("invalid HEX should return an analysis error")
	}
}
