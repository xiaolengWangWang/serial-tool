package com.xiaoleng.commbox.session

interface ConnectionConfig

interface Connection {
    suspend fun send(bytes: ByteArray)
    suspend fun close()
}

interface ConnectionFactory {
    suspend fun open(
        config: ConnectionConfig,
        onBytes: (ByteArray, String?) -> Unit,
        onClosed: (Throwable?) -> Unit,
        onStatus: (ConnectionStatus) -> Unit = {},
    ): Connection
}

enum class ConnectionStatus { RECONNECTING, CONNECTED }

data class TcpConfig(val host: String, val port: Int, val listen: Boolean, val autoReconnect: Boolean) : ConnectionConfig
data class UdpConfig(val host: String, val port: Int, val listen: Boolean) : ConnectionConfig
data class UsbConfig(val deviceId: Int, val port: Int, val baud: Int, val dataBits: Int, val stopBits: Int, val parity: Int) : ConnectionConfig
data class SppConfig(val address: String) : ConnectionConfig
data class BleConfig(val address: String, val serviceUuid: String, val writeUuid: String, val notifyUuid: String, val writeWithResponse: Boolean) : ConnectionConfig

enum class Direction { RX, TX }
enum class TransportKind { USB, SPP, BLE, TCP, UDP }

data class PacketEvent(
    val timestampMs: Long,
    val direction: Direction,
    val transport: TransportKind,
    val bytes: ByteArray,
    val peer: String? = null,
)

sealed interface SessionState {
    data object Disconnected : SessionState
    data class Connecting(val config: ConnectionConfig) : SessionState
    data class Connected(val config: ConnectionConfig) : SessionState
    data class Reconnecting(val config: ConnectionConfig) : SessionState
    data class Failed(val message: String) : SessionState
}
