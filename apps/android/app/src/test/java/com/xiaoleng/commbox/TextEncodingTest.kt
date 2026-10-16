package com.xiaoleng.commbox

import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Test

class TextEncodingTest {
    @Test fun utf8ChineseRoundTrip() {
        val bytes = byteArrayOf(0xE4.toByte(), 0xB8.toByte(), 0xAD.toByte(), 0xE6.toByte(), 0x96.toByte(), 0x87.toByte())
        assertEquals("中文", TextEncoding.UTF8.decode(bytes))
        assertArrayEquals(bytes, TextEncoding.UTF8.encode("中文"))
    }

    @Test fun gbkChineseRoundTrip() {
        val bytes = byteArrayOf(0xD6.toByte(), 0xD0.toByte(), 0xCE.toByte(), 0xC4.toByte())
        assertEquals("中文", TextEncoding.GBK.decode(bytes))
        assertArrayEquals(bytes, TextEncoding.GBK.encode("中文"))
    }
}
