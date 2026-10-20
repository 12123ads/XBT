package com.github.enderwolf006.xbt.update

import org.json.JSONException
import org.json.JSONObject
import java.net.URI

/** Release metadata served by GET /api/app/version. */
internal data class AppRelease(
    val versionCode: Long,
    val versionName: String,
    val apkUrl: String,
    val sha256: String,
    val sizeBytes: Long,
    val changelog: String,
)

internal sealed interface UpdateCheckOutcome {
    /** Server returned 204, or its latest release is not newer than the installed one. */
    data object UpToDate : UpdateCheckOutcome

    /** Network failure, 503, malformed payload... Silent on startup checks. */
    data object Unavailable : UpdateCheckOutcome

    data class Available(val release: AppRelease, val mandatory: Boolean) : UpdateCheckOutcome

    /** The server offered an update we refuse to install (e.g. a non-HTTPS APK URL). */
    data class Rejected(val message: String) : UpdateCheckOutcome
}

private val SHA256_PATTERN = Regex("[0-9a-f]{64}")

internal fun isHttpsUrl(value: String): Boolean {
    val uri = runCatching { URI(value) }.getOrNull() ?: return false
    return uri.scheme?.equals("https", ignoreCase = true) == true && !uri.host.isNullOrBlank()
}

/**
 * Builds the public version endpoint from the shipped target URL.
 *
 * Returns null when the base URL is not HTTPS: the update channel never falls
 * back to cleartext, even though the WebView itself allows mixed content.
 */
internal fun buildVersionEndpoint(
    targetUrl: String,
    packageName: String,
    versionCode: Long,
    abi: String,
): String? {
    val base = runCatching { URI(targetUrl) }.getOrNull() ?: return null
    if (base.scheme?.equals("https", ignoreCase = true) != true) return null
    if (base.userInfo != null) return null
    val authority = base.rawAuthority?.takeIf { it.isNotBlank() } ?: return null
    val query = "package=$packageName&versionCode=$versionCode&abi=$abi"
    return runCatching { URI("https", authority, "/api/app/version", query, null).toString() }.getOrNull()
}

/**
 * Parses the raw 200 body of GET /api/app/version.
 *
 * Unknown fields are ignored; any payload that cannot be trusted (bad hash,
 * non-HTTPS APK URL) is rejected instead of silently downgraded.
 */
internal fun parseVersionManifest(body: String, installedVersionCode: Long): UpdateCheckOutcome {
    return try {
        val root = JSONObject(body)
        val latest = root.optJSONObject("latest") ?: return UpdateCheckOutcome.Unavailable
        val versionCode = latest.optLong("versionCode", 0L)
        if (versionCode <= 0L) return UpdateCheckOutcome.Unavailable
        if (versionCode <= installedVersionCode) return UpdateCheckOutcome.UpToDate

        val versionName = latest.optString("versionName").trim().ifBlank { "新版本" }
        val apkUrl = latest.optString("apkUrl").trim()
        if (!isHttpsUrl(apkUrl)) {
            return UpdateCheckOutcome.Rejected("更新包地址不是 HTTPS，已拒绝本次更新")
        }
        val sha256 = latest.optString("sha256").trim().lowercase()
        if (!SHA256_PATTERN.matches(sha256)) {
            return UpdateCheckOutcome.Rejected("更新包校验信息无效，已拒绝本次更新")
        }
        val sizeBytes = latest.optLong("sizeBytes", 0L)
        if (sizeBytes < 0L) {
            return UpdateCheckOutcome.Rejected("更新包大小信息无效，已拒绝本次更新")
        }
        val changelog = latest.optString("changelog").trim().ifBlank { "包含若干改进与问题修复。" }
        val mandatory = root.optBoolean("mandatory", false) ||
            installedVersionCode < root.optLong("minSupportedVersionCode", 0L)

        UpdateCheckOutcome.Available(
            release = AppRelease(
                versionCode = versionCode,
                versionName = versionName,
                apkUrl = apkUrl,
                sha256 = sha256,
                sizeBytes = sizeBytes,
                changelog = changelog,
            ),
            mandatory = mandatory,
        )
    } catch (_: JSONException) {
        UpdateCheckOutcome.Unavailable
    }
}
