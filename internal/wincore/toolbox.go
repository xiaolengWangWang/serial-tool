package wincore

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// 工具箱:CRC / 校验和 / HEX 转换等纯函数,供两端 UI 的「工具箱」使用。

// HexToBytes 把 "01 03 00" 或 "010300" 形式的 HEX 字符串解析成字节。
func HexToBytes(s string) ([]byte, error) {
	clean := strings.Join(strings.Fields(s), "")
	return hex.DecodeString(clean)
}

// BytesToHex 把字节格式化成大写 HEX 字符串(每字节两位,无空格)。
func BytesToHex(data []byte) string {
	return strings.ToUpper(hex.EncodeToString(data))
}

// CRC16Modbus 计算 Modbus CRC16(初始 0xFFFF,多项式 0xA001,低字节在前)。
func CRC16Modbus(data []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc ^= uint16(b)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ 0xA001
			} else {
				crc >>= 1
			}
		}
	}
	return crc
}

// CRC16CCITT 计算 CRC16/CCITT-FALSE(初始 0xFFFF,多项式 0x1021)。
func CRC16CCITT(data []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc ^= uint16(b) << 8
		for i := 0; i < 8; i++ {
			if crc&0x8000 != 0 {
				crc = (crc << 1) ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

// CRC32 计算标准 CRC32(多项式 0xEDB88320)。
func CRC32(data []byte) uint32 {
	crc := ^uint32(0)
	for _, b := range data {
		crc ^= uint32(b)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ 0xEDB88320
			} else {
				crc >>= 1
			}
		}
	}
	return ^crc
}

// XORChecksum 计算 XOR 校验和。
func XORChecksum(data []byte) byte {
	var x byte
	for _, b := range data {
		x ^= b
	}
	return x
}

// SUMChecksum 计算累加校验和(取低 8 位)。
func SUMChecksum(data []byte) byte {
	var s byte
	for _, b := range data {
		s += b
	}
	return s
}

// Base64Encode 将字节编码为 Base64 字符串。
func Base64Encode(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// Base64Decode 将 Base64 字符串解码为字节。
func Base64Decode(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(strings.TrimSpace(s))
}

// UnixToTime 将 Unix 时间戳转为可读的本地时间。13 位及以上按毫秒处理:
// 抓包、日志、接口里常见的是毫秒时间戳,按秒解释会得到几万年后的日期。
func UnixToTime(ts int64) string {
	if ts >= 1e12 || ts <= -1e12 {
		return time.UnixMilli(ts).Format("2006-01-02 15:04:05.000 MST") + "（按毫秒）"
	}
	return time.Unix(ts, 0).Format("2006-01-02 15:04:05 MST") + "（按秒）"
}

// HexToText 把 HEX 按 UTF-8 还原成文本。控制字符和无效字节用转义显示
// (\r \n \t \xHH),既能看出报文里的回车换行,也不会把乱码直接吐出来。
func HexToText(data []byte) string {
	var b strings.Builder
	for len(data) > 0 {
		r, size := utf8.DecodeRune(data)
		switch {
		case r == utf8.RuneError && size <= 1:
			fmt.Fprintf(&b, "\\x%02X", data[0])
		case r == '\r':
			b.WriteString("\\r")
		case r == '\n':
			b.WriteString("\\n")
		case r == '\t':
			b.WriteString("\\t")
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, "\\x%02X", r)
		default:
			b.WriteRune(r)
		}
		data = data[size:]
	}
	return b.String()
}

// hexToDecimal 把 HEX 按大端、小端解释成无符号整数,并列出逐字节十进制。
func hexToDecimal(data []byte) string {
	if len(data) == 0 {
		return "输入为空"
	}
	parts := make([]string, len(data))
	for i, v := range data {
		parts[i] = strconv.Itoa(int(v))
	}
	perByte := "逐字节：" + strings.Join(parts, " ")
	if len(data) > 8 {
		return perByte + "（超过 8 字节，不按整数解释）"
	}
	be := new(big.Int).SetBytes(data)
	le := make([]byte, len(data))
	for i, v := range data {
		le[len(data)-1-i] = v
	}
	out := fmt.Sprintf("大端：%s；小端：%s", be.String(), new(big.Int).SetBytes(le).String())
	if n := len(data); n == 1 || n == 2 || n == 4 || n == 8 {
		// 有符号只对标准宽度有意义,补码按同宽度解释。
		out += fmt.Sprintf("；有符号 大端：%d；小端：%d", signedOf(data), signedOf(le))
	}
	return out + "\n" + perByte
}

func signedOf(b []byte) int64 {
	switch len(b) {
	case 1:
		return int64(int8(b[0]))
	case 2:
		return int64(int16(binary.BigEndian.Uint16(b)))
	case 4:
		return int64(int32(binary.BigEndian.Uint32(b)))
	default:
		return int64(binary.BigEndian.Uint64(b))
	}
}

// decimalToHex 把十进制整数转成大端/小端 HEX,宽度取能容纳它的最小标准宽度
// (1/2/4/8 字节);负数按该宽度的补码。
func decimalToHex(input string) string {
	s := strings.TrimSpace(input)
	var raw uint64
	width := 8
	if strings.HasPrefix(s, "-") {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return "无效的十进制整数（范围 -9223372036854775808 ~ 18446744073709551615）"
		}
		for _, w := range []int{1, 2, 4, 8} {
			if n >= -(int64(1) << (8*w - 1)) {
				width = w
				break
			}
		}
		raw = uint64(n)
	} else {
		n, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			return "无效的十进制整数（范围 -9223372036854775808 ~ 18446744073709551615）"
		}
		for _, w := range []int{1, 2, 4, 8} {
			if w == 8 || n < uint64(1)<<(8*w) {
				width = w
				break
			}
		}
		raw = n
	}
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, raw)
	be := buf[8-width:]
	le := make([]byte, width)
	for i, v := range be {
		le[width-1-i] = v
	}
	return fmt.Sprintf("0x%X（%d 字节）\n大端：% X\n小端：% X", be, width, be, le)
}

