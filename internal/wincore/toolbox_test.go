package wincore

import "testing"

func TestCRC16Modbus(t *testing.T) {
	// 标准 Modbus 帧 01 03 00 00 00 0A,CRC = 0xCDC5(传输时低字节在前,即 C5 CD)
	got := CRC16Modbus([]byte{0x01, 0x03, 0x00, 0x00, 0x00, 0x0A})
	if got != 0xCDC5 {
		t.Fatalf("CRC16Modbus = %04X, 期望 CDC5", got)
	}
}

func TestCRC16CCITT(t *testing.T) {
	// CRC-16/CCITT-FALSE 对 "123456789" 的校验值为 0x29B1
	got := CRC16CCITT([]byte("123456789"))
	if got != 0x29B1 {
		t.Fatalf("CRC16CCITT = %04X, 期望 29B1", got)
	}
}

func TestCRC32(t *testing.T) {
	// 标准 CRC32 对 "123456789" 的校验值为 0xCBF43926
	got := CRC32([]byte("123456789"))
	if got != 0xCBF43926 {
		t.Fatalf("CRC32 = %08X, 期望 CBF43926", got)
	}
}

func TestChecksums(t *testing.T) {
	data := []byte{0x01, 0x03, 0x00}
	if got := XORChecksum(data); got != 0x02 {
		t.Fatalf("XORChecksum = %02X, 期望 02", got)
	}
	if got := SUMChecksum(data); got != 0x04 {
		t.Fatalf("SUMChecksum = %02X, 期望 04", got)
	}
}
