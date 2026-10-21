package com.github.enderwolf006.xbt.update

import android.content.Context
import android.content.pm.PackageInfo
import android.content.pm.PackageManager
import android.os.Build
import android.webkit.JavascriptInterface
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.compose.runtime.State
import androidx.compose.runtime.mutableStateOf
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONObject
import java.io.File

/**
 * Checks, downloads, verifies and installs APK updates.
 *
 * All mutating entry points are expected on the main thread (UI, JS bridge or
 * lifecycle callbacks); blocking work is delegated to [UpdateApi] and
 * [ApkDownloader], which use Dispatchers.IO internally.
 */
internal class UpdateController(
    private val activity: ComponentActivity,
    private val scope: CoroutineScope,
) {
    private val preferences = UpdatePreferences(activity.applicationContext)
    private val mutableState = mutableStateOf<UpdateUiState>(UpdateUiState.Idle)
    val state: State<UpdateUiState> get() = mutableState

    private var checkJob: Job? = null
    private var downloadJob: Job? = null
    private var pendingApk: File? = null

    fun checkOnStartup() = check(manual = false)

    fun checkManually() = check(manual = true)

    private fun check(manual: Boolean) {
        val current = mutableState.value
        if (current is UpdateUiState.Available ||
            current is UpdateUiState.Downloading ||
            current is UpdateUiState.Verifying ||
            current is UpdateUiState.NeedsInstallPermission
        ) {
            if (manual) toast("正在处理更新，请稍候")
            return
        }
        if (checkJob?.isActive == true) return
        if (current is UpdateUiState.Failed) {
            mutableState.value = UpdateUiState.Idle
        }

        checkJob = scope.launch {
            val packageInfo = installedPackageInfo(activity) ?: return@launch
            val versionCode = packageVersionCode(packageInfo)
            val outcome = try {
                UpdateApi.check(
                    versionCode = versionCode,
                    versionName = packageVersionName(packageInfo),
                )
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (_: Exception) {
                UpdateCheckOutcome.Unavailable
            }

            when (outcome) {
                UpdateCheckOutcome.UpToDate -> if (manual) toast("当前已是最新版本")
                UpdateCheckOutcome.Unavailable -> if (manual) toast("检查更新失败，请稍后重试")
                is UpdateCheckOutcome.Rejected -> {
                    mutableState.value = UpdateUiState.Failed(
                        release = null,
                        mandatory = false,
                        message = outcome.message,
                    )
                }
                is UpdateCheckOutcome.Available -> {
                    if (!outcome.mandatory && !manual &&
                        !preferences.shouldPrompt(outcome.release.versionCode)
                    ) {
                        return@launch
                    }
                    preferences.recordPrompt(outcome.release.versionCode)
                    mutableState.value = UpdateUiState.Available(outcome.release, outcome.mandatory)
                }
            }
        }
    }

    fun startDownload() {
        val available = mutableState.value as? UpdateUiState.Available ?: return
        if (downloadJob?.isActive == true) return
        val release = available.release
        val mandatory = available.mandatory
        mutableState.value = UpdateUiState.Downloading(release, mandatory, 0L, release.sizeBytes)

        downloadJob = scope.launch {
            try {
                val apk = ApkDownloader.download(activity.applicationContext, release) { bytes, total ->
                    activity.runOnUiThread {
                        val shown = mutableState.value as? UpdateUiState.Downloading ?: return@runOnUiThread
                        if (shown.release.versionCode == release.versionCode) {
                            mutableState.value = shown.copy(bytes = bytes, total = total)
                        }
                    }
                }
                mutableState.value = UpdateUiState.Verifying(release, mandatory)

                val error = try {
                    withContext(Dispatchers.IO) {
                        ApkVerifier.verify(activity.applicationContext, apk, release)
                    }
                } catch (cancelled: CancellationException) {
                    throw cancelled
                } catch (_: Exception) {
                    "安装包校验失败（无法校验安装包）"
                }
                if (error != null) {
                    apk.delete()
                    mutableState.value = UpdateUiState.Failed(release, mandatory, error)
                    return@launch
                }

                pendingApk = apk
                if (UpdateInstaller.canInstall(activity)) {
                    launchInstaller(release, mandatory)
                } else {
                    mutableState.value = UpdateUiState.NeedsInstallPermission(release, mandatory)
                    UpdateInstaller.openUnknownSourcesSettings(activity)
                }
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (expected: UpdateException) {
                mutableState.value = UpdateUiState.Failed(release, mandatory, expected.message ?: "更新失败")
            } catch (error: Exception) {
                mutableState.value = UpdateUiState.Failed(
                    release,
                    mandatory,
                    "更新失败：${error.message ?: "未知错误"}",
                )
            }
        }
    }

    fun cancelDownload() {
        val downloading = mutableState.value as? UpdateUiState.Downloading ?: return
        downloadJob?.cancel()
        downloadJob = null
        mutableState.value = UpdateUiState.Available(downloading.release, downloading.mandatory)
    }

    fun dismiss() {
        when (val current = mutableState.value) {
            is UpdateUiState.Available -> if (!current.mandatory) mutableState.value = UpdateUiState.Idle
            is UpdateUiState.Failed -> if (!current.mandatory) mutableState.value = UpdateUiState.Idle
            is UpdateUiState.NeedsInstallPermission -> if (!current.mandatory) {
                pendingApk?.delete()
                pendingApk = null
                mutableState.value = UpdateUiState.Idle
            }
            else -> Unit
        }
    }

    fun retry() {
        val failed = mutableState.value as? UpdateUiState.Failed ?: return
        val release = failed.release ?: run {
            mutableState.value = UpdateUiState.Idle
            return
        }
        mutableState.value = UpdateUiState.Available(release, failed.mandatory)
        startDownload()
    }

    fun retryInstall() {
        val permission = mutableState.value as? UpdateUiState.NeedsInstallPermission ?: return
        if (UpdateInstaller.canInstall(activity)) {
            launchInstaller(permission.release, permission.mandatory)
        } else {
            UpdateInstaller.openUnknownSourcesSettings(activity)
        }
    }

    fun openInstallSettings() {
        UpdateInstaller.openUnknownSourcesSettings(activity)
    }

    /** Called from Activity.onResume: continue as soon as the user granted install permission. */
    fun onResume() {
        val permission = mutableState.value as? UpdateUiState.NeedsInstallPermission ?: return
        if (UpdateInstaller.canInstall(activity)) {
            launchInstaller(permission.release, permission.mandatory)
        }
    }

    private fun launchInstaller(release: AppRelease, mandatory: Boolean) {
        val apk = pendingApk ?: return
        try {
            UpdateInstaller.install(activity, apk)
        } catch (error: Exception) {
            mutableState.value = UpdateUiState.Failed(
                release,
                mandatory,
                "无法启动安装程序：${error.message ?: "未知错误"}",
            )
            return
        }
        pendingApk = null
        mutableState.value = UpdateUiState.Idle
    }

    /** Bridge exposed to the WebView as `XBTUpdateBridge`. */
    fun javascriptBridge(): Any = object {
        @JavascriptInterface
        fun isReady(): Boolean = true

        @JavascriptInterface
        fun getVersion(): String {
            val info = installedPackageInfo(activity)
            return JSONObject()
                .put("versionName", info?.versionName ?: "")
                .put("versionCode", info?.let { packageVersionCode(it) } ?: 0L)
                .toString()
        }

        @JavascriptInterface
        fun checkForUpdate() {
            activity.runOnUiThread { checkManually() }
        }
    }

    private fun toast(message: String) {
        Toast.makeText(activity, message, Toast.LENGTH_SHORT).show()
    }
}

private fun installedPackageInfo(context: Context): PackageInfo? = try {
    context.packageManager.getPackageInfo(context.packageName, 0)
} catch (_: PackageManager.NameNotFoundException) {
    null
}

private fun packageVersionCode(info: PackageInfo): Long =
    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) {
        info.longVersionCode
    } else {
        @Suppress("DEPRECATION")
        info.versionCode.toLong()
    }

private fun packageVersionName(info: PackageInfo): String = info.versionName.orEmpty()
