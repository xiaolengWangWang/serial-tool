package com.xiaoleng.commbox

import android.os.Bundle
import android.Manifest
import android.os.Build
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.result.contract.ActivityResultContracts
import android.app.Application
import androidx.activity.viewModels
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.ime
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.Button
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Card
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalConfiguration
import android.hardware.usb.UsbDevice
import android.hardware.usb.UsbManager
import android.net.Uri
import com.xiaoleng.commbox.protocol.Modbus
import com.xiaoleng.commbox.data.LocalStore
import com.xiaoleng.commbox.session.PacketEvent
import com.xiaoleng.commbox.session.BleConfig
import com.xiaoleng.commbox.session.SppConfig
import com.xiaoleng.commbox.session.UsbConfig
import com.xiaoleng.commbox.session.SessionController
import com.xiaoleng.commbox.session.SessionState
import com.xiaoleng.commbox.session.TcpConfig
import com.xiaoleng.commbox.session.UdpConfig
import com.xiaoleng.commbox.transport.AndroidConnections
import com.xiaoleng.commbox.transport.BleDeviceInfo
import com.xiaoleng.commbox.transport.BluetoothDeviceInfo
import com.xiaoleng.commbox.update.AndroidApkInstaller
import com.xiaoleng.commbox.update.ApkUpdater
import com.xiaoleng.commbox.update.SafeUpdateHttp
import com.xiaoleng.commbox.update.UpdateInfo
import java.io.File
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.SupervisorJob

class MainActivity : ComponentActivity() {
    private val model: SessionViewModel by viewModels()
    private var pendingBluetoothConfig: com.xiaoleng.commbox.session.ConnectionConfig? = null
    private var pendingBleScan = false
    private var pendingSppScan = false
    private val bluetoothPermission = registerForActivityResult(ActivityResultContracts.RequestMultiplePermissions()) { result ->
        if (pendingBleScan) {
            pendingBleScan = false
            if (result.values.all { it }) model.scanBle() else model.bluetoothPermissionDenied()
            return@registerForActivityResult
        }
        if (pendingSppScan) {
            pendingSppScan = false
            if (result.values.all { it }) model.scanSpp() else model.bluetoothPermissionDenied()
            return@registerForActivityResult
        }
        val config = pendingBluetoothConfig ?: return@registerForActivityResult
        pendingBluetoothConfig = null
        // Invoke the connection path after either result so a denial appears as a readable state error.
        model.connect(config)
    }
    private val exportDocument = registerForActivityResult(ActivityResultContracts.CreateDocument("text/csv")) { uri -> if (uri != null) model.export(uri) }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContent {
            MaterialTheme {
                val state by model.controller.state.collectAsState()
                val packets by model.packets.collectAsState()
                val updateStatus by model.updateStatus.collectAsState()
                val updateAvailable by model.updateAvailable.collectAsState()
                val bleDevices by model.bleDevices.collectAsState()
                val sppDevices by model.sppDevices.collectAsState()
                val recentProfiles by model.recentProfiles.collectAsState()
                CommBoxScreen(
                    state = state,
                    packets = packets,
                    onConnect = ::requestConnect,
                    onDisconnect = model::disconnect,
                    onSend = model::send,
                    onCheckUpdate = model::checkUpdate,
                    onInstallUpdate = model::installUpdate,
                    onExport = { exportDocument.launch("CommBox-capture.csv") },
                    updateStatus = updateStatus,
                    updateAvailable = updateAvailable != null,
                    bleDevices = bleDevices,
                    onScanBle = ::requestBleScan,
                    sppDevices = sppDevices,
                    onScanSpp = ::requestSppScan,
                    recentProfiles = recentProfiles,
                )
            }
        }
    }

    private fun requestConnect(config: com.xiaoleng.commbox.session.ConnectionConfig) {
        if (Build.VERSION.SDK_INT >= 31 && (config is SppConfig || config is BleConfig) &&
            checkSelfPermission(Manifest.permission.BLUETOOTH_CONNECT) != android.content.pm.PackageManager.PERMISSION_GRANTED) {
            pendingBluetoothConfig = config
            bluetoothPermission.launch(arrayOf(Manifest.permission.BLUETOOTH_CONNECT))
        } else model.connect(config)
    }

    private fun requestBleScan() {
        if (Build.VERSION.SDK_INT >= 31) {
            pendingBleScan = true
            bluetoothPermission.launch(arrayOf(Manifest.permission.BLUETOOTH_CONNECT, Manifest.permission.BLUETOOTH_SCAN))
        } else if (Build.VERSION.SDK_INT >= 23 && checkSelfPermission(Manifest.permission.ACCESS_FINE_LOCATION) != android.content.pm.PackageManager.PERMISSION_GRANTED) {
            pendingBleScan = true
            bluetoothPermission.launch(arrayOf(Manifest.permission.ACCESS_FINE_LOCATION))
        } else model.scanBle()
    }

    private fun requestSppScan() {
        if (Build.VERSION.SDK_INT >= 31 && checkSelfPermission(Manifest.permission.BLUETOOTH_CONNECT) != android.content.pm.PackageManager.PERMISSION_GRANTED) {
            pendingSppScan = true
            bluetoothPermission.launch(arrayOf(Manifest.permission.BLUETOOTH_CONNECT))
        } else model.scanSpp()
    }

}

