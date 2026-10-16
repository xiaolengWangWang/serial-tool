package com.xiaoleng.commbox

import java.nio.charset.Charset

enum class TextEncoding(val label: String, private val charset: Charset) {
    UTF8("UTF-8", Charsets.UTF_8),
    GBK("GBK", Charset.forName("GBK"));

    fun decode(bytes: ByteArray): String = String(bytes, charset)
    fun encode(text: String): ByteArray = text.toByteArray(charset)
}
