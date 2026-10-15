package com.xiaoleng.commbox.transport

import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Test

class BleChunksTest {
    @Test fun nusDefaultsMatchStandardProfile() {
        assertEquals("6e400001-b5a3-f393-e0a9-e50e24dcca9e", BleConnectionFactory.NUS_SERVICE.toString())
        assertEquals("6e400002-b5a3-f393-e0a9-e50e24dcca9e", BleConnectionFactory.NUS_WRITE.toString())
        assertEquals("6e400003-b5a3-f393-e0a9-e50e24dcca9e", BleConnectionFactory.NUS_NOTIFY.toString())
    }

    @Test fun splitsAtPayloadBoundaryAndPreservesOrder() {
        val bytes = ByteArray(47) { it.toByte() }
        val chunks = splitBleWrite(bytes, 20)
        assertEquals(listOf(20, 20, 7), chunks.map(ByteArray::size))
        assertArrayEquals(bytes, chunks.reduce(ByteArray::plus))
    }

    @Test fun rejectsInvalidPayloadSize() {
        assertThrows(IllegalArgumentException::class.java) { splitBleWrite(byteArrayOf(1), 0) }
    }
}
