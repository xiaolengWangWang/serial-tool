package com.xiaoleng.commbox.transport

import com.xiaoleng.commbox.session.Connection
import com.xiaoleng.commbox.session.ConnectionConfig
import com.xiaoleng.commbox.session.ConnectionFactory
import com.xiaoleng.commbox.session.ConnectionStatus
import com.xiaoleng.commbox.session.TcpConfig
import com.xiaoleng.commbox.session.UdpConfig
import java.net.DatagramPacket
import java.net.DatagramSocket
import java.net.InetAddress
import java.net.InetSocketAddress
import java.net.ServerSocket
import java.net.Socket
import java.net.SocketException
import java.util.concurrent.atomic.AtomicReference
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext

class NetworkConnectionFactory(private val scope: CoroutineScope) : ConnectionFactory {
    override suspend fun open(config: ConnectionConfig, onBytes: (ByteArray, String?) -> Unit, onClosed: (Throwable?) -> Unit, onStatus: (ConnectionStatus) -> Unit): Connection = when (config) {
        is TcpConfig -> openTcp(config, onBytes, onClosed, onStatus)
        is UdpConfig -> openUdp(config, onBytes, onClosed, onStatus)
        else -> error("网络连接工厂不支持此连接类型")
    }

    private suspend fun openTcp(config: TcpConfig, onBytes: (ByteArray, String?) -> Unit, onClosed: (Throwable?) -> Unit, onStatus: (ConnectionStatus) -> Unit): Connection {
        require(config.port in 1..65535) { "端口必须为 1–65535" }
        val socket: Socket
        val server: ServerSocket?
        if (config.listen) {
            require(config.host.isNotBlank()) { "监听地址不能为空" }
            server = withContext(Dispatchers.IO) { ServerSocket().apply { reuseAddress = true; bind(InetSocketAddress(config.host, config.port)) } }
            socket = Socket()
        } else {
            require(config.host.isNotBlank()) { "目标地址不能为空" }
            server = null
            socket = withContext(Dispatchers.IO) { Socket().apply { connect(InetSocketAddress(config.host, config.port), 5_000) } }
        }
        val writeLock = Mutex()
        val reader = AtomicReference<Job?>(null)
        var acceptJob: Job? = null
        val accepted = AtomicReference<Socket?>(null)
        val activeClient = AtomicReference<Socket?>(if (server == null) socket else null)
        val closed = java.util.concurrent.atomic.AtomicBoolean(false)
        fun attach(active: Socket) {
            accepted.set(active)
            reader.set(scope.launch(Dispatchers.IO) {
                var failure: Throwable? = null
                try {
                    val input = active.getInputStream()
                    val buffer = ByteArray(4096)
                    while (isActive) {
                        val count = input.read(buffer)
                        if (count < 0) break
                        if (count > 0) onBytes(buffer.copyOf(count), active.remoteSocketAddress.toString())
                    }
                } catch (error: Throwable) {
                    if (!closed.get() && error !is SocketException) failure = error
                } finally {
                    runCatching { active.close() }
                    runCatching { server?.close() }
                    if (closed.compareAndSet(false, true)) onClosed(failure)
                }
            })
        }
        if (server == null && !config.autoReconnect) attach(socket)
        else if (server == null) reader.set(scope.launch(Dispatchers.IO) {
            var active = socket
            var backoff = 500L
            while (isActive && !closed.get()) {
                try {
                    val input = active.getInputStream()
                    val buffer = ByteArray(4096)
                    while (isActive && !closed.get()) {
                        val count = input.read(buffer)
                        if (count < 0) throw java.io.EOFException("TCP 对端已关闭连接")
                        if (count > 0) onBytes(buffer.copyOf(count), active.remoteSocketAddress.toString())
                    }
                    break
                } catch (error: Throwable) {
                    runCatching { active.close() }
                    if (!isActive || closed.get()) break
                    onStatus(ConnectionStatus.RECONNECTING)
                    var connected = false
                    while (isActive && !closed.get() && !connected) {
                        delay(backoff)
                        try {
                            val reopened = Socket().apply { connect(InetSocketAddress(config.host, config.port), 5_000) }
                            if (!isActive || closed.get()) {
                                reopened.close()
                                break
                            }
                            active = reopened
                            activeClient.set(active)
                            backoff = 500L
                            connected = true
                            onStatus(ConnectionStatus.CONNECTED)
                        } catch (_: Throwable) {
                            backoff = (backoff * 2).coerceAtMost(5_000L)
                        }
                    }
                }
            }
            if (closed.compareAndSet(false, true)) onClosed(null)
        }) else acceptJob = scope.launch(Dispatchers.IO) {
            try {
                attach(server.accept())
            } catch (error: Throwable) {
                runCatching { server.close() }
                if (closed.compareAndSet(false, true)) onClosed(error)
            }
        }
        return object : Connection {
            override suspend fun send(bytes: ByteArray) {
                writeLock.withLock {
                    val active = (if (server == null) activeClient.get() else accepted.get())
                        ?: error(if (server == null) "TCP 连接尚未就绪" else "等待 TCP 客户端连接")
                    withContext(Dispatchers.IO) { active.getOutputStream().apply { write(bytes); flush() } }
                }
            }

            override suspend fun close() {
                if (closed.compareAndSet(false, true)) onClosed(null)
                withContext(Dispatchers.IO) { runCatching { activeClient.get()?.close() }; runCatching { accepted.get()?.close() }; runCatching { server?.close() } }
                acceptJob?.cancelAndJoin()
                reader.get()?.cancelAndJoin()
            }
        }
    }

    private suspend fun openUdp(config: UdpConfig, onBytes: (ByteArray, String?) -> Unit, onClosed: (Throwable?) -> Unit, @Suppress("UNUSED_PARAMETER") onStatus: (ConnectionStatus) -> Unit): Connection {
        require(config.port in 1..65535) { "端口必须为 1–65535" }
        if (!config.listen) require(config.host.isNotBlank()) { "目标地址不能为空" }
        val socket = withContext(Dispatchers.IO) {
            if (config.listen) DatagramSocket(null).apply { reuseAddress = true; bind(InetSocketAddress(config.host, config.port)) }
            else DatagramSocket()
        }
        val destination = if (config.listen) null else withContext(Dispatchers.IO) { InetAddress.getByName(config.host) }
        val remote = destination?.let { InetSocketAddress(it, config.port) }
        val closed = java.util.concurrent.atomic.AtomicBoolean(false)
        val lastPeer = AtomicReference<InetSocketAddress?>(null)
        val reader = scope.launch(Dispatchers.IO) {
            val buffer = ByteArray(65507)
            try {
                while (isActive) {
                    val datagram = DatagramPacket(buffer, buffer.size)
                    socket.receive(datagram)
                    lastPeer.set(InetSocketAddress(datagram.address, datagram.port))
                    onBytes(datagram.data.copyOf(datagram.length), "${datagram.address.hostAddress}:${datagram.port}")
                }
            } catch (error: Throwable) {
                val failure = if (error is SocketException && socket.isClosed) null else error
                runCatching { socket.close() }
                if (closed.compareAndSet(false, true)) onClosed(failure)
            }
        }
        return object : Connection {
            override suspend fun send(bytes: ByteArray) {
                val peer = remote ?: lastPeer.get() ?: error("UDP 监听模式需要先收到对端数据")
                withContext(Dispatchers.IO) { socket.send(DatagramPacket(bytes, bytes.size, peer)) }
            }

            override suspend fun close() {
                closed.set(true)
                socket.close()
                reader.cancelAndJoin()
            }
        }
    }
}