class SessionViewModel(application: Application) : AndroidViewModel(application) {
    private val store = LocalStore(application)
    private val connections = AndroidConnections(application, viewModelScope)
    private val mutablePackets = MutableStateFlow<List<PacketEvent>>(emptyList())
    val packets = mutablePackets.asStateFlow()
    private val mutableUpdateStatus = MutableStateFlow("")
    val updateStatus = mutableUpdateStatus.asStateFlow()
    private val mutableUpdateAvailable = MutableStateFlow<UpdateInfo?>(null)
    val updateAvailable = mutableUpdateAvailable.asStateFlow()
    private val mutableBleDevices = MutableStateFlow<List<BleDeviceInfo>>(emptyList())
    val bleDevices = mutableBleDevices.asStateFlow()
    private val mutableSppDevices = MutableStateFlow<List<BluetoothDeviceInfo>>(emptyList())
    val sppDevices = mutableSppDevices.asStateFlow()
    private val mutableRecentProfiles = MutableStateFlow<List<com.xiaoleng.commbox.session.ConnectionConfig>>(emptyList())
    val recentProfiles = mutableRecentProfiles.asStateFlow()
    val controller = SessionController(connections, onPacket = { packet ->
        mutablePackets.value = (listOf(packet) + mutablePackets.value).take(500)
        viewModelScope.launch(Dispatchers.IO) { runCatching { store.record(packet) } }
    })

    init { viewModelScope.launch { mutablePackets.value = store.recentPackets(); mutableRecentProfiles.value = store.recentProfiles() } }

    fun connect(config: com.xiaoleng.commbox.session.ConnectionConfig) { viewModelScope.launch {
        runCatching { store.saveProfile(config); mutableRecentProfiles.value = store.recentProfiles() }
        controller.connect(config)
    } }
    fun disconnect() { viewModelScope.launch { controller.disconnect() } }
    fun send(bytes: ByteArray) { viewModelScope.launch { runCatching { controller.send(bytes) }.onFailure { mutableUpdateStatus.value = "发送失败：${it.message}" } } }
    fun export(uri: Uri) { viewModelScope.launch { runCatching { store.export(uri, getApplication()) }.onFailure { mutableUpdateStatus.value = "导出失败：${it.message}" } } }
    fun scanBle() { viewModelScope.launch {
        mutableUpdateStatus.value = "正在扫描 BLE 设备…"
        runCatching { connections.scanBle() }.onSuccess { devices -> mutableBleDevices.value = devices; mutableUpdateStatus.value = "BLE 扫描完成：${devices.size} 台设备" }
            .onFailure { mutableUpdateStatus.value = "BLE 扫描失败：${it.message}" }
    } }
    fun scanSpp() { runCatching { mutableSppDevices.value = connections.pairedSppDevices() }
        .onFailure { mutableUpdateStatus.value = "读取配对设备失败：${it.message}" } }
    fun bluetoothPermissionDenied() { mutableUpdateStatus.value = "蓝牙权限被拒绝，请在系统设置中允许后重试" }

