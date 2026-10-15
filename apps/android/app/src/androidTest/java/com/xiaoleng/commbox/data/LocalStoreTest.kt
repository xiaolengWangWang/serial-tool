package com.xiaoleng.commbox.data

import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.xiaoleng.commbox.session.Direction
import com.xiaoleng.commbox.session.PacketEvent
import com.xiaoleng.commbox.session.TcpConfig
import com.xiaoleng.commbox.session.TransportKind
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class LocalStoreTest {
    @Test fun binaryCaptureAndProfileSurviveReopen() = runBlocking {
        val context = ApplicationProvider.getApplicationContext<android.content.Context>()
        context.deleteDatabase("commbox.db")
        val first = LocalStore(context)
        first.record(PacketEvent(1, Direction.RX, TransportKind.TCP, byteArrayOf(0, -1), "127.0.0.1:502"))
        first.saveProfile(TcpConfig("127.0.0.1", 502, false, true))
        first.close()

        val reopened = LocalStore(context)
        val packet = reopened.recentPackets().single()
        assertArrayEquals(byteArrayOf(0, -1), packet.bytes)
        assertEquals(TcpConfig("127.0.0.1", 502, false, true), reopened.recentProfiles().single())
        reopened.close()
    }
}
