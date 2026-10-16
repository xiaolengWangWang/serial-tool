package com.xiaoleng.commbox.transport

import android.Manifest
import android.bluetooth.BluetoothDevice
import android.bluetooth.BluetoothGatt
import android.bluetooth.BluetoothGattCallback
import android.bluetooth.BluetoothGattCharacteristic
import android.bluetooth.BluetoothGattDescriptor
import android.bluetooth.BluetoothManager
import android.bluetooth.BluetoothProfile
import android.bluetooth.le.ScanCallback
import android.bluetooth.le.ScanResult
import android.bluetooth.le.BluetoothLeScanner
import android.content.Context
import android.content.pm.PackageManager
import android.os.Build
import com.xiaoleng.commbox.session.BleConfig
import com.xiaoleng.commbox.session.Connection
import com.xiaoleng.commbox.session.ConnectionConfig
import com.xiaoleng.commbox.session.ConnectionFactory
import com.xiaoleng.commbox.session.ConnectionStatus
import java.util.UUID
import java.util.concurrent.atomic.AtomicBoolean
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.delay
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeout
import kotlin.coroutines.resume

class BleConnectionFactory(private val context: Context, private val scope: CoroutineScope) : ConnectionFactory {
    suspend fun scanDevices(durationMs: Long = 6_000): List<BleDeviceInfo> {
        requirePermission(Manifest.permission.BLUETOOTH_SCAN, "请授予蓝牙扫描权限")
        val scanner: BluetoothLeScanner = context.getSystemService(BluetoothManager::class.java).adapter?.bluetoothLeScanner
            ?: error("蓝牙扫描不可用")
        return suspendCancellableCoroutine { continuation ->
            val devices = linkedMapOf<String, BleDeviceInfo>()
            val devicesLock = Any()
            val callback = object : ScanCallback() {
                override fun onScanResult(callbackType: Int, result: ScanResult) {
                    val device = result.device
                    val info = BleDeviceInfo(device.address, runCatching { device.name.orEmpty() }.getOrDefault(""))
                    synchronized(devicesLock) { devices[device.address] = info }
                }
                override fun onScanFailed(errorCode: Int) {
                    runCatching { scanner.stopScan(this) }
                    if (continuation.isActive) continuation.resumeWith(Result.failure(IllegalStateException("BLE 扫描失败，状态码 $errorCode")))
                }
            }
            try { scanner.startScan(callback) } catch (error: Throwable) { continuation.resumeWith(Result.failure(error)); return@suspendCancellableCoroutine }
            val stop = scope.launch {
                delay(durationMs)
                runCatching { scanner.stopScan(callback) }
                val result = synchronized(devicesLock) { devices.values.toList() }
                if (continuation.isActive) continuation.resume(result)
            }
            continuation.invokeOnCancellation { stop.cancel(); runCatching { scanner.stopScan(callback) } }
        }
    }

