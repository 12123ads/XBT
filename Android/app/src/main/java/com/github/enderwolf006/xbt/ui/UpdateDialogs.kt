package com.github.enderwolf006.xbt.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.DialogProperties
import com.github.enderwolf006.xbt.update.UpdateUiState
import java.util.Locale

/** Renders every dialog of the in-app update flow; idle renders nothing. */
@Composable
internal fun UpdateDialogs(
    state: UpdateUiState,
    onDownload: () -> Unit,
    onCancelDownload: () -> Unit,
    onDismiss: () -> Unit,
    onRetry: () -> Unit,
    onOpenSettings: () -> Unit,
    onRetryInstall: () -> Unit,
) {
    when (state) {
        UpdateUiState.Idle -> Unit
        is UpdateUiState.Available -> AvailableDialog(state, onDownload, onDismiss)
        is UpdateUiState.Downloading -> DownloadingDialog(state, onCancelDownload)
        is UpdateUiState.Verifying -> VerifyingDialog(state.release.versionName)
        is UpdateUiState.NeedsInstallPermission -> InstallPermissionDialog(
            mandatory = state.mandatory,
            onDismiss = onDismiss,
            onOpenSettings = onOpenSettings,
            onRetryInstall = onRetryInstall,
        )
        is UpdateUiState.Failed -> FailedDialog(state, onDismiss, onRetry)
    }
}

@Composable
private fun AvailableDialog(
    state: UpdateUiState.Available,
    onDownload: () -> Unit,
    onDismiss: () -> Unit,
) {
    AlertDialog(
        onDismissRequest = { if (!state.mandatory) onDismiss() },
        properties = DialogProperties(
            dismissOnBackPress = !state.mandatory,
            dismissOnClickOutside = !state.mandatory,
        ),
        title = { Text("发现新版本 ${state.release.versionName}") },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text(state.release.changelog)
                if (state.release.sizeBytes > 0L) {
                    Text(
                        "安装包大小：${formatBytes(state.release.sizeBytes)}",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                if (state.mandatory) {
                    Text(
                        "此版本为强制更新，完成后才能继续使用。",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.error,
                        fontWeight = FontWeight.Bold,
                    )
                }
            }
        },
        confirmButton = {
            Button(onClick = onDownload) { Text("立即更新") }
        },
        dismissButton = if (state.mandatory) {
            null
        } else {
            { TextButton(onClick = onDismiss) { Text("稍后") } }
        },
    )
}

@Composable
private fun DownloadingDialog(
    state: UpdateUiState.Downloading,
    onCancelDownload: () -> Unit,
) {
    AlertDialog(
        onDismissRequest = onCancelDownload,
        properties = DialogProperties(
            dismissOnBackPress = true,
            dismissOnClickOutside = false,
        ),
        title = { Text("正在下载 ${state.release.versionName}") },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                if (state.total > 0L) {
                    LinearProgressIndicator(
                        progress = { (state.bytes.toFloat() / state.total.toFloat()).coerceIn(0f, 1f) },
                        modifier = Modifier.fillMaxWidth(),
                    )
                } else {
                    LinearProgressIndicator(modifier = Modifier.fillMaxWidth())
                }
                Text(
                    if (state.total > 0L) {
                        "${formatBytes(state.bytes)} / ${formatBytes(state.total)}"
                    } else {
                        "已下载 ${formatBytes(state.bytes)}"
                    },
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        },
        confirmButton = {
            TextButton(onClick = onCancelDownload) { Text("取消下载") }
        },
    )
}

@Composable
private fun VerifyingDialog(versionName: String) {
    AlertDialog(
        onDismissRequest = {},
        properties = DialogProperties(dismissOnBackPress = false, dismissOnClickOutside = false),
        title = { Text("正在校验 ${versionName}") },
        text = {
            Row(verticalAlignment = Alignment.CenterVertically) {
                CircularProgressIndicator(modifier = Modifier.size(20.dp), strokeWidth = 2.dp)
                Spacer(modifier = Modifier.width(12.dp))
                Text("正在校验安装包，请稍候…")
            }
        },
        confirmButton = {},
    )
}

@Composable
private fun InstallPermissionDialog(
    mandatory: Boolean,
    onDismiss: () -> Unit,
    onOpenSettings: () -> Unit,
    onRetryInstall: () -> Unit,
) {
    AlertDialog(
        onDismissRequest = { if (!mandatory) onDismiss() },
        properties = DialogProperties(
            dismissOnBackPress = !mandatory,
            dismissOnClickOutside = !mandatory,
        ),
        title = { Text("需要安装权限") },
        text = {
            Text(
                "安装包已下载并校验通过。请在系统设置中允许本应用「安装未知应用」。" +
                    "部分系统还会再次弹窗确认，请选择允许；返回本应用后会自动继续安装。",
            )
        },
        confirmButton = {
            Button(onClick = onOpenSettings) { Text("打开设置") }
        },
        dismissButton = {
            TextButton(onClick = onRetryInstall) { Text("已允许，继续") }
        },
    )
}

@Composable
private fun FailedDialog(
    state: UpdateUiState.Failed,
    onDismiss: () -> Unit,
    onRetry: () -> Unit,
) {
    AlertDialog(
        onDismissRequest = { if (!state.mandatory) onDismiss() },
        properties = DialogProperties(
            dismissOnBackPress = !state.mandatory,
            dismissOnClickOutside = !state.mandatory,
        ),
        title = { Text("更新失败") },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text(state.message)
                state.release?.let {
                    Text(
                        "版本 ${it.versionName}（${it.versionCode}）",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
        },
        confirmButton = {
            if (state.release != null) {
                Button(onClick = onRetry) { Text("重试") }
            } else {
                Button(onClick = onDismiss) { Text("知道了") }
            }
        },
        dismissButton = if (!state.mandatory && state.release != null) {
            { TextButton(onClick = onDismiss) { Text("关闭") } }
        } else {
            null
        },
    )
}

private fun formatBytes(bytes: Long): String {
    if (bytes <= 0L) return "0 B"
    val units = arrayOf("B", "KB", "MB", "GB")
    var value = bytes.toDouble()
    var unit = 0
    while (value >= 1024 && unit < units.lastIndex) {
        value /= 1024
        unit++
    }
    return if (unit == 0) "${bytes} B" else String.format(Locale.US, "%.1f %s", value, units[unit])
}
