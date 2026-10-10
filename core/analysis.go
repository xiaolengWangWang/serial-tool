package core

import (
	"encoding/binary"
	"fmt"
	"math"
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
	values := data
	if !isIPTransport {
		if f, ok := parseModbus(transport, data); ok {
			appendModbusReport(&out, f)
			// Modbus 帧只解释其中的数据部分；请求帧没有数据，帧头字节拼出来的数字没有意义。
			values = f.values()
		}
	}
	appendDataTypeReport(&out, values)
	return strings.TrimRight(out.String(), "\n")
}

func appendDataTypeReport(out *strings.Builder, data []byte) {
	if len(data) >= 2 {
		fmt.Fprintf(out, "数据类型候选：UInt16 BE=%d，LE=%d\n", binary.BigEndian.Uint16(data[:2]), binary.LittleEndian.Uint16(data[:2]))
	}
	if len(data) < 4 {
		return
	}
	// 用切片固定顺序:遍历 map 顺序随机,同一帧每次分析的输出会不一样。
	orders := []struct {
		name  string
		bytes []byte
	}{
		{"ABCD", data[:4]}, {"BADC", []byte{data[1], data[0], data[3], data[2]}},
		{"CDAB", []byte{data[2], data[3], data[0], data[1]}}, {"DCBA", []byte{data[3], data[2], data[1], data[0]}},
	}
	fmt.Fprintf(out, "UInt32/Float32 候选：\n")
	for _, o := range orders {
		bits := binary.BigEndian.Uint32(o.bytes)
		value := math.Float32frombits(bits)
		if !math.IsNaN(float64(value)) && !math.IsInf(float64(value), 0) {
			fmt.Fprintf(out, "  %s：UInt32=%d，Float32=%g\n", o.name, bits, value)
		}
	}
}

// modbusFunctionNames 是常用功能码的中文名，本地报告与 ModbusSummary 共用。
var modbusFunctionNames = map[byte]string{1: "读线圈", 2: "读离散输入", 3: "读保持寄存器", 4: "读输入寄存器", 5: "写单线圈", 6: "写单寄存器", 15: "写多线圈", 16: "写多寄存器"}

// modbusExceptionNames 是异常应答里异常码的含义。
var modbusExceptionNames = map[byte]string{1: "非法功能码", 2: "非法数据地址", 3: "非法数据值", 4: "从站设备故障", 5: "确认（正在处理）", 6: "从站设备忙", 8: "存储奇偶校验错误", 0x0A: "网关路径不可用", 0x0B: "网关目标设备未响应"}

func modbusExceptionName(code byte) string {
	if name, ok := modbusExceptionNames[code]; ok {
		return name
	}
	return "未知异常"
}

// modbusFrame 是按 Modbus RTU 或 Modbus TCP 拆开的一帧。本地报告、交给 AI 的摘要和
// 数据表的异常标注都从这里取结论，同一帧在三处的解读保持一致。
type modbusFrame struct {
	tcp             bool
	transaction     uint16 // 仅 Modbus TCP
	unit            byte   // RTU 从站地址或 TCP 单元号
	function        byte
	body            []byte // 功能码之后、CRC 之前的字节
	crcGot, crcWant uint16 // 仅 RTU
}

func (f modbusFrame) crcOK() bool { return f.tcp || f.crcGot == f.crcWant }

// modbusShape 是帧体的几种结构。读功能码（1–4）的请求固定是「起始地址 + 数量」4 字节，
// 应答是「字节数 + 数据」；写多个（15、16）反过来，请求带数据，应答是 4 字节。
type modbusShape int

const (
	shapeUnknown   modbusShape = iota
	shapeException             // 异常应答：异常码
	shapeRead                  // 读请求：起始地址、数量
	shapeReadData              // 读应答：字节数、数据
	shapeWrite                 // 写单个（请求与应答相同）：地址、值
	shapeWriteAck              // 写多个应答：起始地址、数量
	shapeWriteData             // 写多个请求：起始地址、数量、字节数、数据
)

func (f modbusFrame) shape() modbusShape {
	b := f.body
	switch fc := f.function; {
	case fc&0x80 != 0:
		if len(b) >= 1 {
			return shapeException
		}
	case fc >= 1 && fc <= 4:
		// 4 字节既可能是读请求，也可能是 3 字节数据的线圈应答；寄存器应答的字节数总是偶数。
		if len(b) == 4 && (fc >= 3 || b[0] != 3) {
			return shapeRead
		}
		if len(b) >= 1 && len(b) == 1+int(b[0]) {
			return shapeReadData
		}
	case fc == 5 || fc == 6:
		if len(b) == 4 {
			return shapeWrite
		}
	case fc == 15 || fc == 16:
		if len(b) == 4 {
			return shapeWriteAck
		}
		if len(b) >= 5 && len(b) == 5+int(b[4]) {
			return shapeWriteData
		}
	}
	return shapeUnknown
}

