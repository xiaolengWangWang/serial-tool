package com.xiaoleng.commbox.update

import android.content.Context
import android.content.Intent
import android.content.pm.PackageInfo
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import androidx.core.content.FileProvider
import java.io.File

class AndroidApkInstaller(private val context: Context) {
    fun install(file: File): Boolean {
        require(file.isFile && file.extension.equals("apk", ignoreCase = true)) { "APK 文件不存在" }
        val flags = if (Build.VERSION.SDK_INT >= 28) PackageManager.GET_SIGNING_CERTIFICATES else legacySignaturesFlag()
        val archive = context.packageManager.getPackageArchiveInfo(file.absolutePath, flags) ?: error("APK 文件无法解析")
        require(archive.packageName == context.packageName) { "APK 应用包名不匹配" }
        require(signatures(archive) == signatures(context.packageManager.getPackageInfo(context.packageName, flags))) { "APK 签名与当前应用不一致" }
        if (Build.VERSION.SDK_INT >= 26 && !context.packageManager.canRequestPackageInstalls()) {
            val permission = Intent(android.provider.Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, Uri.parse("package:${context.packageName}"))
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
            context.startActivity(permission)
            return false
        }
        val uri = FileProvider.getUriForFile(context, "${context.packageName}.fileprovider", file)
        val intent = Intent(Intent.ACTION_VIEW)
            .setDataAndType(uri, "application/vnd.android.package-archive")
            .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_ACTIVITY_NEW_TASK)
        context.startActivity(intent)
        return true
    }

    private fun signatures(info: PackageInfo): Set<String> {
        val bytes = if (Build.VERSION.SDK_INT >= 28) {
            info.signingInfo?.apkContentsSigners?.map { it.toByteArray() }.orEmpty()
        } else {
            legacySignatures(info)
        }
        require(bytes.isNotEmpty()) { "APK 缺少签名证书" }
        return bytes.map { it.joinToString("") { byte -> "%02x".format(byte) } }.toSet()
    }
}

@Suppress("DEPRECATION")
private fun legacySignaturesFlag() = PackageManager.GET_SIGNATURES

@Suppress("DEPRECATION")
private fun legacySignatures(info: PackageInfo) = info.signatures?.map { it.toByteArray() }.orEmpty()
