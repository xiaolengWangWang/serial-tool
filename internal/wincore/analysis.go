package wincore

import (
	"fmt"
	"strings"
)

// AnalyzeHexPacket returns a small, deterministic report for a captured HEX frame.
// It is intentionally local so analysis works offline and never sends packet data out.
func AnalyzeHexPacket(input string) string {
	return AnalyzeTransportPacket("", input)
}

// AnalyzeTransportPacket adds TCP/UDP context while keeping application payload analysis local.
func AnalyzeTransportPacket(transport, input string) string {
	data, err := ParseData(input, true, "无")
	if err != nil {
		return "分析失败：" + err.Error()
	}
	printable := 0
	for _, b := range data {
		if b >= 0x20 && b <= 0x7e {
			printable++
		}
	}
	var out strings.Builder
	if transport == "TCP" || transport == "UDP" {
		fmt.Fprintf(&out, "%s 报文分析（应用层负载）\n", transport)
	} else {
		out.WriteString("本地报文分析\n")
	}
	fmt.Fprintf(&out, "长度：%d 字节\n可打印 ASCII：%d/%d\n", len(data), printable, len(data))
	if len(data) == 0 {
		return out.String()
	}
	isIPTransport := false
	if len(data) >= 20 && data[0]>>4 == 4 {
		iHL := int(data[0]&0x0f) * 4
		if iHL >= 20 && iHL <= len(data) && (data[9] == 6 || data[9] == 17) {
			isIPTransport = true
			proto := "TCP"
			header := 20
			if data[9] == 17 {
				proto, header = "UDP", 8
			}
			if len(data) >= iHL+header {
				fmt.Fprintf(&out, "IPv4 传输协议：%s\n源端口：%d\n目的端口：%d\nIP 头长度：%d 字节\n",
					proto, uint16(data[iHL])<<8|uint16(data[iHL+1]), uint16(data[iHL+2])<<8|uint16(data[iHL+3]), iHL)
			}
		}
	}
	fmt.Fprintf(&out, "首字节：0x%02X\n", data[0])
	if !isIPTransport {
		appendModbusReport(&out, transport, data)
	}
	return strings.TrimRight(out.String(), "\n")
}

func appendModbusReport(out *strings.Builder, transport string, data []byte) {
	offset, tcp := 1, false
	if transport == "TCP" && len(data) >= 8 && data[2] == 0 && data[3] == 0 {
		length := int(data[4])<<8 | int(data[5])
		if length == len(data)-6 && length >= 2 {
			tcp, offset = true, 6
			fmt.Fprintf(out, "Modbus TCP：事务标识 0x%04X，协议标识 0x%04X，单元号 %d\n", uint16(data[0])<<8|uint16(data[1]), uint16(data[2])<<8|uint16(data[3]), data[6])
			offset++
		}
	}
	if !tcp {
		if len(data) < 4 || data[0] == 0 || data[0] > 247 {
			return
		}
		fmt.Fprintf(out, "Modbus RTU：从站地址 %d\n", data[0])
	}
	if offset >= len(data) {
		return
	}
	function := data[offset]
	name := map[byte]string{1: "读线圈", 2: "读离散输入", 3: "读保持寄存器", 4: "读输入寄存器", 5: "写单线圈", 6: "写单寄存器", 15: "写多线圈", 16: "写多寄存器"}[function&0x7f]
	if name == "" {
		name = "未知"
	}
	if function&0x80 != 0 {
		name += "（异常响应）"
	}
	fmt.Fprintf(out, "Modbus 功能码：0x%02X %s\n", function, name)
	if function&0x80 != 0 && offset+1 < len(data) {
		fmt.Fprintf(out, "异常码：0x%02X\n", data[offset+1])
	} else if offset+4 < len(data) && function&0x7f <= 6 {
		fmt.Fprintf(out, "起始地址：%d，数量/值：%d\n", uint16(data[offset+1])<<8|uint16(data[offset+2]), uint16(data[offset+3])<<8|uint16(data[offset+4]))
	} else if offset+1 < len(data) {
		fmt.Fprintf(out, "数据字节数：%d\n", data[offset+1])
	}
	if !tcp {
		if len(data) < 4 {
			return
		}
		want := CRC16Modbus(data[:len(data)-2])
		got := uint16(data[len(data)-2]) | uint16(data[len(data)-1])<<8
		if want == got {
			fmt.Fprintf(out, "Modbus CRC：通过（0x%04X）\n", got)
		} else {
			fmt.Fprintf(out, "Modbus CRC：不匹配（收到 0x%04X，计算 0x%04X）\n", got, want)
		}
	}
}
