package wincore

// 工具箱:CRC / 校验和等纯函数,供两端 UI 的「工具箱」使用。

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
