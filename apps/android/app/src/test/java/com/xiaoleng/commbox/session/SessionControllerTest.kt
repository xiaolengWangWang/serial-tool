package com.xiaoleng.commbox.session

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Test

@OptIn(ExperimentalCoroutinesApi::class)
class SessionControllerTest {
    @Test fun lateCallbackIgnoredAfterDisconnect() = runTest {
        var callback: ((ByteArray, String?) -> Unit)? = null
        val factory = object : ConnectionFactory {
            override suspend fun open(config: ConnectionConfig, onBytes: (ByteArray, String?) -> Unit, onClosed: (Throwable?) -> Unit, onStatus: (ConnectionStatus) -> Unit): Connection {
                callback = onBytes
                return NoopConnection
            }
        }
        val received = mutableListOf<PacketEvent>()
        val controller = SessionController(factory, received::add)
        val config = TcpConfig("127.0.0.1", 502, false, false)
        controller.connect(config)
        controller.disconnect()
        callback!!(byteArrayOf(1), null)
        assertEquals(0, received.size)
        assertEquals(SessionState.Disconnected, controller.state.value)
    }

    @Test fun secondConnectClosesFirst() = runTest {
        var closed = 0
        val factory = object : ConnectionFactory {
            override suspend fun open(config: ConnectionConfig, onBytes: (ByteArray, String?) -> Unit, onClosed: (Throwable?) -> Unit, onStatus: (ConnectionStatus) -> Unit) = object : Connection {
                override suspend fun send(bytes: ByteArray) = Unit
                override suspend fun close() { closed++ }
            }
        }
        val controller = SessionController(factory)
        controller.connect(TcpConfig("127.0.0.1", 502, false, false))
        controller.connect(TcpConfig("127.0.0.1", 503, false, false))
        assertEquals(1, closed)
    }

    @Test fun failedOpenReturnsDisconnectedWithError() = runTest {
        val factory = object : ConnectionFactory {
            override suspend fun open(config: ConnectionConfig, onBytes: (ByteArray, String?) -> Unit, onClosed: (Throwable?) -> Unit, onStatus: (ConnectionStatus) -> Unit): Connection = error("拒绝连接")
        }
        val controller = SessionController(factory)
        controller.connect(TcpConfig("invalid", 502, false, false))
        assertEquals(SessionState.Failed("拒绝连接"), controller.state.value)
    }

    @Test fun immediateCloseDuringOpenDoesNotLeaveConnectedState() = runTest {
        val factory = object : ConnectionFactory {
            override suspend fun open(config: ConnectionConfig, onBytes: (ByteArray, String?) -> Unit, onClosed: (Throwable?) -> Unit, onStatus: (ConnectionStatus) -> Unit): Connection {
                onClosed(null)
                return NoopConnection
            }
        }
        val controller = SessionController(factory)
        controller.connect(TcpConfig("127.0.0.1", 502, false, false))
        assertEquals(SessionState.Disconnected, controller.state.value)
    }
}

private object NoopConnection : Connection {
    override suspend fun send(bytes: ByteArray) = Unit
    override suspend fun close() = Unit
}
