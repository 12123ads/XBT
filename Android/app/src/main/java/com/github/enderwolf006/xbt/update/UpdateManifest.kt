package com.github.enderwolf006.xbt.update

import org.json.JSONException
import org.json.JSONObject
import java.net.URI

/** Release metadata resolved from the GitHub Release and its version.json asset. */
internal data class AppRelease(
    val versionCode: Long,
    val versionName: String,
    val apkUrl: String,
    val sha256: String,
    val sizeBytes: Long,
    val changelog: String,
)

internal sealed interface UpdateCheckOutcome {
    /** GitHub's latest release is not newer than the installed one. */
    data object UpToDate : UpdateCheckOutcome

    /** Network failure, missing release assets, malformed payload... Silent on startup checks. */
    data object Unavailable : UpdateCheckOutcome

    data class Available(val release: AppRelease, val mandatory: Boolean) : UpdateCheckOutcome

    /** The release offered an update we refuse to install (e.g. a non-HTTPS APK URL). */
    data class Rejected(val message: String) : UpdateCheckOutcome
}

/** Latest non-draft, non-prerelease release of the public repository. */
internal const val GITHUB_RELEASE_API = "https://api.github.com/repos/12123ads/XBT/releases/latest"

/** Download accelerator for asset downloads (the API itself is queried directly). */
internal const val GITHUB_DOWNLOAD_ACCELERATOR = "https://ghfast.top/"

/** Fixed asset names published by the Android CI workflow. */
internal const val VERSION_ASSET_NAME = "version.json"
internal const val APK_ASSET_NAME = "xbt-latest.apk"

private val SHA256_PATTERN = Regex("[0-9a-f]{64}")

internal fun isHttpsUrl(value: String): Boolean {
    val uri = runCatching { URI(value) }.getOrNull() ?: return false
    return uri.scheme?.equals("https", ignoreCase = true) == true && !uri.host.isNullOrBlank()
}

/**
 * Extracts `name -> browser_download_url` from a GitHub release payload.
 *
 * Returns null when the payload has no usable asset array. Duplicate names keep
 * the first occurrence; GitHub cannot publish two assets with the same name.
 */
internal fun parseReleaseAssets(body: String): Map<String, String>? {
    return try {
        val assets = JSONObject(body).optJSONArray("assets") ?: return null
        val urls = LinkedHashMap<String, String>()
        for (index in 0 until assets.length()) {
            val asset = assets.optJSONObject(index) ?: continue
            val name = asset.optString("name").trim()
            val url = asset.optString("browser_download_url").trim()
            if (name.isNotEmpty() && url.isNotEmpty() && name !in urls) {
                urls[name] = url
            }
        }
        urls
    } catch (_: JSONException) {
        null
    }
}

/**
 * Combines a GitHub release payload with the body of its `version.json` asset.
 *
 * GitHub Releases have no `versionCode`, so the manifest asset is the source of
 * truth for numeric version, checksum and size. Missing assets mean the release
 * is incomplete (CI failure) and therefore unavailable rather than rejected.
 */
internal fun parseGitHubRelease(
    releaseBody: String,
    versionBody: String,
    installedVersionCode: Long,
): UpdateCheckOutcome {
    val assets = parseReleaseAssets(releaseBody) ?: return UpdateCheckOutcome.Unavailable
    val versionDownloadUrl = assets[VERSION_ASSET_NAME] ?: return UpdateCheckOutcome.Unavailable
    val apkDownloadUrl = assets[APK_ASSET_NAME] ?: return UpdateCheckOutcome.Unavailable
    if (!isHttpsUrl(versionDownloadUrl)) return UpdateCheckOutcome.Unavailable
    return parseVersionManifest(versionBody, installedVersionCode, apkDownloadUrl)
}

/**
 * Parses the body of the `version.json` release asset.
 *
 * Unknown fields are ignored; any payload that cannot be trusted (bad hash,
 * non-HTTPS APK URL) is rejected instead of silently downgraded.
 */
internal fun parseVersionManifest(
    body: String,
    installedVersionCode: Long,
    apkDownloadUrl: String,
): UpdateCheckOutcome {
    return try {
        val root = JSONObject(body)
        val versionCode = root.optLong("versionCode", 0L)
        if (versionCode <= 0L) return UpdateCheckOutcome.Unavailable
        if (versionCode <= installedVersionCode) return UpdateCheckOutcome.UpToDate

        if (!isHttpsUrl(apkDownloadUrl)) {
            return UpdateCheckOutcome.Rejected("更新包地址不是 HTTPS，已拒绝本次更新")
        }
        val apkUrl = GITHUB_DOWNLOAD_ACCELERATOR + apkDownloadUrl
        if (!isHttpsUrl(apkUrl)) {
            return UpdateCheckOutcome.Rejected("更新包地址不是 HTTPS，已拒绝本次更新")
        }
        val sha256 = root.optString("sha256").trim().lowercase()
        if (!SHA256_PATTERN.matches(sha256)) {
            return UpdateCheckOutcome.Rejected("更新包校验信息无效，已拒绝本次更新")
        }
        val sizeBytes = root.optLong("sizeBytes", 0L)
        if (sizeBytes < 0L) {
            return UpdateCheckOutcome.Rejected("更新包大小信息无效，已拒绝本次更新")
        }
        val versionName = root.optString("versionName").trim().ifBlank { "新版本" }
        val changelog = root.optString("changelog").trim().ifBlank { "包含若干改进与问题修复。" }
        val mandatory = installedVersionCode < root.optLong("minSupportedVersionCode", 0L)

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
