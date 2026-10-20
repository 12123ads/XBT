package com.github.enderwolf006.xbt.update

import android.app.Activity
import android.content.ActivityNotFoundException
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Build
import android.provider.Settings
import android.widget.Toast
import androidx.core.content.FileProvider
import java.io.File

internal object UpdateInstaller {
    private const val APK_MIME_TYPE = "application/vnd.android.package-archive"

    /** Android 8+ requires per-app user consent for installing unknown apps. */
    fun canInstall(context: Context): Boolean =
        Build.VERSION.SDK_INT < Build.VERSION_CODES.O ||
            context.packageManager.canRequestPackageInstalls()

    fun openUnknownSourcesSettings(activity: Activity): Boolean {
        val intent = Intent(
            Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES,
            Uri.parse("package:${activity.packageName}"),
        ).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        return try {
            activity.startActivity(intent)
            true
        } catch (_: ActivityNotFoundException) {
            Toast.makeText(
                activity,
                "无法打开安装权限设置，请在系统设置中允许「${activity.applicationInfo.loadLabel(activity.packageManager)}」安装应用",
                Toast.LENGTH_LONG,
            ).show()
            false
        }
    }

    fun install(context: Context, apk: File) {
        val uri = FileProvider.getUriForFile(
            context,
            "${context.packageName}.fileprovider",
            apk,
        )
        val intent = Intent(Intent.ACTION_VIEW).apply {
            setDataAndType(uri, APK_MIME_TYPE)
            addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_ACTIVITY_NEW_TASK)
        }
        context.startActivity(intent)
    }
}
