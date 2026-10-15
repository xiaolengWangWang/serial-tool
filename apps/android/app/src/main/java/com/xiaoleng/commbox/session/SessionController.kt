package com.xiaoleng.commbox.session

import java.util.concurrent.atomic.AtomicLong
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock

class SessionController(
    private val factory: ConnectionFactory,
    private val onPacket: (PacketEvent) -> Unit = {},
    private val nowMs: () -> Long = System::currentTimeMillis,
) {
    private val generation = AtomicLong()
    private val operationLock = Mutex()
    private val stateLock = Any()
    private val mutableState = MutableStateFlow<SessionState>(SessionState.Disconnected)
    val state: StateFlow<SessionState> = mutableState.asStateFlow()
    private var connection: Connection? = null
    private var config: ConnectionConfig? = null
    private var connectionGeneration = 0L

    suspend fun connect(next: ConnectionConfig) {
        val token = synchronized(stateLock) { generation.incrementAndGet() }
        operationLock.withLock {
            val old = synchronized(stateLock) {
                connection.also { connection = null; config = null; connectionGeneration = 0L }
            }
            try {
                old?.close()
            } catch (error: Throwable) {
                if (error is CancellationException) throw error
                synchronized(stateLock) {
                    if (generation.get() == token) mutableState.value = SessionState.Failed("关闭旧连接失败：${error.message}")
                }
                return
            }
            if (generation.get() != token) return
            synchronized(stateLock) { mutableState.value = SessionState.Connecting(next) }
            val closedDuringOpen = java.util.concurrent.atomic.AtomicBoolean(false)
            val pendingStatus = java.util.concurrent.atomic.AtomicReference<ConnectionStatus?>(null)
            try {
                val opened = factory.open(next, { bytes, peer ->
                    synchronized(stateLock) {
                        if (generation.get() == token && connectionGeneration == token) {
                            onPacket(PacketEvent(nowMs(), Direction.RX, next.transport(), bytes.copyOf(), peer))
                        }
                    }
                }, { error ->
                    synchronized(stateLock) {
                        if (generation.get() == token) {
                            if (connection == null) closedDuringOpen.set(true)
                            connection = null
                            config = null
                            connectionGeneration = 0L
                            mutableState.value = error?.let { SessionState.Failed(it.message ?: "连接已断开") }
                                ?: SessionState.Disconnected
                        }
                    }
                }, { status ->
                    synchronized(stateLock) {
                        if (generation.get() == token) {
                            if (connectionGeneration == token) {
                                mutableState.value = if (status == ConnectionStatus.RECONNECTING) SessionState.Reconnecting(next) else SessionState.Connected(next)
                            } else {
                                pendingStatus.set(status)
                            }
                        }
                    }
                })
                val accepted = synchronized(stateLock) {
                    if (generation.get() == token && !closedDuringOpen.get()) {
                        connection = opened
                        config = next
                        connectionGeneration = token
                        mutableState.value = when (pendingStatus.get()) {
                            ConnectionStatus.RECONNECTING -> SessionState.Reconnecting(next)
                            else -> SessionState.Connected(next)
                        }
                        true
                    } else false
                }
                if (!accepted) {
                    opened.close()
                    return
                }
            } catch (error: Throwable) {
                if (error is CancellationException) throw error
                synchronized(stateLock) {
                    if (generation.get() == token) mutableState.value = SessionState.Failed(error.message ?: "连接失败")
                }
            }
        }
    }

    suspend fun send(bytes: ByteArray) = operationLock.withLock {
        val (active, activeConfig, token) = synchronized(stateLock) {
            val active = connection ?: error("尚未连接")
            val activeConfig = config ?: error("尚未连接")
            check(connectionGeneration == generation.get()) { "连接正在切换" }
            Triple(active, activeConfig, connectionGeneration)
        }
        val copy = bytes.copyOf()
        active.send(copy)
        synchronized(stateLock) {
            if (generation.get() == token && connectionGeneration == token) {
                onPacket(PacketEvent(nowMs(), Direction.TX, activeConfig.transport(), copy, null))
            }
        }
    }

    suspend fun disconnect() {
        synchronized(stateLock) { generation.incrementAndGet() }
        operationLock.withLock {
            val old = synchronized(stateLock) {
                connection.also {
                    connection = null
                    config = null
                    connectionGeneration = 0L
                    mutableState.value = SessionState.Disconnected
                }
            }
            old?.close()
        }
    }
}

private fun ConnectionConfig.transport(): TransportKind = when (this) {
    is TcpConfig -> TransportKind.TCP
    is UdpConfig -> TransportKind.UDP
    is UsbConfig -> TransportKind.USB
    is SppConfig -> TransportKind.SPP
    is BleConfig -> TransportKind.BLE
    else -> error("不支持的连接类型")
}
