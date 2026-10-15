package com.xiaoleng.commbox.update

import java.io.File
import java.net.URL
import java.security.MessageDigest
import org.json.JSONObject
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

data class UpdateInfo(val version: String, val name: String, val downloadUrl: String, val sha256: String)

interface UpdateHttp {
    suspend fun getJson(url: String): String
    suspend fun download(url: String, dest: File)
}

class ApkUpdater(private val cacheDir: File, private val http: UpdateHttp) {
    suspend fun check(currentVersion: String): UpdateInfo? {
        val candidates = mutableListOf<UpdateInfo>()
        var successfulSource = false
        var lastError: Throwable? = null
        for (api in RELEASE_APIS) {
            try {
                val json = http.getJson(api)
                val info = parseRelease(json, currentVersion)
                successfulSource = true
                info?.let(candidates::add)
            } catch (error: Throwable) {
                if (error is CancellationException) throw error
                lastError = error
            }
        }
        if (!successfulSource) throw IllegalStateException("无法连接更新服务器", lastError)
        return candidates.filter { compareVersions(it.version, currentVersion) > 0 }
            .reduceOrNull { best, next ->
                val order = compareVersions(next.version, best.version)
                if (order > 0 || order == 0 && best.sha256.isBlank() && next.sha256.isNotBlank()) next else best
            }
    }

    suspend fun downloadVerified(info: UpdateInfo): File = withContext(Dispatchers.IO) {
        require(info.version.isNotBlank() && info.name.matches(Regex("CommBox-[A-Za-z0-9.+_-]+-Android\\.apk"))) { "APK 名称无效" }
        require(allowedUrl(info.downloadUrl)) { "下载地址不受信任" }
        require(info.sha256.matches(Regex("(?i)[0-9a-f]{64}"))) { "发布信息缺少有效 SHA256" }
        require(cacheDir.mkdirs() || cacheDir.isDirectory) { "无法创建 APK 缓存目录" }
        val target = File(cacheDir, info.name)
        target.delete()
        try {
            http.download(info.downloadUrl, target)
            require(target.isFile && target.length() in 1..MAX_APK_BYTES) { "APK 下载不完整或文件过大" }
            val actual = target.inputStream().use { input ->
                val digest = MessageDigest.getInstance("SHA-256")
                val buffer = ByteArray(16 * 1024)
                while (true) {
                    val count = input.read(buffer)
                    if (count < 0) break
                    digest.update(buffer, 0, count)
                }
                digest.digest().joinToString("") { "%02x".format(it) }
            }
            require(actual.equals(info.sha256, ignoreCase = true)) { "APK SHA256 校验失败" }
            target
        } catch (error: Throwable) {
            target.delete()
            throw error
        }
    }

    companion object {
        const val GITHUB_RELEASES = "https://api.github.com/repos/xiaolengWangWang/serial-tool/releases/latest"
        const val GITEE_RELEASES = "https://gitee.com/api/v5/repos/sun_xuanqi/serial-tool/releases/latest"
        private const val MAX_APK_BYTES = 150L * 1024 * 1024
        private val RELEASE_APIS = listOf(GITEE_RELEASES, GITHUB_RELEASES)

        fun parseRelease(json: String, currentVersion: String): UpdateInfo? {
            val release = JSONObject(json)
            val version = release.optString("tag_name").removePrefix("v").trim()
            if (version.isBlank() || compareVersions(version, currentVersion) <= 0) return null
            val notes = release.optString("body")
            val assets = release.optJSONArray("assets") ?: return null
            for (index in 0 until assets.length()) {
                val asset = assets.optJSONObject(index) ?: continue
                val name = asset.optString("name")
                if (!name.endsWith("-Android.apk")) continue
                val url = asset.optString("browser_download_url").ifBlank { asset.optString("html_url") }
                if (!allowedUrl(url)) continue
                val escaped = Regex.escape(name)
                val hash = Regex("(?im)^\\s*([0-9a-f]{64})\\s+\\*?$escaped\\s*$").find(notes)?.groupValues?.get(1) ?: ""
                return UpdateInfo(version, name, url, hash)
            }
            return null
        }

        fun compareVersions(left: String, right: String): Int {
            val leftParts = left.removePrefix("v").split('-', limit = 2)
            val rightParts = right.removePrefix("v").split('-', limit = 2)
            val a = leftParts[0].substringBefore('+').split('.')
            val b = rightParts[0].substringBefore('+').split('.')
            for (index in 0 until maxOf(a.size, b.size)) {
                val x = a.getOrNull(index)?.toLongOrNull() ?: 0L
                val y = b.getOrNull(index)?.toLongOrNull() ?: 0L
                if (x != y) return x.compareTo(y)
            }
            val leftPre = leftParts.getOrNull(1)?.substringBefore('+')
            val rightPre = rightParts.getOrNull(1)?.substringBefore('+')
            return when {
                leftPre == rightPre -> 0
                leftPre == null -> 1
                rightPre == null -> -1
                else -> leftPre.compareTo(rightPre, ignoreCase = true)
            }
        }

        fun allowedUrl(raw: String): Boolean = runCatching {
            val url = URL(raw)
            url.protocol == "https" && (url.port == -1 || url.port == 443) && listOf("github.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com", "gitee.com")
                .any { url.host.equals(it, true) || url.host.endsWith(".$it", true) }
        }.getOrDefault(false)
    }
}

class SafeUpdateHttp : UpdateHttp {
    override suspend fun getJson(url: String): String = withContext(Dispatchers.IO) {
        require(ApkUpdater.allowedUrl(url)) { "更新地址不受信任" }
        val connection = URL(url).openConnection() as java.net.HttpURLConnection
        connection.connectTimeout = 10_000
        connection.readTimeout = 15_000
        connection.setRequestProperty("Accept", "application/json")
        connection.inputStream.use { input ->
            val output = java.io.ByteArrayOutputStream()
            val buffer = ByteArray(8192)
            while (output.size() < 4 * 1024 * 1024) {
                val count = input.read(buffer, 0, minOf(buffer.size, 4 * 1024 * 1024 - output.size()))
                if (count < 0) break
                output.write(buffer, 0, count)
            }
            output.toString(Charsets.UTF_8.name())
        }
    }

    override suspend fun download(url: String, dest: File) = withContext(Dispatchers.IO) {
        var current = url
        repeat(6) { attempt ->
            require(ApkUpdater.allowedUrl(current)) { "下载地址不受信任" }
            val connection = URL(current).openConnection() as java.net.HttpURLConnection
            connection.instanceFollowRedirects = false
            connection.connectTimeout = 15_000
            connection.readTimeout = 30_000
            when (val status = connection.responseCode) {
                in 200..299 -> {
                    connection.inputStream.use { input -> java.io.FileOutputStream(dest).use { output ->
                        val buffer = ByteArray(16 * 1024)
                        var total = 0L
                        while (true) {
                            val count = input.read(buffer)
                            if (count < 0) break
                            total += count
                            require(total <= 150L * 1024 * 1024) { "APK 超过大小限制" }
                            output.write(buffer, 0, count)
                        }
                    } }
                    return@withContext
                }
                in 300..399 -> {
                    current = URL(URL(current), connection.getHeaderField("Location") ?: error("更新服务器没有提供跳转地址")).toString()
                    if (attempt == 5) error("APK 下载跳转次数过多")
                }
                else -> error("APK 下载失败：HTTP $status")
            }
        }
    }
}