// ParseToolbox 执行工具箱操作,返回结果字符串。kind 支持 modbus/crc16/crc32/xor/sum/
// base64enc/base64dec/unixtime/hex2text/text2hex/hex2dec/dec2hex。
func ParseToolbox(kind, input string) string {
	switch kind {
	case "text2hex":
		if input == "" {
			return "输入为空"
		}
		return fmt.Sprintf("% X", []byte(input))
	case "dec2hex":
		return decimalToHex(input)
	case "base64enc":
		return Base64Encode([]byte(input))
	case "base64dec":
		decoded, err := Base64Decode(input)
		if err != nil {
			return "Base64 解码失败: " + err.Error()
		}
		return fmt.Sprintf("% X", decoded)
	case "unixtime":
		var ts int64
		if _, err := fmt.Sscanf(strings.TrimSpace(input), "%d", &ts); err != nil {
			return "无效的 Unix 时间戳"
		}
		return UnixToTime(ts)
	}
	data, err := HexToBytes(input)
	if err != nil {
		return "输入不是有效的 HEX"
	}
	switch kind {
	case "modbus":
		c := CRC16Modbus(data)
		return fmt.Sprintf("0x%04X (低字节在前: %02X %02X)", c, byte(c), byte(c>>8))
	case "crc16":
		return fmt.Sprintf("0x%04X", CRC16CCITT(data))
	case "crc32":
		return fmt.Sprintf("0x%08X", CRC32(data))
	case "xor":
		return fmt.Sprintf("0x%02X", XORChecksum(data))
	case "sum":
		return fmt.Sprintf("0x%02X", SUMChecksum(data))
	case "hex2text":
		return HexToText(data)
	case "hex2dec":
		return hexToDecimal(data)
	}
	return "未知操作"
}
