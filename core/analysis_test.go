package core

import (
	"fmt"
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

func TestModbusSummary(t *testing.T) {
	for _, c := range []struct {
		name, transport, hex, want string
	}{
		{"RTU 请求", "SERIAL", "01 03 00 00 00 0A C5 CD", "Modbus RTU 从站 1 功能码 0x03 读保持寄存器 地址 0 数量/值 10，CRC 通过"},
		{"RTU 响应", "SERIAL", "01 03 08 00 64 01 F4 FF 9C 00 0A 70 28", "Modbus RTU 从站 1 功能码 0x03 读保持寄存器 数据字节数 8 寄存器值 100、500、65436、10，CRC 通过"},
		{"RTU 单寄存器响应", "SERIAL", "01 03 02 00 64 B9 AF", "读保持寄存器 数据字节数 2 寄存器值 100，CRC 通过"},
		{"RTU CRC 被改坏", "SERIAL", "01 03 08 00 67 01 F4 FF 9C 00 0A 43 D7", "数据字节数 8 寄存器值 103、500、65436、10，CRC 不匹配（收到 0xD743，应为 0x2843）"},
		{"RTU 异常响应", "SERIAL", "01 83 02 C0 F1", "功能码 0x83 读保持寄存器（异常响应）异常码 0x02 非法数据地址，CRC 通过"},
		{"Modbus TCP", "TCP", "00 01 00 00 00 06 01 03 00 00 00 0A", "Modbus TCP 事务 0x0001 单元 1 功能码 0x03 读保持寄存器 地址 0 数量/值 10"},
		{"Modbus TCP 响应", "TCP", "00 01 00 00 00 05 01 03 02 00 64", "Modbus TCP 事务 0x0001 单元 1 功能码 0x03 读保持寄存器 数据字节数 2 寄存器值 100"},
		{"串口服务器透传：TCP 上的 RTU 帧", "TCP", "01 03 02 00 64 B9 AF", "Modbus RTU 从站 1 功能码 0x03 读保持寄存器 数据字节数 2 寄存器值 100，CRC 通过"},
		{"TCP 上 CRC 不对的数据不算 RTU", "TCP", "01 03 02 00 64 00 00", ""},
		{"普通文本不算 Modbus", "SERIAL", "48 65 6C 6C 6F 0D 0A", ""},
		{"功能码对但长度对不上且 CRC 错", "SERIAL", "01 03 00 00 00 0A 12 34 56", ""},
		{"TCP 长度字段不符", "TCP", "00 01 00 00 00 09 01 03 00 00 00 0A", ""},
	} {
		data, err := ParseData(c.hex, true, "无")
		if err != nil {
			t.Fatal(err)
		}
		got := ModbusSummary(c.transport, data)
		if c.want == "" && got != "" || c.want != "" && !strings.Contains(got, c.want) {
			t.Errorf("%s: ModbusSummary = %q, want contains %q", c.name, got, c.want)
		}
	}
}

func TestAnalyzeModbusTCP(t *testing.T) {
	report := AnalyzeTransportPacket("TCP", "00 01 00 00 00 06 01 03 00 00 00 0A")
	for _, want := range []string{"Modbus TCP", "单元号 1", "读保持寄存器（请求）", "起始地址：0，数量：10"} {
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

// 同一帧反复分析,报告必须逐字相同,字节序候选按 ABCD/BADC/CDAB/DCBA 排列。
func TestAnalyzeDataTypesDeterministic(t *testing.T) {
	first := AnalyzeHexPacket("41 20 00 00 3F 80")
	for i := 0; i < 50; i++ {
		if got := AnalyzeHexPacket("41 20 00 00 3F 80"); got != first {
			t.Fatalf("report changed between runs:\n%s\n---\n%s", first, got)
		}
	}
	last := -1
	for _, name := range []string{"ABCD：", "BADC：", "CDAB：", "DCBA："} {
		i := strings.Index(first, name)
		if i < 0 || i < last {
			t.Fatalf("byte orders out of order in:\n%s", first)
		}
		last = i
	}
}

// 读寄存器的应答不能按请求拆：01 03 02 00 64 B9 AF 是 1 个寄存器、值 100，
// 不是「起始地址 512，数量 25785」；数据类型候选取寄存器数据，不取帧头 01 03。
func TestAnalyzeModbusReadResponse(t *testing.T) {
	report := AnalyzeTransportPacket("SERIAL", "01 03 02 00 64 B9 AF")
	for _, want := range []string{"读保持寄存器（应答）", "数据字节数：2", "寄存器值：100（0x0064）", "CRC：通过（0xAFB9）", "UInt16 BE=100，LE=25600"} {
		if !strings.Contains(report, want) {
			t.Fatalf("report %q does not contain %q", report, want)
		}
	}
	if strings.Contains(report, "起始地址") || strings.Contains(report, "UInt16 BE=259") {
		t.Fatalf("response parsed as a request:\n%s", report)
	}
	tcp := AnalyzeTransportPacket("TCP", "00 01 00 00 00 05 01 03 02 00 64")
	if !strings.Contains(tcp, "寄存器值：100（0x0064）") || !strings.Contains(tcp, "UInt16 BE=100") {
		t.Fatalf("Modbus TCP response report = %q", tcp)
	}
}

// 请求帧没有数据，不给数据类型候选；帧头字节拼出来的数字只会误导。
func TestAnalyzeModbusRequestHasNoDataTypes(t *testing.T) {
	report := AnalyzeTransportPacket("SERIAL", "01 03 00 00 00 01 84 0A")
	if !strings.Contains(report, "起始地址：0，数量：1") || strings.Contains(report, "数据类型候选") {
		t.Fatalf("request report = %q", report)
	}
}

func TestAnalyzeModbusWriteMultiple(t *testing.T) {
	frame := []byte{1, 0x10, 0, 1, 0, 2, 4, 0, 0x0A, 1, 2}
	crc := CRC16Modbus(frame)
	frame = append(frame, byte(crc), byte(crc>>8))
	if got := ModbusSummary("SERIAL", frame); !strings.Contains(got, "写多寄存器 地址 1 数量/值 2，CRC 通过") {
		t.Fatalf("summary = %q", got)
	}
	report := AnalyzeTransportPacket("SERIAL", fmt.Sprintf("% X", frame))
	if !strings.Contains(report, "起始地址：1，数量：2，数据字节数：4") || !strings.Contains(report, "UInt16 BE=10") {
		t.Fatalf("write multiple report = %q", report)
	}
}

func TestModbusException(t *testing.T) {
	for _, c := range []struct{ name, transport, hex, want string }{
		{"RTU 异常应答", "SERIAL", "01 83 02 C0 F1", "异常应答 02：非法数据地址"},
		{"TCP 异常应答", "TCP", "00 01 00 00 00 03 01 83 02", "异常应答 02：非法数据地址"},
		{"未知异常码", "TCP", "00 01 00 00 00 03 01 83 07", "异常应答 07：未知异常"},
		{"CRC 不对不标注", "SERIAL", "01 83 02 00 00", ""},
		{"正常应答不标注", "SERIAL", "01 03 02 00 64 B9 AF", ""},
		{"普通数据不标注", "UDP", "48 65 6C 6C 6F", ""},
	} {
		data, err := ParseData(c.hex, true, "无")
		if err != nil {
			t.Fatal(err)
		}
		if got := ModbusException(c.transport, data); got != c.want {
			t.Errorf("%s: ModbusException = %q, want %q", c.name, got, c.want)
		}
	}
}