func (f modbusFrame) address() uint16  { return binary.BigEndian.Uint16(f.body[0:2]) }
func (f modbusFrame) quantity() uint16 { return binary.BigEndian.Uint16(f.body[2:4]) }

// values 返回帧里承载的数据：读应答的数据、写单寄存器的值、写多个的数据。
// 请求和确认帧没有数据，返回 nil。
func (f modbusFrame) values() []byte {
	switch f.shape() {
	case shapeReadData:
		return f.body[1:]
	case shapeWrite:
		if f.function == 6 {
			return f.body[2:4]
		}
	case shapeWriteData:
		return f.body[5:]
	}
	return nil
}

// registers 把读寄存器应答的数据按大端拆成 16 位寄存器值。
func (f modbusFrame) registers() []uint16 {
	if fc := f.function; fc != 3 && fc != 4 || f.shape() != shapeReadData {
		return nil
	}
	data := f.body[1:]
	regs := make([]uint16, 0, len(data)/2)
	for i := 0; i+1 < len(data); i += 2 {
		regs = append(regs, binary.BigEndian.Uint16(data[i:]))
	}
	return regs
}

// parseModbus 只认像 Modbus 的帧：功能码已知；TCP 的 MBAP 头与长度吻合；RTU 的 CRC 通过，
// 或在串口上帧长与功能码吻合（CRC 不对仍认作 Modbus，便于发现算错的校验码）。
// 串口服务器透传时 TCP / UDP 上跑的是 RTU 帧，MBAP 不成立再按 RTU 试，但这时要求 CRC 通过，
// 免得把任意网络数据认成 CRC 错误的 Modbus 帧。
func parseModbus(transport string, data []byte) (modbusFrame, bool) {
	network := transport == "TCP" || transport == "UDP"
	if network && len(data) >= 8 && data[2] == 0 && data[3] == 0 && int(binary.BigEndian.Uint16(data[4:])) == len(data)-6 {
		if _, ok := modbusFunctionNames[data[7]&0x7f]; ok {
			return modbusFrame{tcp: true, transaction: binary.BigEndian.Uint16(data), unit: data[6], function: data[7], body: data[8:]}, true
		}
	}
	if len(data) < 5 || data[0] == 0 || data[0] > 247 {
		return modbusFrame{}, false
	}
	if _, ok := modbusFunctionNames[data[1]&0x7f]; !ok {
		return modbusFrame{}, false
	}
	f := modbusFrame{unit: data[0], function: data[1], body: data[2 : len(data)-2],
		crcGot: uint16(data[len(data)-2]) | uint16(data[len(data)-1])<<8, crcWant: CRC16Modbus(data[:len(data)-2])}
	if !f.crcOK() && (network || !modbusLengthFits(f.function, data)) {
		return modbusFrame{}, false
	}
	return f, true
}

// ModbusSummary 把一帧按 Modbus 解析成一行结论，随报文一起交给 AI：CRC、功能码这类
// 要精确计算的事实由程序给出，模型自己算 CRC 并不可靠（实测把改坏 CRC 的帧判成正常）。
// 不像 Modbus 的数据返回空串，免得把普通文本也标成「CRC 不匹配的 Modbus 帧」误导分析。
func ModbusSummary(transport string, data []byte) string {
	f, ok := parseModbus(transport, data)
	if !ok {
		return ""
	}
	name := modbusFunctionNames[f.function&0x7f]
	var s string
	if f.tcp {
		s = fmt.Sprintf("Modbus TCP 事务 0x%04X 单元 %d 功能码 0x%02X %s", f.transaction, f.unit, f.function, name)
	} else {
		s = fmt.Sprintf("Modbus RTU 从站 %d 功能码 0x%02X %s", f.unit, f.function, name)
	}
	switch f.shape() {
	case shapeException:
		s += fmt.Sprintf("（异常响应）异常码 0x%02X %s", f.body[0], modbusExceptionName(f.body[0]))
	case shapeRead, shapeWrite, shapeWriteAck, shapeWriteData:
		s += fmt.Sprintf(" 地址 %d 数量/值 %d", f.address(), f.quantity())
	case shapeReadData:
		s += fmt.Sprintf(" 数据字节数 %d", f.body[0])
		if regs := f.registers(); len(regs) > 0 {
			s += " 寄存器值 " + joinRegisters(regs, 8, "%d")
		}
	}
	if f.tcp {
		return s
	}
	if f.crcOK() {
		return s + "，CRC 通过"
	}
	return s + fmt.Sprintf("，CRC 不匹配（收到 0x%04X，应为 0x%04X）", f.crcGot, f.crcWant)
}

