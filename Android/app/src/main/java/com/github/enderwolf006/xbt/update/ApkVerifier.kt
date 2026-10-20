package com.github.enderwolf006.xbt.update

import android.content.Context
import android.content.pm.PackageInfo
import android.content.pm.PackageManager
import android.os.Build
import java.io.File

/**
 * Verifies a downloaded APK before handing it to the system installer:
 * SHA-256, package name, version code and the signing certificate set must all
 * match what this install trusts. Returns null when the APK is installable.
 */
internal object ApkVerifier {
    private const val SHA_MISMATCH = "安装包校验失败（SHA-256 不一致）"
    private const val UNREADABLE = "安装包校验失败（无法读取安装包）"
    private const val PACKAGE_MISMATCH = "安装包校验失败（包名不一致）"
    private const val VERSION_MISMATCH = "安装包校验失败（版本号不一致）"
    private const val SIGNATURE_MISMATCH = "安装包校验失败（签名不一致）"

    fun verify(context: Context, apk: File, expected: AppRelease): String? {
        if (!apk.isFile) return UNREADABLE
        val digest = sha256Hex(apk) ?: return UNREADABLE
        if (!digest.equals(expected.sha256, ignoreCase = true)) return SHA_MISMATCH

        val packageManager = context.packageManager
        val archive = packageManager.getPackageArchiveInfo(apk.absolutePath, signFlags())
            ?: return UNREADABLE
        if (archive.packageName != context.packageName) return PACKAGE_MISMATCH
        if (archiveVersionCode(archive) != expected.versionCode) return VERSION_MISMATCH

        val installed = try {
            packageManager.getPackageInfo(context.packageName, signFlags())
        } catch (_: PackageManager.NameNotFoundException) {
            return UNREADABLE
        }
        val installedCertificates = certificateDigests(installed)
        val archiveCertificates = certificateDigests(archive)
        if (installedCertificates.isEmpty() || archiveCertificates.isEmpty()) return UNREADABLE
        if (installedCertificates != archiveCertificates) return SIGNATURE_MISMATCH
        return null
    }

    private fun signFlags(): Int =
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) {
            PackageManager.GET_SIGNING_CERTIFICATES
        } else {
            @Suppress("DEPRECATION")
            PackageManager.GET_SIGNATURES
        }

    private fun archiveVersionCode(info: PackageInfo): Long =
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) {
            info.longVersionCode
        } else {
            @Suppress("DEPRECATION")
            info.versionCode.toLong()
        }

    private fun certificateDigests(info: PackageInfo): Set<String> {
        val signatures = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) {
            info.signingInfo?.apkContentsSigners?.toList().orEmpty()
        } else {
            @Suppress("DEPRECATION")
            info.signatures?.toList().orEmpty()
        }
        return signatures.mapNotNull { signature ->
            runCatching { sha256OfBytes(signature.toByteArray()) }.getOrNull()
        }.toSet()
    }

    private fun sha256OfBytes(bytes: ByteArray): String? = try {
        java.security.MessageDigest.getInstance("SHA-256").digest(bytes).toHexString()
    } catch (_: Exception) {
        null
    }
}