    override suspend fun open(config: ConnectionConfig, onBytes: (ByteArray, String?) -> Unit, onClosed: (Throwable?) -> Unit, @Suppress("UNUSED_PARAMETER") onStatus: (ConnectionStatus) -> Unit): Connection {
        require(config is BleConfig) { "BLE 工厂仅支持 BLE UART" }
        requirePermission(Manifest.permission.BLUETOOTH_CONNECT, "请授予蓝牙连接权限")
        val address = config.address
        val serviceId = UUID.fromString(config.serviceUuid)
        val writeId = UUID.fromString(config.writeUuid)
        val notifyId = UUID.fromString(config.notifyUuid)
        val adapter = context.getSystemService(BluetoothManager::class.java).adapter ?: error("此设备不支持蓝牙")
        if (!adapter.isEnabled) error("请先开启蓝牙")
        val device = adapter.getRemoteDevice(address)
        val ready = CompletableDeferred<Unit>()
        val closed = AtomicBoolean(false)
        var mtu = 23
        var gattRef: BluetoothGatt? = null
        var writeChar: BluetoothGattCharacteristic? = null
        var pendingWrite: CompletableDeferred<Int>? = null
        val callback = object : BluetoothGattCallback() {
            override fun onConnectionStateChange(gatt: BluetoothGatt, status: Int, newState: Int) {
                if (status != BluetoothGatt.GATT_SUCCESS) {
                    if (!ready.isCompleted) ready.completeExceptionally(IllegalStateException("BLE 连接失败，状态码 $status"))
                    else if (closed.compareAndSet(false, true)) onClosed(IllegalStateException("BLE 连接断开，状态码 $status"))
                } else if (newState == BluetoothProfile.STATE_CONNECTED) {
                    if (Build.VERSION.SDK_INT >= 21 && !gatt.requestMtu(247)) gatt.discoverServices()
                    else if (Build.VERSION.SDK_INT < 21) gatt.discoverServices()
                } else if (newState == BluetoothProfile.STATE_DISCONNECTED) {
                    if (!ready.isCompleted) ready.completeExceptionally(IllegalStateException("BLE 设备已断开"))
                    else if (closed.compareAndSet(false, true)) onClosed(null)
                }
            }

            override fun onMtuChanged(gatt: BluetoothGatt, value: Int, status: Int) {
                mtu = if (status == BluetoothGatt.GATT_SUCCESS) value else 23
                gatt.discoverServices()
            }

            override fun onServicesDiscovered(gatt: BluetoothGatt, status: Int) {
                if (status != BluetoothGatt.GATT_SUCCESS) {
                    ready.completeExceptionally(IllegalStateException("BLE 服务发现失败"))
                    return
                }
                val service = gatt.getService(serviceId)
                writeChar = service?.getCharacteristic(writeId)
                val notifyChar = service?.getCharacteristic(notifyId)
                if (service == null || writeChar == null || notifyChar == null) {
                    ready.completeExceptionally(IllegalStateException("未找到配置的 BLE UART 服务或特征"))
                    return
                }
                if (!gatt.setCharacteristicNotification(notifyChar, true)) {
                    ready.completeExceptionally(IllegalStateException("无法订阅 BLE 通知"))
                    return
                }
                val descriptor = notifyChar.getDescriptor(CCCD_UUID)
                    ?: run { ready.completeExceptionally(IllegalStateException("BLE 通知缺少 CCCD 描述符")); return }
                if (Build.VERSION.SDK_INT >= 33) {
                    val result = gatt.writeDescriptor(descriptor, BluetoothGattDescriptor.ENABLE_NOTIFICATION_VALUE)
                    if (result != BluetoothGatt.GATT_SUCCESS) ready.completeExceptionally(IllegalStateException("BLE 通知订阅失败"))
                } else {
                    @Suppress("DEPRECATION")
                    descriptor.value = BluetoothGattDescriptor.ENABLE_NOTIFICATION_VALUE
                    @Suppress("DEPRECATION")
                    if (!gatt.writeDescriptor(descriptor)) ready.completeExceptionally(IllegalStateException("BLE 通知订阅失败"))
                }
            }

            override fun onDescriptorWrite(gatt: BluetoothGatt, descriptor: BluetoothGattDescriptor, status: Int) {
                if (descriptor.uuid == CCCD_UUID) {
                    if (status == BluetoothGatt.GATT_SUCCESS) ready.complete(Unit)
                    else ready.completeExceptionally(IllegalStateException("BLE 通知订阅失败，状态码 $status"))
                }
            }

            override fun onCharacteristicChanged(gatt: BluetoothGatt, characteristic: BluetoothGattCharacteristic, value: ByteArray) {
                if (characteristic.uuid == notifyId && !closed.get()) onBytes(value.copyOf(), address)
            }

            @Suppress("DEPRECATION")
            override fun onCharacteristicChanged(gatt: BluetoothGatt, characteristic: BluetoothGattCharacteristic) {
                if (characteristic.uuid == notifyId && !closed.get()) onBytes(characteristic.value?.copyOf() ?: return, address)
            }

            override fun onCharacteristicWrite(gatt: BluetoothGatt, characteristic: BluetoothGattCharacteristic, status: Int) {
                pendingWrite?.complete(status)
                pendingWrite = null
            }
        }
        gattRef = device.connectGatt(context, false, callback, BluetoothDevice.TRANSPORT_LE)
            ?: error("无法启动 BLE 连接")
        try {
            withTimeout(15_000) { ready.await() }
        } catch (error: Throwable) {
            runCatching { gattRef?.close() }
            throw error
        }
        val gatt = gattRef ?: error("BLE 连接已关闭")
        val writerLock = Mutex()
        return object : Connection {
            override suspend fun send(bytes: ByteArray) = writerLock.withLock {
                val characteristic = writeChar ?: error("BLE 写入特征不可用")
                for (chunk in splitBleWrite(bytes, (mtu - 3).coerceAtLeast(1))) {
                    val completion = CompletableDeferred<Int>()
                    pendingWrite = completion
                    val result = if (Build.VERSION.SDK_INT >= 33) {
                        gatt.writeCharacteristic(characteristic, chunk, writeType(config.writeWithResponse))
                    } else {
                        @Suppress("DEPRECATION")
                        characteristic.writeType = writeType(config.writeWithResponse)
                        @Suppress("DEPRECATION")
                        characteristic.value = chunk
                        @Suppress("DEPRECATION")
                        if (gatt.writeCharacteristic(characteristic)) BluetoothGatt.GATT_SUCCESS else BluetoothGatt.GATT_FAILURE
                    }
                    if (result != BluetoothGatt.GATT_SUCCESS) error("BLE 写入未排队，状态码 $result")
                    if (result != BluetoothGatt.GATT_SUCCESS) pendingWrite = null
                    if (config.writeWithResponse) {
                        val status = withTimeout(5_000) { completion.await() }
                        if (status != BluetoothGatt.GATT_SUCCESS) error("BLE 写入失败，状态码 $status")
                    } else {
                        delay(15)
                        if (pendingWrite === completion) pendingWrite = null
                    }
                }
            }

            override suspend fun close() {
                if (closed.compareAndSet(false, true)) onClosed(null)
                withContext(Dispatchers.IO) { gatt.disconnect(); gatt.close() }
            }
        }
    }

