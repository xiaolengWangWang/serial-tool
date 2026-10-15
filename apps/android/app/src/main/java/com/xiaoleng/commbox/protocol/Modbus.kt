package com.xiaoleng.commbox.protocol

object Modbus {
    fun buildRtu(unit: Int, function: Int, address: Int, values: List<Int>, quantity: Int): ByteArray {
        validate(unit, function, address, values, quantity)
        val pdu = buildPdu(function, address, values, quantity)
        val frame = byteArrayOf(unit.toByte()) + pdu
        val crc = crc16(frame)
        return frame + byteArrayOf(crc.toByte(), (crc ushr 8).toByte())
    }

    fun buildTcp(transaction: Int, unit: Int, function: Int, address: Int, values: List<Int>, quantity: Int): ByteArray {
        require(transaction in 0..0xffff) { "事务标识超出范围" }
        validate(unit, function, address, values, quantity)
        val pdu = buildPdu(function, address, values, quantity)
        val length = pdu.size + 1
        return byteArrayOf(
            (transaction ushr 8).toByte(), transaction.toByte(), 0, 0,
            (length ushr 8).toByte(), length.toByte(), unit.toByte(),
        ) + pdu
    }

    fun crc16(bytes: ByteArray): Int {
        var crc = 0xffff
        for (byte in bytes) {
            crc = crc xor (byte.toInt() and 0xff)
            repeat(8) { crc = if (crc and 1 != 0) (crc ushr 1) xor 0xa001 else crc ushr 1 }
        }
        return crc and 0xffff
    }

    private fun validate(unit: Int, function: Int, address: Int, values: List<Int>, quantity: Int) {
        require(unit in 0..247) { "站号必须为 0–247" }
        require(address in 0..0xffff) { "寄存器地址必须为 0–65535" }
        require(function in setOf(0x03, 0x04, 0x06, 0x10)) { "仅支持功能码 03、04、06、10" }
        when (function) {
            0x03, 0x04 -> {
                require(quantity in 1..125) { "读取数量必须为 1–125" }
                require(address + quantity <= 0x10000) { "寄存器地址范围溢出" }
            }
            0x06 -> {
                require(quantity == 1 && values.size == 1) { "06 功能码需要一个寄存器值" }
                validateValues(values)
            }
            0x10 -> {
                require(quantity in 1..123 && values.size == quantity) { "写入数量与寄存器值数量不匹配" }
                require(address + quantity <= 0x10000) { "寄存器地址范围溢出" }
                validateValues(values)
            }
        }
        if (function == 0x03 || function == 0x04) require(values.isEmpty()) { "读取指令无需寄存器值" }
    }

    private fun validateValues(values: List<Int>) = values.forEach {
        require(it in 0..0xffff) { "寄存器值必须为 0–65535" }
    }

    private fun buildPdu(function: Int, address: Int, values: List<Int>, quantity: Int): ByteArray {
        val head = byteArrayOf(function.toByte(), (address ushr 8).toByte(), address.toByte())
        return when (function) {
            0x03, 0x04 -> head + word(quantity)
            0x06 -> head + word(values.single())
            0x10 -> head + word(quantity) + byteArrayOf((quantity * 2).toByte()) + values.flatMap { word(it).toList() }.toByteArray()
            else -> error("unsupported function")
        }
    }

    private fun word(value: Int) = byteArrayOf((value ushr 8).toByte(), value.toByte())
}