    override fun onCleared() {
        CoroutineScope(SupervisorJob() + Dispatchers.IO).launch { controller.disconnect(); store.close() }
        super.onCleared()
    }
    fun checkUpdate() { viewModelScope.launch {
        mutableUpdateStatus.value = "正在检查更新…"
        runCatching {
            val version = getApplication<Application>().packageManager.getPackageInfo(getApplication<Application>().packageName, 0).versionName ?: "0.1.0"
            ApkUpdater(File(getApplication<Application>().cacheDir, "apk_updates"), SafeUpdateHttp()).check(version)
        }.onSuccess { info ->
            mutableUpdateAvailable.value = info
            mutableUpdateStatus.value = if (info == null) "当前已是最新版" else "发现新版本 ${info.version}"
        }.onFailure { mutableUpdateStatus.value = "检查更新失败：${it.message}" }
    } }
    fun installUpdate() { viewModelScope.launch {
        val info = mutableUpdateAvailable.value ?: return@launch
        mutableUpdateStatus.value = "正在下载并校验 APK…"
        runCatching {
            val apk = ApkUpdater(File(getApplication<Application>().cacheDir, "apk_updates"), SafeUpdateHttp()).downloadVerified(info)
            AndroidApkInstaller(getApplication()).install(apk)
        }.onSuccess { launched -> mutableUpdateStatus.value = if (launched) "校验完成，已打开系统安装器" else "请授权 CommBox 安装应用，授权后再次点击安装" }
            .onFailure { mutableUpdateStatus.value = "更新失败：${it.message}" }
    } }
}

private fun com.xiaoleng.commbox.session.ConnectionConfig.label(): String = when (this) {
    is TcpConfig -> "TCP $host:$port"
    is UdpConfig -> "UDP $host:$port"
    is UsbConfig -> "USB $deviceId:$port"
    is SppConfig -> "SPP $address"
    is BleConfig -> "BLE $address"
    else -> "连接配置"
}

