package com.xiaoleng.commbox.transport

import com.xiaoleng.commbox.session.Connection
import com.xiaoleng.commbox.session.ConnectionConfig
import com.xiaoleng.commbox.session.ConnectionFactory
import com.xiaoleng.commbox.session.ConnectionStatus
import android.content.Context
import kotlinx.coroutines.CoroutineScope

class AndroidConnections(private val context: Context, scope: CoroutineScope) : ConnectionFactory {
    private val network = NetworkConnectionFactory(scope)
    private val usb = UsbConnectionFactory(context, scope)
    private val spp = SppConnectionFactory(context, scope)
    private val ble = BleConnectionFactory(context, scope)

    suspend fun scanBle() = ble.scanDevices()
    fun pairedSppDevices() = spp.pairedDevices()

    override suspend fun open(
        config: ConnectionConfig,
        onBytes: (ByteArray, String?) -> Unit,
        onClosed: (Throwable?) -> Unit,
        onStatus: (ConnectionStatus) -> Unit,
    ): Connection = when (config) {
        is com.xiaoleng.commbox.session.UsbConfig -> usb.open(config, onBytes, onClosed, onStatus)
        is com.xiaoleng.commbox.session.SppConfig -> spp.open(config, onBytes, onClosed, onStatus)
        is com.xiaoleng.commbox.session.BleConfig -> ble.open(config, onBytes, onClosed, onStatus)
        else -> network.open(config, onBytes, onClosed, onStatus)
    }
}
