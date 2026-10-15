package com.xiaoleng.commbox.transport

import android.app.PendingIntent
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.hardware.usb.UsbManager
import android.os.Build
import com.hoho.android.usbserial.driver.UsbSerialPort
import com.hoho.android.usbserial.driver.UsbSerialProber
import com.xiaoleng.commbox.session.Connection
import com.xiaoleng.commbox.session.ConnectionConfig
import com.xiaoleng.commbox.session.ConnectionFactory
import com.xiaoleng.commbox.session.ConnectionStatus
import com.xiaoleng.commbox.session.UsbConfig
import java.util.concurrent.atomic.AtomicBoolean
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.withContext
import kotlin.coroutines.resume

class UsbConnectionFactory(private val context: Context, private val scope: CoroutineScope) : ConnectionFactory {
    override suspend fun open(config: ConnectionConfig, onBytes: (ByteArray, String?) -> Unit, onClosed: (Throwable?) -> Unit, @Suppress("UNUSED_PARAMETER") onStatus: (ConnectionStatus) -> Unit): Connection {
        require(config is UsbConfig) { "USB 工厂仅支持 USB 串口" }
        require(config.baud > 0 && config.dataBits in 5..8 && config.stopBits in 1..2 && config.parity in 0..2) { "串口参数无效" }
        val manager = context.getSystemService(UsbManager::class.java)
        val driver = UsbSerialProber.getDefaultProber().findAllDrivers(manager)
            .firstOrNull { it.device.deviceId == config.deviceId } ?: error("未找到所选 USB 串口设备")
        if (!manager.hasPermission(driver.device)) requestPermission(manager, driver.device.deviceId)
        val port = driver.ports.getOrNull(config.port) ?: error("USB 串口号无效")
        val usb = manager.openDevice(driver.device) ?: error("无法打开 USB 设备")
        try {
            withContext(Dispatchers.IO) {
                port.open(usb)
                port.setParameters(config.baud, config.dataBits, config.stopBits, config.parity)
            }
        } catch (error: Throwable) {
            runCatching { port.close() }
            usb.close()
            throw error
        }
        val closed = AtomicBoolean(false)
        val detachReceiver = object : BroadcastReceiver() {
            override fun onReceive(ctx: Context, intent: Intent) {
                val detached = intent.getParcelableExtra<android.hardware.usb.UsbDevice>(UsbManager.EXTRA_DEVICE)
                if (intent.action == UsbManager.ACTION_USB_DEVICE_DETACHED && detached?.deviceId == driver.device.deviceId && closed.compareAndSet(false, true)) {
                    runCatching { port.close() }
                    runCatching { usb.close() }
                    onClosed(IllegalStateException("USB 设备已拔出"))
                }
            }
        }
        val detachFilter = IntentFilter(UsbManager.ACTION_USB_DEVICE_DETACHED)
        if (Build.VERSION.SDK_INT >= 33) context.registerReceiver(detachReceiver, detachFilter, Context.RECEIVER_NOT_EXPORTED)
        else @Suppress("DEPRECATION") context.registerReceiver(detachReceiver, detachFilter)
        val reader = scope.launch(Dispatchers.IO) {
            val buffer = ByteArray(4096)
            var failure: Throwable? = null
            try {
                while (isActive && !closed.get()) {
                    val count = port.read(buffer, 250)
                    if (count > 0) onBytes(buffer.copyOf(count), "USB ${driver.device.deviceName}")
                }
            } catch (error: Throwable) {
                if (!closed.get()) failure = error
            } finally {
                runCatching { context.unregisterReceiver(detachReceiver) }
                runCatching { port.close() }
                runCatching { usb.close() }
                if (closed.compareAndSet(false, true)) onClosed(failure)
            }
        }
        return object : Connection {
            override suspend fun send(bytes: ByteArray) = withContext(Dispatchers.IO) { port.write(bytes, 3000) }
            override suspend fun close() {
                closed.set(true)
                runCatching { context.unregisterReceiver(detachReceiver) }
                withContext(Dispatchers.IO) { runCatching { port.close() }; usb.close() }
                reader.cancelAndJoin()
            }
        }
    }

    private suspend fun requestPermission(manager: UsbManager, deviceId: Int) = suspendCancellableCoroutine { continuation ->
        val action = "${context.packageName}.USB_PERMISSION"
        val receiver = object : BroadcastReceiver() {
            override fun onReceive(ctx: Context, intent: Intent) {
                if (intent.action != action) return
                runCatching { context.unregisterReceiver(this) }
                val granted = intent.getBooleanExtra(UsbManager.EXTRA_PERMISSION_GRANTED, false)
                if (continuation.isActive) {
                    if (granted) continuation.resume(Unit) else continuation.resumeWith(Result.failure(SecurityException("USB 设备授权被拒绝")))
                }
            }
        }
        val filter = IntentFilter(action)
        if (Build.VERSION.SDK_INT >= 33) context.registerReceiver(receiver, filter, Context.RECEIVER_NOT_EXPORTED)
        else @Suppress("DEPRECATION") context.registerReceiver(receiver, filter)
        continuation.invokeOnCancellation { runCatching { context.unregisterReceiver(receiver) } }
        val intent = PendingIntent.getBroadcast(context, deviceId, Intent(action).setPackage(context.packageName), PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE)
        val device = manager.deviceList.values.firstOrNull { it.deviceId == deviceId }
            ?: run { context.unregisterReceiver(receiver); continuation.resumeWith(Result.failure(IllegalStateException("USB 设备已拔出"))); return@suspendCancellableCoroutine }
        manager.requestPermission(device, intent)
    }
}

internal fun usbParity(value: Int): Int = when (value) {
    1 -> UsbSerialPort.PARITY_ODD
    2 -> UsbSerialPort.PARITY_EVEN
    else -> UsbSerialPort.PARITY_NONE
}
