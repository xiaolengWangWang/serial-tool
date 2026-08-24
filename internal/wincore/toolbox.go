package wincore

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
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

// UnixToTime 将 Unix 时间戳(秒)转为可读时间字符串。
func UnixToTime(ts int64) string {
	return time.Unix(ts, 0).Format("2006-01-02 15:04:05")
}

// ParseToolbox 执行工具箱操作,返回结果字符串。kind 支持 modbus/crc16/crc32/xor/sum/base64enc/base64dec/unixtime。
func ParseToolbox(kind, input string) string {
	switch kind {
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
	}
	return "未知操作"
}
