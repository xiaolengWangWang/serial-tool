package com.xiaoleng.commbox.transport

import android.Manifest
import android.bluetooth.BluetoothManager
import android.bluetooth.BluetoothSocket
import android.content.Context
import android.content.pm.PackageManager
import android.os.Build
import com.xiaoleng.commbox.session.Connection
import com.xiaoleng.commbox.session.ConnectionConfig
import com.xiaoleng.commbox.session.ConnectionFactory
import com.xiaoleng.commbox.session.ConnectionStatus
import com.xiaoleng.commbox.session.SppConfig
import java.util.UUID
import java.util.concurrent.atomic.AtomicBoolean
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

class SppConnectionFactory(private val context: Context, private val scope: CoroutineScope) : ConnectionFactory {
    fun pairedDevices(): List<BluetoothDeviceInfo> {
        requireConnectPermission()
        val adapter = context.getSystemService(BluetoothManager::class.java).adapter ?: return emptyList()
        return adapter.bondedDevices.orEmpty().map { BluetoothDeviceInfo(it.address, runCatching { it.name.orEmpty() }.getOrDefault("")) }
    }

    override suspend fun open(config: ConnectionConfig, onBytes: (ByteArray, String?) -> Unit, onClosed: (Throwable?) -> Unit, @Suppress("UNUSED_PARAMETER") onStatus: (ConnectionStatus) -> Unit): Connection {
        require(config is SppConfig) { "SPP 工厂仅支持经典蓝牙串口" }
        require(config.address.matches(Regex("(?i)[0-9a-f]{2}(:[0-9a-f]{2}){5}"))) { "蓝牙地址格式无效" }
        requireConnectPermission()
        val adapter = context.getSystemService(BluetoothManager::class.java).adapter ?: error("此设备不支持蓝牙")
        if (!adapter.isEnabled) error("请先开启蓝牙")
        val device = adapter.getRemoteDevice(config.address)
        val socket = device.createRfcommSocketToServiceRecord(SPP_UUID)
        try {
            withContext(Dispatchers.IO) { adapter.cancelDiscovery(); socket.connect() }
        } catch (error: Throwable) {
            runCatching { socket.close() }
            throw IllegalStateException("无法连接蓝牙串口设备：${error.message ?: "连接失败"}", error)
        }
        val closed = AtomicBoolean(false)
        val reader = scope.launch(Dispatchers.IO) {
            val buffer = ByteArray(4096)
            var failure: Throwable? = null
            try {
                while (isActive && !closed.get()) {
                    val count = socket.inputStream.read(buffer)
                    if (count < 0) break
                    if (count > 0) onBytes(buffer.copyOf(count), config.address)
                }
            } catch (error: Throwable) {
                if (!closed.get()) failure = error
            } finally {
                runCatching { socket.close() }
                if (closed.compareAndSet(false, true)) onClosed(failure)
            }
        }
        return object : Connection {
            override suspend fun send(bytes: ByteArray) { withContext(Dispatchers.IO) { socket.outputStream.apply { write(bytes); flush() } } }
            override suspend fun close() {
                closed.set(true)
                withContext(Dispatchers.IO) { socket.close() }
                reader.cancelAndJoin()
            }
        }
    }

    private fun requireConnectPermission() {
        if (Build.VERSION.SDK_INT >= 31 && context.checkSelfPermission(Manifest.permission.BLUETOOTH_CONNECT) != PackageManager.PERMISSION_GRANTED) {
            throw SecurityException("请授予蓝牙连接权限")
        }
    }

    companion object { val SPP_UUID: UUID = UUID.fromString("00001101-0000-1000-8000-00805F9B34FB") }
}

data class BluetoothDeviceInfo(val address: String, val name: String)
