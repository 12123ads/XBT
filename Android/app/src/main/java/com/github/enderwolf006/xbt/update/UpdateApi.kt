package com.github.enderwolf006.xbt.update

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.io.IOException
import java.net.HttpURLConnection
import java.net.URL

/**
 * Resolves the latest GitHub Release and its manifest asset. Only HTTPS is
 * accepted, including redirects. All transport failures collapse into
 * [UpdateCheckOutcome.Unavailable] so a startup check can stay silent
 * (offline, rate limit, GitHub outage...).
 */
internal object UpdateApi {
    private const val CONNECT_TIMEOUT_MS = 8_000
    private const val READ_TIMEOUT_MS = 12_000
    private const val MAX_BODY_BYTES = 256 * 1024

    suspend fun check(
        versionCode: Long,
        versionName: String,
    ): UpdateCheckOutcome = withContext(Dispatchers.IO) {
        val userAgent = "xbt-android/${versionName.trim().ifBlank { "unknown" }}"

        val releaseBody = get(GITHUB_RELEASE_API, userAgent) ?: return@withContext UpdateCheckOutcome.Unavailable
        val assets = parseReleaseAssets(releaseBody) ?: return@withContext UpdateCheckOutcome.Unavailable
        val apkDownloadUrl = assets[APK_ASSET_NAME] ?: return@withContext UpdateCheckOutcome.Unavailable
        val versionDownloadUrl = assets[VERSION_ASSET_NAME] ?: return@withContext UpdateCheckOutcome.Unavailable
        if (!isHttpsUrl(versionDownloadUrl)) return@withContext UpdateCheckOutcome.Unavailable

        val versionBody = get(GITHUB_DOWNLOAD_ACCELERATOR + versionDownloadUrl, userAgent)
            ?: return@withContext UpdateCheckOutcome.Unavailable
        parseVersionManifest(versionBody, versionCode, apkDownloadUrl)
    }

    /** Returns the 200 body, or null for any non-200/failed/unsafe request. */
    private fun get(endpoint: String, userAgent: String): String? {
        if (!isHttpsUrl(endpoint)) return null
        var connection: HttpURLConnection? = null
        return try {
            val http = URL(endpoint).openConnection() as? HttpURLConnection ?: return null
            connection = http
            http.connectTimeout = CONNECT_TIMEOUT_MS
            http.readTimeout = READ_TIMEOUT_MS
            http.instanceFollowRedirects = true
            http.setRequestProperty("Accept", "application/vnd.github+json")
            http.setRequestProperty("X-GitHub-Api-Version", "2022-11-28")
            http.setRequestProperty("User-Agent", userAgent)
            http.setRequestProperty("Accept-Encoding", "identity")
            http.connect()

            // A redirect to plain HTTP is not acceptable for the update channel.
            if (!http.url.protocol.equals("https", ignoreCase = true)) return null

            if (http.responseCode != HttpURLConnection.HTTP_OK) {
                return null
            }
            http.inputStream.use { stream ->
                stream.readLimitedBytes(MAX_BODY_BYTES)?.toString(Charsets.UTF_8)
            }
        } catch (_: IOException) {
            null
        } catch (_: IllegalArgumentException) {
            null
        } finally {
            connection?.disconnect()
        }
    }
}

private fun java.io.InputStream.readLimitedBytes(limit: Int): ByteArray? {
    val output = java.io.ByteArrayOutputStream()
    val buffer = ByteArray(8 * 1024)
    while (true) {
        val read = read(buffer)
        if (read < 0) break
        if (output.size() + read > limit) return null
        output.write(buffer, 0, read)
    }
    return output.toByteArray()
}
