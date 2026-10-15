package com.xiaoleng.commbox.transport

import java.net.ServerSocket
import java.net.DatagramSocket
import java.net.Socket
import java.util.concurrent.Executors
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import kotlinx.coroutines.withContext
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertThrows
import org.junit.Test
import com.xiaoleng.commbox.session.TcpConfig
import com.xiaoleng.commbox.session.UdpConfig
import com.xiaoleng.commbox.session.ConnectionStatus

class NetworkConnectionsTest {
    @Test fun tcpClientReceivesRawBytes() = runBlocking {
        val port = ServerSocket(0).use { it.localPort }
        val executor = Executors.newSingleThreadExecutor()
        val peer = executor.submit {
            java.net.ServerSocket(port).use { listener -> listener.accept().use { it.getOutputStream().write(byteArrayOf(0, -1)) } }
        }
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
        val received = CompletableDeferred<ByteArray>()
        val connection = NetworkConnectionFactory(scope).open(TcpConfig("127.0.0.1", port, false, false), { bytes, _ -> received.complete(bytes) }, {})
        try {
            assertArrayEquals(byteArrayOf(0, -1), withTimeout(3000) { received.await() })
        } finally {
            connection.close()
            scope.cancel()
            executor.shutdownNow()
            peer.get()
        }
    }

    @Test fun rejectsInvalidPortBeforeOpeningSocket() = runBlocking {
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
        assertThrows(IllegalArgumentException::class.java) {
            runBlocking { NetworkConnectionFactory(scope).open(UdpConfig("localhost", 0, false), { _, _ -> }, {}) }
        }
        scope.cancel()
    }

    @Test fun tcpListenerExchangesRawBytes() = runBlocking {
        val port = ServerSocket(0).use { it.localPort }
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
        val received = CompletableDeferred<ByteArray>()
        val server = NetworkConnectionFactory(scope).open(TcpConfig("127.0.0.1", port, true, false), { bytes, _ -> received.complete(bytes) }, {})
        val client = Socket()
        try {
            withContext(Dispatchers.IO) { client.connect(java.net.InetSocketAddress("127.0.0.1", port), 3000); client.getOutputStream().write(byteArrayOf(9, 0, 8)) }
            assertArrayEquals(byteArrayOf(9, 0, 8), withTimeout(3000) { received.await() })
            server.send(byteArrayOf(7, 6))
            val response = withContext(Dispatchers.IO) { ByteArray(2).also { java.io.DataInputStream(client.getInputStream()).readFully(it) } }
            assertArrayEquals(byteArrayOf(7, 6), response)
        } finally {
            client.close()
            server.close()
            scope.cancel()
        }
    }

    @Test fun tcpClientReconnectsAfterPeerCloses() = runBlocking {
        val listener = java.net.ServerSocket(0)
        val executor = Executors.newSingleThreadExecutor()
        val peer = executor.submit {
            listener.accept().close()
            listener.accept().use { it.getOutputStream().write(byteArrayOf(4, 2)) }
            listener.close()
        }
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
        val reconnecting = CompletableDeferred<Unit>()
        val reconnected = CompletableDeferred<Unit>()
        val received = CompletableDeferred<ByteArray>()
        val connection = NetworkConnectionFactory(scope).open(
            TcpConfig("127.0.0.1", listener.localPort, false, true),
            { bytes, _ -> received.complete(bytes) },
            {},
            { status -> if (status == ConnectionStatus.RECONNECTING) reconnecting.complete(Unit) else reconnected.complete(Unit) },
        )
        try {
            withTimeout(5000) { reconnecting.await(); reconnected.await() }
            assertArrayEquals(byteArrayOf(4, 2), withTimeout(3000) { received.await() })
        } finally {
            connection.close()
            scope.cancel()
            runCatching { listener.close() }
            executor.shutdownNow()
            runCatching { peer.get(1, java.util.concurrent.TimeUnit.SECONDS) }
        }
    }

    @Test fun udpListenerRepliesToMostRecentPeer() = runBlocking {
        val port = DatagramSocket(0).use { it.localPort }
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
        val serverReceived = CompletableDeferred<ByteArray>()
        val clientReceived = CompletableDeferred<ByteArray>()
        val factory = NetworkConnectionFactory(scope)
        val server = factory.open(UdpConfig("127.0.0.1", port, true), { bytes, _ -> serverReceived.complete(bytes) }, {})
        val client = factory.open(UdpConfig("127.0.0.1", port, false), { bytes, _ -> clientReceived.complete(bytes) }, {})
        try {
            client.send(byteArrayOf(0, 1))
            assertArrayEquals(byteArrayOf(0, 1), withTimeout(3000) { serverReceived.await() })
            server.send(byteArrayOf(2, 0xff.toByte()))
            assertArrayEquals(byteArrayOf(2, 0xff.toByte()), withTimeout(3000) { clientReceived.await() })
        } finally {
            client.close()
            server.close()
            scope.cancel()
        }
    }
}
