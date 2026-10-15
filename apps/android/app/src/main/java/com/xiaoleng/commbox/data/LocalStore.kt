package com.xiaoleng.commbox.data

import android.content.ContentValues
import android.content.Context
import android.database.sqlite.SQLiteDatabase
import android.database.sqlite.SQLiteOpenHelper
import android.net.Uri
import com.xiaoleng.commbox.session.BleConfig
import com.xiaoleng.commbox.session.ConnectionConfig
import com.xiaoleng.commbox.session.Direction
import com.xiaoleng.commbox.session.PacketEvent
import com.xiaoleng.commbox.session.SppConfig
import com.xiaoleng.commbox.session.TcpConfig
import com.xiaoleng.commbox.session.TransportKind
import com.xiaoleng.commbox.session.UdpConfig
import com.xiaoleng.commbox.session.UsbConfig
import org.json.JSONObject
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

class LocalStore(context: Context) : SQLiteOpenHelper(context, "commbox.db", null, 1) {
    override fun onCreate(db: SQLiteDatabase) {
        db.execSQL("CREATE TABLE packets(id INTEGER PRIMARY KEY, timestamp_ms INTEGER NOT NULL, direction TEXT NOT NULL, transport TEXT NOT NULL, peer TEXT, bytes BLOB NOT NULL)")
        db.execSQL("CREATE TABLE profiles(name TEXT PRIMARY KEY, config TEXT NOT NULL, updated_ms INTEGER NOT NULL)")
    }

    override fun onUpgrade(db: SQLiteDatabase, oldVersion: Int, newVersion: Int) = Unit

    suspend fun record(event: PacketEvent) = withContext(Dispatchers.IO) {
        val db = writableDatabase
        db.beginTransaction()
        try {
            db.insertOrThrow("packets", null, ContentValues().apply {
                put("timestamp_ms", event.timestampMs)
                put("direction", event.direction.name)
                put("transport", event.transport.name)
                put("peer", event.peer)
                put("bytes", event.bytes)
            })
            while (true) {
                val count = db.rawQuery("SELECT COUNT(*), COALESCE(SUM(length(bytes)),0) FROM packets", null).use { cursor ->
                    cursor.moveToFirst(); cursor.getLong(0) to cursor.getLong(1)
                }
                if (count.first <= MAX_PACKETS && count.second <= MAX_BYTES) break
                db.execSQL("DELETE FROM packets WHERE id=(SELECT id FROM packets ORDER BY id LIMIT 1)")
            }
            db.setTransactionSuccessful()
        } finally { db.endTransaction() }
    }

    suspend fun recentPackets(limit: Int = 500): List<PacketEvent> = withContext(Dispatchers.IO) {
        val result = mutableListOf<PacketEvent>()
        readableDatabase.rawQuery(
            "SELECT timestamp_ms,direction,transport,peer,bytes FROM packets ORDER BY id DESC LIMIT ?",
            arrayOf(limit.coerceIn(0, MAX_PACKETS).toString()),
        ).use { cursor ->
            while (cursor.moveToNext()) result += PacketEvent(
                cursor.getLong(0), Direction.valueOf(cursor.getString(1)), TransportKind.valueOf(cursor.getString(2)),
                cursor.getBlob(4), if (cursor.isNull(3)) null else cursor.getString(3),
            )
        }
        result.reverse()
        result
    }

    suspend fun saveProfile(config: ConnectionConfig) = withContext(Dispatchers.IO) {
        val name = when (config) {
            is TcpConfig -> "TCP ${config.host}:${config.port}"
            is UdpConfig -> "UDP ${config.host}:${config.port}"
            is UsbConfig -> "USB ${config.deviceId}:${config.port} ${config.baud}"
            is SppConfig -> "SPP ${config.address}"
            is BleConfig -> "BLE ${config.address}"
            else -> error("不支持的连接配置")
        }
        writableDatabase.insertWithOnConflict("profiles", null, ContentValues().apply {
            put("name", name); put("config", encode(config)); put("updated_ms", System.currentTimeMillis())
        }, SQLiteDatabase.CONFLICT_REPLACE)
        name
    }

    suspend fun recentProfiles(limit: Int = 32): List<ConnectionConfig> = withContext(Dispatchers.IO) {
        val result = mutableListOf<ConnectionConfig>()
        readableDatabase.rawQuery("SELECT config FROM profiles ORDER BY updated_ms DESC LIMIT ?", arrayOf(limit.coerceIn(0, 128).toString())).use { cursor ->
            while (cursor.moveToNext()) runCatching { decode(cursor.getString(0)) }.getOrNull()?.let(result::add)
        }
        result
    }

    suspend fun export(uri: Uri, context: Context) = withContext(Dispatchers.IO) {
        val events = recentPackets(MAX_PACKETS)
        context.contentResolver.openOutputStream(uri)?.bufferedWriter(Charsets.UTF_8)?.use { writer ->
            writer.appendLine("timestamp_ms,direction,transport,peer,hex")
            events.forEach { event ->
                val peer = event.peer.orEmpty().replace("\"", "\"\"")
                val hex = event.bytes.joinToString("") { "%02X".format(it) }
                writer.appendLine("${event.timestampMs},${event.direction},${event.transport},\"$peer\",$hex")
            }
        } ?: error("无法创建导出文件")
    }

    private fun encode(config: ConnectionConfig): String = JSONObject().apply {
        when (config) {
            is TcpConfig -> put("type", "tcp").put("host", config.host).put("port", config.port).put("listen", config.listen).put("reconnect", config.autoReconnect)
            is UdpConfig -> put("type", "udp").put("host", config.host).put("port", config.port).put("listen", config.listen)
            is UsbConfig -> put("type", "usb").put("device", config.deviceId).put("port", config.port).put("baud", config.baud).put("bits", config.dataBits).put("stop", config.stopBits).put("parity", config.parity)
            is SppConfig -> put("type", "spp").put("address", config.address)
            is BleConfig -> put("type", "ble").put("address", config.address).put("service", config.serviceUuid).put("write", config.writeUuid).put("notify", config.notifyUuid).put("response", config.writeWithResponse)
            else -> error("不支持的连接配置")
        }
    }.toString()

    private fun decode(raw: String): ConnectionConfig {
        val json = JSONObject(raw)
        return when (json.getString("type")) {
            "tcp" -> TcpConfig(json.getString("host"), json.getInt("port"), json.getBoolean("listen"), json.getBoolean("reconnect"))
            "udp" -> UdpConfig(json.getString("host"), json.getInt("port"), json.getBoolean("listen"))
            "usb" -> UsbConfig(json.getInt("device"), json.getInt("port"), json.getInt("baud"), json.getInt("bits"), json.getInt("stop"), json.getInt("parity"))
            "spp" -> SppConfig(json.getString("address"))
            "ble" -> BleConfig(json.getString("address"), json.getString("service"), json.getString("write"), json.getString("notify"), json.getBoolean("response"))
            else -> error("未知连接类型")
        }
    }

    companion object {
        const val MAX_PACKETS = 10_000
        const val MAX_BYTES = 32L * 1024 * 1024
    }
}
