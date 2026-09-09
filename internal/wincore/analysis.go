package wincore

import (
	"fmt"
	"strings"
)

// AnalyzeHexPacket returns a small, deterministic report for a captured HEX frame.
// It is intentionally local so analysis works offline and never sends packet data out.
func AnalyzeHexPacket(input string) string {
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
	fmt.Fprintf(&out, "本地报文分析\n长度：%d 字节\n可打印 ASCII：%d/%d\n", len(data), printable, len(data))
	if len(data) == 0 {
		return out.String()
	}
	fmt.Fprintf(&out, "首字节：0x%02X\n", data[0])
	if len(data) >= 4 {
		name := map[byte]string{1: "读线圈", 2: "读离散输入", 3: "读保持寄存器", 4: "读输入寄存器", 5: "写单线圈", 6: "写单寄存器", 15: "写多线圈", 16: "写多寄存器"}[data[1]&0x7f]
		if name == "" {
			name = "未知"
		}
		if data[1]&0x80 != 0 {
			name += "（异常响应）"
		}
		fmt.Fprintf(&out, "Modbus 功能码候选：0x%02X %s\n", data[1], name)
		want := CRC16Modbus(data[:len(data)-2])
		got := uint16(data[len(data)-2]) | uint16(data[len(data)-1])<<8
		if want == got {
			fmt.Fprintf(&out, "Modbus CRC：通过（0x%04X）\n", got)
		} else {
			fmt.Fprintf(&out, "Modbus CRC：不匹配（收到 0x%04X，计算 0x%04X）\n", got, want)
		}
	}
	return strings.TrimRight(out.String(), "\n")
}