// ModbusException 在数据是 Modbus 异常应答时返回一句说明，如「异常应答 02：非法数据地址」；
// 否则返回空串。只认 CRC 通过的 RTU 帧和 MBAP 完整的 TCP 帧，数据表据此标注，不能误报。
func ModbusException(transport string, data []byte) string {
	f, ok := parseModbus(transport, data)
	if !ok || !f.crcOK() || f.shape() != shapeException {
		return ""
	}
	return fmt.Sprintf("异常应答 %02X：%s", f.body[0], modbusExceptionName(f.body[0]))
}

// modbusLengthFits 判断 RTU 帧长与功能码是否吻合，CRC 不对时据此区分
// 「传坏了的 Modbus 帧」与「根本不是 Modbus」。
func modbusLengthFits(function byte, data []byte) bool {
	n := len(data)
	switch {
	case function&0x80 != 0:
		return n == 5
	case function >= 1 && function <= 6:
		return n == 8 || function <= 4 && n == 5+int(data[2])
	case function == 15 || function == 16:
		return n == 8 || n >= 9 && n == 9+int(data[6])
	}
	return false
}

func joinRegisters(regs []uint16, limit int, format string) string {
	parts := make([]string, 0, limit+1)
	for i, r := range regs {
		if i == limit {
			parts = append(parts, fmt.Sprintf("…共 %d 个", len(regs)))
			break
		}
		parts = append(parts, fmt.Sprintf(format, r))
	}
	return strings.Join(parts, "、")
}

func appendModbusReport(out *strings.Builder, f modbusFrame) {
	if f.tcp {
		fmt.Fprintf(out, "Modbus TCP：事务标识 0x%04X，协议标识 0x0000，单元号 %d\n", f.transaction, f.unit)
	} else {
		fmt.Fprintf(out, "Modbus RTU：从站地址 %d\n", f.unit)
	}
	name := modbusFunctionNames[f.function&0x7f]
	switch f.shape() {
	case shapeException:
		fmt.Fprintf(out, "Modbus 功能码：0x%02X %s（异常响应）\n", f.function, name)
		fmt.Fprintf(out, "异常码：0x%02X %s\n", f.body[0], modbusExceptionName(f.body[0]))
	case shapeRead:
		fmt.Fprintf(out, "Modbus 功能码：0x%02X %s（请求）\n", f.function, name)
		fmt.Fprintf(out, "起始地址：%d，数量：%d\n", f.address(), f.quantity())
	case shapeReadData:
		fmt.Fprintf(out, "Modbus 功能码：0x%02X %s（应答）\n", f.function, name)
		fmt.Fprintf(out, "数据字节数：%d\n", f.body[0])
		if regs := f.registers(); len(regs) > 0 {
			fmt.Fprintf(out, "寄存器值：%s\n", joinRegisters(regs, 16, "%[1]d（0x%04[1]X）"))
		} else if len(f.body) > 1 {
			fmt.Fprintf(out, "数据：% X\n", f.body[1:])
		}
	case shapeWrite:
		fmt.Fprintf(out, "Modbus 功能码：0x%02X %s\n", f.function, name)
		fmt.Fprintf(out, "地址：%d，写入值：%d（0x%04X）\n", f.address(), f.quantity(), f.quantity())
	case shapeWriteAck:
		fmt.Fprintf(out, "Modbus 功能码：0x%02X %s（应答）\n", f.function, name)
		fmt.Fprintf(out, "起始地址：%d，数量：%d\n", f.address(), f.quantity())
	case shapeWriteData:
		fmt.Fprintf(out, "Modbus 功能码：0x%02X %s（请求）\n", f.function, name)
		fmt.Fprintf(out, "起始地址：%d，数量：%d，数据字节数：%d\n", f.address(), f.quantity(), f.body[4])
	default:
		fmt.Fprintf(out, "Modbus 功能码：0x%02X %s\n", f.function, name)
		fmt.Fprintf(out, "帧长与功能码对不上，无法拆分字段\n")
	}
	if f.tcp {
		return
	}
	if f.crcOK() {
		fmt.Fprintf(out, "Modbus CRC：通过（0x%04X）\n", f.crcGot)
	} else {
		fmt.Fprintf(out, "Modbus CRC：不匹配（收到 0x%04X，计算 0x%04X）\n", f.crcGot, f.crcWant)
	}
}
