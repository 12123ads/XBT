package com.github.enderwolf006.xbt.update

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.io.IOException
import java.net.HttpURLConnection
import java.net.URL

/**
 * Fetches the release manifest. Only HTTPS is accepted, including redirects.
 * All transport failures collapse into [UpdateCheckOutcome.Unavailable] so a
 * startup check can stay silent (offline, 503, proxy errors...).
 */
internal object UpdateApi {
    private const val CONNECT_TIMEOUT_MS = 8_000
    private const val READ_TIMEOUT_MS = 12_000
    private const val MAX_BODY_BYTES = 256 * 1024

    suspend fun check(
        targetUrl: String,
        packageName: String,
        versionCode: Long,
        abi: String,
    ): UpdateCheckOutcome = withContext(Dispatchers.IO) {
        val endpoint = buildVersionEndpoint(targetUrl, packageName, versionCode, abi)
            ?: return@withContext UpdateCheckOutcome.Unavailable
        when (val response = get(endpoint)) {
            null -> UpdateCheckOutcome.Unavailable
            is HttpResponse.NoContent -> UpdateCheckOutcome.UpToDate
            is HttpResponse.Success -> parseVersionManifest(response.body, versionCode)
            HttpResponse.Other -> UpdateCheckOutcome.Unavailable
        }
    }

    private sealed interface HttpResponse {
        data object NoContent : HttpResponse
        data class Success(val body: String) : HttpResponse
        data object Other : HttpResponse
    }

    private fun get(endpoint: String): HttpResponse? {
        if (!isHttpsUrl(endpoint)) return null
        var connection: HttpURLConnection? = null
        return try {
            val http = URL(endpoint).openConnection() as? HttpURLConnection ?: return null
            connection = http
            http.connectTimeout = CONNECT_TIMEOUT_MS
            http.readTimeout = READ_TIMEOUT_MS
            http.instanceFollowRedirects = true
            http.setRequestProperty("Accept", "application/json")
            http.setRequestProperty("Accept-Encoding", "identity")
            http.connect()

            // A redirect to plain HTTP is not acceptable for the update channel.
            if (!http.url.protocol.equals("https", ignoreCase = true)) return null

            when (val code = http.responseCode) {
                HttpURLConnection.HTTP_NO_CONTENT -> HttpResponse.NoContent
                HttpURLConnection.HTTP_OK -> {
                    val body = http.inputStream.use { stream ->
                        stream.readLimitedBytes(MAX_BODY_BYTES)?.toString(Charsets.UTF_8)
                    } ?: return null
                    HttpResponse.Success(body)
                }
                else -> {
                    // Drain nothing; the error body is irrelevant. Just log-free skip.
                    HttpResponse.Other
                }
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
