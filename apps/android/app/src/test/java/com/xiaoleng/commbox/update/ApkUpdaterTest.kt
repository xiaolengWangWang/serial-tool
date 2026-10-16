package com.xiaoleng.commbox.update

import java.io.File
import java.security.MessageDigest
import kotlin.io.path.createTempDirectory
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertThrows
import org.junit.Test

class ApkUpdaterTest {
    @Test fun selectsNewestAndroidReleaseAndIgnoresCurrentVersion() = runBlocking {
        val payload = release("v0.10.0", APK, "https://gitee.com/test/apk")
        val updater = ApkUpdater(createTempDirectory().toFile(), FakeHttp(mapOf(ApkUpdater.GITEE_RELEASES to payload, ApkUpdater.GITHUB_RELEASES to payload)))
        assertNotNull(updater.check("0.9.9"))
        assertNull(updater.check("0.10.0"))
    }

    @Test fun prereleaseIsOlderThanMatchingStableVersion() {
        assertEquals(-1, ApkUpdater.compareVersions("1.2.0-rc1", "1.2.0"))
        assertEquals(1, ApkUpdater.compareVersions("1.10.0", "1.9.0"))
    }

    @Test fun absentChecksumIsRejectedBeforeDownload() = runBlocking {
        val http = FakeHttp(emptyMap())
        val updater = ApkUpdater(createTempDirectory().toFile(), http)
        assertThrows(IllegalArgumentException::class.java) {
            runBlocking { updater.downloadVerified(UpdateInfo("1.0.0", APK, "https://gitee.com/apk", "")) }
        }
        assertEquals(0, http.downloadCount)
    }

    @Test fun checksumMismatchDeletesFile() = runBlocking {
        val dir = createTempDirectory().toFile()
        val updater = ApkUpdater(dir, FakeHttp(emptyMap(), APK_BYTES))
        val wrong = "0".repeat(64)
        assertThrows(IllegalArgumentException::class.java) {
            runBlocking { updater.downloadVerified(UpdateInfo("1.0.0", APK, "https://gitee.com/apk", wrong)) }
        }
        assertFalse(File(dir, APK).exists())
    }

    @Test fun rejectsUntrustedHostAndInterruptedDownload() = runBlocking {
        val dir = createTempDirectory().toFile()
        val http = FakeHttp(emptyMap(), APK_BYTES, failAfterWrite = true)
        val updater = ApkUpdater(dir, http)
        val hash = sha256(APK_BYTES)
        assertThrows(IllegalArgumentException::class.java) {
            runBlocking { updater.downloadVerified(UpdateInfo("1.0.0", APK, "https://evil.example/apk", hash)) }
        }
        assertThrows(IllegalStateException::class.java) {
            runBlocking { updater.downloadVerified(UpdateInfo("1.0.0", APK, "https://gitee.com/apk", hash)) }
        }
        assertFalse(File(dir, APK).exists())
    }

    private class FakeHttp(private val responses: Map<String, String>, private val bytes: ByteArray = APK_BYTES, private val failAfterWrite: Boolean = false) : UpdateHttp {
        var downloadCount = 0
        override suspend fun getJson(url: String) = responses[url] ?: error("offline")
        override suspend fun download(url: String, dest: File) {
            downloadCount++
            dest.writeBytes(bytes)
            if (failAfterWrite) error("interrupted")
        }
    }

    companion object {
        private const val APK = "CommBox-1.0.0-Android.apk"
        private val APK_BYTES = byteArrayOf(0x50, 0x4b, 0x03, 0x04)
        private fun sha256(bytes: ByteArray) = MessageDigest.getInstance("SHA-256").digest(bytes).joinToString("") { "%02x".format(it) }
        private fun release(tag: String, name: String, url: String): String {
            val hash = sha256(APK_BYTES)
            return """{"tag_name":"$tag","body":"$hash  $name","assets":[{"name":"$name","browser_download_url":"$url"}]}"""
        }
    }
}