    private fun requirePermission(permission: String, message: String) {
        if (Build.VERSION.SDK_INT >= 31 && context.checkSelfPermission(permission) != PackageManager.PERMISSION_GRANTED) throw SecurityException(message)
        if (Build.VERSION.SDK_INT in 23..30 && permission == Manifest.permission.BLUETOOTH_SCAN &&
            context.checkSelfPermission(Manifest.permission.ACCESS_FINE_LOCATION) != PackageManager.PERMISSION_GRANTED) throw SecurityException("请授予定位权限后扫描 BLE")
    }

    companion object {
        val NUS_SERVICE: UUID = UUID.fromString("6e400001-b5a3-f393-e0a9-e50e24dcca9e")
        val NUS_WRITE: UUID = UUID.fromString("6e400002-b5a3-f393-e0a9-e50e24dcca9e")
        val NUS_NOTIFY: UUID = UUID.fromString("6e400003-b5a3-f393-e0a9-e50e24dcca9e")
        private val CCCD_UUID: UUID = UUID.fromString("00002902-0000-1000-8000-00805f9b34fb")
    }
}

data class BleDeviceInfo(val address: String, val name: String)

fun splitBleWrite(bytes: ByteArray, payloadSize: Int): List<ByteArray> {
    require(payloadSize > 0) { "BLE 单次写入长度必须大于 0" }
    return bytes.asList().chunked(payloadSize).map { it.toByteArray() }
}

private fun writeType(withResponse: Boolean) = if (withResponse) BluetoothGattCharacteristic.WRITE_TYPE_DEFAULT else BluetoothGattCharacteristic.WRITE_TYPE_NO_RESPONSE