@Composable
private fun CommBoxScreen(
    state: SessionState,
    packets: List<PacketEvent>,
    onConnect: (com.xiaoleng.commbox.session.ConnectionConfig) -> Unit,
    onDisconnect: () -> Unit,
    onSend: (ByteArray) -> Unit,
    onCheckUpdate: () -> Unit,
    onInstallUpdate: () -> Unit,
    onExport: () -> Unit,
    updateStatus: String,
    updateAvailable: Boolean,
    bleDevices: List<BleDeviceInfo>,
    onScanBle: () -> Unit,
    sppDevices: List<BluetoothDeviceInfo>,
    onScanSpp: () -> Unit,
    recentProfiles: List<com.xiaoleng.commbox.session.ConnectionConfig>,
) {
    var host by rememberSaveable { mutableStateOf("192.168.1.10") }
    var usbDeviceId by rememberSaveable { mutableStateOf("") }
    var sppAddress by rememberSaveable { mutableStateOf("") }
    var bleAddress by rememberSaveable { mutableStateOf("") }
    var port by rememberSaveable { mutableStateOf("502") }
    var transport by rememberSaveable { mutableStateOf("TCP") }
    var listen by rememberSaveable { mutableStateOf(false) }
    var autoReconnect by rememberSaveable { mutableStateOf(false) }
    var serialBaud by rememberSaveable { mutableStateOf("115200") }
    var serialPort by rememberSaveable { mutableStateOf("0") }
    var serialDataBits by rememberSaveable { mutableStateOf(8) }
    var serialStopBits by rememberSaveable { mutableStateOf(1) }
    var serialParity by rememberSaveable { mutableStateOf(0) }
    var modbusAddress by rememberSaveable { mutableStateOf("0") }
    var modbusQuantity by rememberSaveable { mutableStateOf("1") }
    var modbusValues by rememberSaveable { mutableStateOf("1,2") }
    var bleService by rememberSaveable { mutableStateOf(com.xiaoleng.commbox.transport.BleConnectionFactory.NUS_SERVICE.toString()) }
    var bleWrite by rememberSaveable { mutableStateOf(com.xiaoleng.commbox.transport.BleConnectionFactory.NUS_WRITE.toString()) }
    var bleNotify by rememberSaveable { mutableStateOf(com.xiaoleng.commbox.transport.BleConnectionFactory.NUS_NOTIFY.toString()) }
    var input by rememberSaveable { mutableStateOf("") }
    var hexMode by rememberSaveable { mutableStateOf(false) }
    var captureHexMode by rememberSaveable { mutableStateOf(false) }
    var textEncodingName by rememberSaveable { mutableStateOf(TextEncoding.UTF8.name) }
    val textEncoding = TextEncoding.valueOf(textEncodingName)
    var errorText by rememberSaveable { mutableStateOf("") }
    var activePage by rememberSaveable { mutableStateOf("连接") }
    var helpTopic by rememberSaveable { mutableStateOf<String?>(null) }
    var showBleUuids by rememberSaveable { mutableStateOf(false) }
    var usbDevices by remember { mutableStateOf(emptyList<UsbDevice>()) }
    val connected = state is SessionState.Connected || state is SessionState.Reconnecting
    val context = LocalContext.current
    val configuration = LocalConfiguration.current
    fun previewModbus(function: Int) {
        runCatching {
            val address = modbusAddress.toInt()
            val quantity = modbusQuantity.toInt()
            val values = if (function == 0x06 || function == 0x10) modbusValues.split(',').map { it.trim().toInt() } else emptyList()
            when (transport) {
                "TCP" -> Modbus.buildTcp(1, 1, function, address, values, if (function == 0x06) 1 else quantity)
                "USB", "SPP", "BLE" -> Modbus.buildRtu(1, function, address, values, if (function == 0x06) 1 else quantity)
                else -> error("Modbus 预置指令适用于 TCP 或串口链路")
            }
        }.onSuccess { frame ->
            input = frame.joinToString(" ") { "%02X".format(it) }
            hexMode = true
            errorText = ""
            activePage = "收发"
        }.onFailure { errorText = it.message ?: "Modbus 参数无效" }
    }

    MaterialTheme {
        Scaffold { inset ->
            BoxWithConstraints(Modifier.fillMaxSize().padding(inset).imePadding().padding(12.dp)) {
                // Keep the layout mode stable while the keyboard resizes the window.
                val twoPane = remember(configuration.orientation, configuration.screenWidthDp) {
                    maxWidth >= 700.dp && maxHeight >= 500.dp
                }
                val keyboardVisible = WindowInsets.ime.getBottom(LocalDensity.current) > 0
                val compactComposer = keyboardVisible || maxHeight < 450.dp
                val connectionPanel: @Composable () -> Unit = {
                    Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                        Text("CommBox", style = MaterialTheme.typography.headlineMedium)
                        Text(when (state) {
                            SessionState.Disconnected -> "未连接"
                            is SessionState.Connecting -> "正在连接…"
                            is SessionState.Connected -> "已连接 · ${state.config}"
                            is SessionState.Reconnecting -> "连接中断，正在重连… · ${state.config}"
                            is SessionState.Failed -> "连接失败 · ${state.message}"
                        })
                        Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                            listOf("TCP", "UDP", "USB", "SPP", "BLE").forEach { kind ->
                                OutlinedButton(onClick = { transport = kind }) { Text(if (transport == kind) "✓ $kind" else kind) }
                            }
                            if (transport == "TCP" || transport == "UDP") OutlinedButton(onClick = { listen = !listen }) { Text(if (listen) "监听" else "客户端") }
                            if (transport == "TCP" && !listen) OutlinedButton(onClick = { autoReconnect = !autoReconnect }) { Text(if (autoReconnect) "✓ 自动重连" else "自动重连") }
                        }
                        if (transport == "TCP" || transport == "UDP") OutlinedTextField(host, { host = it }, Modifier.fillMaxWidth(), label = { Text("网络地址") }, singleLine = true)
                        if (transport == "TCP" || transport == "UDP") OutlinedTextField(port, { port = it.filter(Char::isDigit).take(5) }, Modifier.fillMaxWidth(), label = { Text("端口") }, singleLine = true)
                        if (transport == "USB") {
                            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                                Text("USB 转串口设备", style = MaterialTheme.typography.titleMedium)
                                TextButton(onClick = { helpTopic = "USB 设备 ID" }) { Text("？") }
                            }
                            Text("插入 OTG 设备后刷新并选择；设备 ID 会自动填入。")
                            OutlinedButton(onClick = {
                                usbDevices = context.getSystemService(UsbManager::class.java).deviceList.values.toList()
                            }, modifier = Modifier.fillMaxWidth()) { Text("刷新 USB 设备") }
                            if (usbDevices.isEmpty()) Text("尚未发现 USB 设备。请检查 OTG 连接和手机的 USB 权限。")
                            usbDevices.forEach { device ->
                                val id = device.deviceId.toString()
                                OutlinedButton(onClick = { usbDeviceId = id }, modifier = Modifier.fillMaxWidth()) {
                                    Column(Modifier.fillMaxWidth()) {
                                        Text("${if (usbDeviceId == id) "✓ " else ""}${device.productName ?: "USB 设备"} · ID $id")
                                        Text("VID ${device.vendorId} · PID ${device.productId}", style = MaterialTheme.typography.bodySmall)
                                    }
                                }
                            }
                            OutlinedTextField(usbDeviceId, {}, Modifier.fillMaxWidth(), label = { Text("已选 USB 设备 ID") }, readOnly = true, singleLine = true)
                            OutlinedTextField(serialPort, { serialPort = it.filter(Char::isDigit) }, Modifier.fillMaxWidth(), label = { Text("串口号") }, singleLine = true)
                            OutlinedTextField(serialBaud, { serialBaud = it.filter(Char::isDigit).take(7) }, Modifier.fillMaxWidth(), label = { Text("波特率") }, singleLine = true)
                            Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                                listOf(5, 6, 7, 8).forEach { bits -> OutlinedButton(onClick = { serialDataBits = bits }) { Text("${if (serialDataBits == bits) "✓ " else ""}${bits}位") } }
                                listOf(1, 2).forEach { bits -> OutlinedButton(onClick = { serialStopBits = bits }) { Text("${if (serialStopBits == bits) "✓ " else ""}${bits}停止位") } }
                                listOf(0 to "无校验", 1 to "奇校验", 2 to "偶校验").forEach { (value, label) -> OutlinedButton(onClick = { serialParity = value }) { Text(if (serialParity == value) "✓ $label" else label) } }
                            }
                        }
                        if (transport == "BLE") {
                            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                                Text("BLE 低功耗蓝牙", style = MaterialTheme.typography.titleMedium)
                                TextButton(onClick = { helpTopic = "BLE" }) { Text("？") }
                            }
                            Text("扫描并选择支持 BLE UART 的设备。")
                            OutlinedButton(onClick = onScanBle, modifier = Modifier.fillMaxWidth()) { Text("扫描 BLE 设备") }
                            if (bleDevices.isEmpty()) Text("暂无扫描结果，点上方按钮开始扫描。")
                            bleDevices.forEach { device ->
                                OutlinedButton(onClick = { bleAddress = device.address }, modifier = Modifier.fillMaxWidth()) {
                                    Column(Modifier.fillMaxWidth()) {
                                        Text("${if (bleAddress == device.address) "✓ " else ""}${device.name.ifBlank { "未命名 BLE 设备" }}")
                                        Text(device.address, style = MaterialTheme.typography.bodySmall)
                                    }
                                }
                            }
                            OutlinedTextField(bleAddress, { bleAddress = it }, Modifier.fillMaxWidth(), label = { Text("已选 BLE 地址（也可手动输入）") }, singleLine = true)
                            OutlinedButton(onClick = { showBleUuids = !showBleUuids }, modifier = Modifier.fillMaxWidth()) { Text(if (showBleUuids) "收起 UUID 设置" else "高级设置：服务与特征 UUID") }
                            if (showBleUuids) {
                                Text("默认使用 Nordic UART Service；其他设备请按外设说明修改。")
                                OutlinedTextField(bleService, { bleService = it }, Modifier.fillMaxWidth(), label = { Text("服务 UUID") }, singleLine = true)
                                OutlinedTextField(bleWrite, { bleWrite = it }, Modifier.fillMaxWidth(), label = { Text("写入 UUID") }, singleLine = true)
                                OutlinedTextField(bleNotify, { bleNotify = it }, Modifier.fillMaxWidth(), label = { Text("通知 UUID") }, singleLine = true)
                            }
                        }
                        if (transport == "SPP") {
                            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                                Text("SPP 经典蓝牙串口", style = MaterialTheme.typography.titleMedium)
                                TextButton(onClick = { helpTopic = "SPP" }) { Text("？") }
                            }
                            Text("先在系统设置中配对，再从下方选择设备。")
                            OutlinedButton(onClick = onScanSpp, modifier = Modifier.fillMaxWidth()) { Text("读取已配对设备") }
                            if (sppDevices.isEmpty()) Text("暂无设备。请先完成系统蓝牙配对。")
                            sppDevices.forEach { device ->
                                OutlinedButton(onClick = { sppAddress = device.address }, modifier = Modifier.fillMaxWidth()) {
                                    Column(Modifier.fillMaxWidth()) {
                                        Text("${if (sppAddress == device.address) "✓ " else ""}${device.name.ifBlank { "未命名 SPP 设备" }}")
                                        Text(device.address, style = MaterialTheme.typography.bodySmall)
                                    }
                                }
                            }
                            OutlinedTextField(sppAddress, { sppAddress = it }, Modifier.fillMaxWidth(), label = { Text("已选 SPP 地址（也可手动输入）") }, singleLine = true)
                        }
                        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                            Button(enabled = !connected && state !is SessionState.Connecting, onClick = {
                                errorText = ""
                                runCatching {
                                    when (transport) {
                                        "TCP", "UDP" -> {
                                            val p = port.toIntOrNull() ?: error("端口范围为 1–65535")
                                            require(p in 1..65535) { "端口范围为 1–65535" }
                                            if (transport == "TCP") TcpConfig(host, p, listen, autoReconnect && !listen) else UdpConfig(host, p, listen)
                                        }
                                        "USB" -> UsbConfig(usbDeviceId.toIntOrNull() ?: error("请选择 USB 设备"), serialPort.toIntOrNull() ?: 0, serialBaud.toIntOrNull() ?: error("波特率无效"), serialDataBits, serialStopBits, serialParity)
                                        "SPP" -> SppConfig(sppAddress.takeIf(String::isNotBlank) ?: error("请选择 SPP 设备"))
                                        else -> BleConfig(bleAddress.takeIf(String::isNotBlank) ?: error("请选择 BLE 设备"), bleService, bleWrite, bleNotify, true)
                                    }
                                }.onSuccess { onConnect(it); activePage = "收发" }.onFailure { errorText = it.message ?: "连接参数无效" }
                            }) { Text("连接") }
                            OutlinedButton(enabled = connected, onClick = onDisconnect) { Text("断开") }
                        }
                        if (recentProfiles.isNotEmpty()) {
                            Text("最近连接")
                            Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                                recentProfiles.forEach { profile ->
                                    OutlinedButton(onClick = { onConnect(profile); activePage = "收发" }) { Text(profile.label()) }
                                }
                            }
                        }
                        if (transport != "UDP") {
                            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                                OutlinedTextField(modbusAddress, { modbusAddress = it.filter(Char::isDigit).take(5) }, Modifier.weight(1f), label = { Text("寄存器地址") }, singleLine = true)
                                OutlinedTextField(modbusQuantity, { modbusQuantity = it.filter(Char::isDigit).take(3) }, Modifier.weight(1f), label = { Text("数量") }, singleLine = true)
                                OutlinedTextField(modbusValues, { modbusValues = it.filter { ch -> ch.isDigit() || ch == ',' }.take(40) }, Modifier.weight(1f), label = { Text("写入值,值…") }, singleLine = true)
                            }
                            Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                                listOf(0x03, 0x04, 0x06, 0x10).forEach { code ->
                                    OutlinedButton(onClick = { previewModbus(code) }) { Text("功能码 %02X".format(code)) }
                                }
                            }
                        }
                        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                            OutlinedButton(onClick = onCheckUpdate) { Text("检查更新") }
                            if (updateAvailable) Button(onClick = onInstallUpdate) { Text("下载并安装 APK") }
                        }
                        if (updateStatus.isNotBlank()) Text(updateStatus)
                        if (errorText.isNotBlank()) Text(errorText, color = MaterialTheme.colorScheme.error)
                    }
                }
                val capturePanel: @Composable () -> Unit = {
                    Column(Modifier.fillMaxSize().then(if (compactComposer) Modifier.verticalScroll(rememberScrollState()) else Modifier), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                        Text(when (state) {
                            SessionState.Disconnected -> "未连接"
                            is SessionState.Connecting -> "正在连接…"
                            is SessionState.Connected -> "已连接 · ${state.config.label()}"
                            is SessionState.Reconnecting -> "连接中断，正在重连…"
                            is SessionState.Failed -> "连接失败 · ${state.message}"
                        }, style = MaterialTheme.typography.bodySmall)
                        if (!compactComposer) {
                            Text("收发记录 · ${packets.size}", style = MaterialTheme.typography.titleMedium)
                            OutlinedButton(onClick = onExport) { Text("导出记录") }
                            LazyColumn(Modifier.weight(1f), reverseLayout = false) {
                                items(packets) { packet ->
                                    Card(Modifier.fillMaxWidth().padding(vertical = 3.dp)) {
                                        val content = if (captureHexMode) packet.bytes.joinToString(" ") { "%02X".format(it) } else textEncoding.decode(packet.bytes).replace("\u0000", "␀")
                                        Text("${packet.direction} ${packet.peer.orEmpty()}  $content", Modifier.padding(10.dp))
                                    }
                                }
                            }
                        }
                        Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                            OutlinedButton(onClick = { captureHexMode = !captureHexMode }) { Text(if (captureHexMode) "收发 HEX" else "收发文本") }
                            OutlinedButton(onClick = { hexMode = !hexMode }) { Text(if (hexMode) "HEX" else "文本") }
                            OutlinedButton(onClick = { textEncodingName = if (textEncoding == TextEncoding.UTF8) TextEncoding.GBK.name else TextEncoding.UTF8.name }) {
                                Text("中文编码：${textEncoding.label}")
                            }
                        }
                        OutlinedTextField(input, { input = it }, Modifier.fillMaxWidth().heightIn(min = if (compactComposer) 72.dp else 120.dp), label = { Text("发送内容（支持中文）") }, minLines = if (compactComposer) 2 else 4, maxLines = 7)
                        Button(enabled = connected, modifier = Modifier.fillMaxWidth(), onClick = {
                            runCatching {
                                if (hexMode) input.split(Regex("[\\s,;]+" )).filter(String::isNotBlank).map {
                                    val value = it.toInt(16)
                                    require(value in 0..255) { "每个 HEX 字节必须为 00–FF" }
                                    value.toByte()
                                }.toByteArray()
                                else textEncoding.encode(input)
                            }.onSuccess(onSend).onFailure { errorText = it.message ?: "HEX 格式错误" }
                        }) { Text("发送") }
                        if (updateStatus.isNotBlank()) Text(updateStatus)
                        if (errorText.isNotBlank()) Text(errorText, color = MaterialTheme.colorScheme.error)
                    }
                }
                if (twoPane) Row(Modifier.fillMaxSize(), horizontalArrangement = Arrangement.spacedBy(20.dp)) {
                    Column(Modifier.weight(0.38f).fillMaxSize().verticalScroll(rememberScrollState())) { connectionPanel() }
                    Column(Modifier.weight(0.62f)) { capturePanel() }
                } else Column(Modifier.fillMaxSize(), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        listOf("连接", "收发").forEach { page ->
                            OutlinedButton(onClick = { activePage = page }, modifier = Modifier.weight(1f)) { Text(if (activePage == page) "✓ $page" else page) }
                        }
                    }
                    if (activePage == "连接") Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState())) { connectionPanel() }
                    else capturePanel()
                }
            }
            if (helpTopic != null) AlertDialog(
                onDismissRequest = { helpTopic = null },
                title = { Text(helpTopic.orEmpty()) },
                text = { Text(when (helpTopic) {
                    "USB 设备 ID" -> "设备 ID 是 Android 为当前插入的 USB 设备分配的数字编号，重新插拔后可能改变。插入 OTG 转串口设备，点“刷新 USB 设备”并选择即可；VID/PID 是厂商和产品编号。它不是 Modbus 从站地址。"
                    "SPP" -> "SPP 是经典蓝牙串口。先在手机系统蓝牙设置中完成配对，再点“读取已配对设备”并选择。远端设备需要支持 SPP；选择后会自动填入蓝牙地址。"
                    else -> "BLE 是低功耗蓝牙。点“扫描 BLE 设备”并选择支持 UART 服务的设备。默认使用 Nordic UART Service；其他设备需要按外设说明修改服务、写入和通知 UUID。BLE 设备不一定支持串口收发。"
                }) },
                confirmButton = { TextButton(onClick = { helpTopic = null }) { Text("知道了") } },
            )
        }
    }
}
