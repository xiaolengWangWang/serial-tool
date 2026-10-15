package com.xiaoleng.commbox.protocol

import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertThrows
import org.junit.Test

class ModbusTest {
    @Test fun rtuReadOneRegisterMatchesDesktopVector() {
        assertArrayEquals(hex("01 03 00 00 00 01 84 0A"), Modbus.buildRtu(1, 0x03, 0, emptyList(), 1))
    }

    @Test fun tcpReadOneRegisterMatchesDesktopVector() {
        assertArrayEquals(hex("00 01 00 00 00 06 01 03 00 00 00 01"), Modbus.buildTcp(1, 1, 0x03, 0, emptyList(), 1))
    }

    @Test fun singleAndMultipleRegisterWriteVectors() {
        assertArrayEquals(hex("01 06 00 01 00 03 98 0B"), Modbus.buildRtu(1, 0x06, 1, listOf(3), 1))
        assertArrayEquals(hex("01 10 00 01 00 02 04 00 03 00 04 C3 A0"), Modbus.buildRtu(1, 0x10, 1, listOf(3, 4), 2))
    }

    @Test fun rejectsInvalidQuantityAndValue() {
        assertThrows(IllegalArgumentException::class.java) { Modbus.buildRtu(1, 0x03, 0, emptyList(), 126) }
        assertThrows(IllegalArgumentException::class.java) { Modbus.buildRtu(1, 0x03, 0xffff, emptyList(), 2) }
        assertThrows(IllegalArgumentException::class.java) { Modbus.buildRtu(1, 0x06, 0, listOf(65536), 1) }
    }

    private fun hex(value: String) = value.split(" ").map { it.toInt(16).toByte() }.toByteArray()
}
