package com.github.enderwolf006.xbt.update

import android.content.Context
import android.os.SystemClock
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.withContext
import java.io.File
import java.io.FileOutputStream
import java.io.IOException
import java.net.URL
import java.security.MessageDigest
import javax.net.ssl.HttpsURLConnection

/**
 * Downloads an update APK into cacheDir/updates and verifies its SHA-256 while
 * streaming. Partial files are always removed on cancel or failure.
 */
internal object ApkDownloader {
    private const val MAX_APK_BYTES = 256L * 1024L * 1024L
    private const val CONNECT_TIMEOUT_MS = 15_000
    private const val READ_TIMEOUT_MS = 30_000
    private const val BUFFER_SIZE = 64 * 1024
    private const val PROGRESS_INTERVAL_MS = 120L

    fun updatesDir(context: Context): File = File(context.cacheDir, "updates")

    suspend fun download(
        context: Context,
        release: AppRelease,
        onProgress: (bytes: Long, total: Long) -> Unit,
    ): File = withContext(Dispatchers.IO) {
        if (!isHttpsUrl(release.apkUrl)) {
            throw UpdateException("更新包地址不是 HTTPS，已拒绝下载")
        }

        val dir = updatesDir(context)
        if (!dir.isDirectory && !dir.mkdirs()) {
            throw UpdateException("无法创建更新缓存目录")
        }
        // Remove leftovers from earlier attempts so a cancelled download can
        // never be mistaken for a complete one.
        dir.listFiles()?.forEach { file ->
            if (file.isFile && (file.name.endsWith(".apk") || file.name.endsWith(".part"))) {
                file.delete()
            }
        }

        val target = File(dir, "xbt-${release.versionCode}.apk")
        val part = File(dir, "xbt-${release.versionCode}.apk.part")
        var connection: HttpsURLConnection? = null
        var cancellationHandle: kotlinx.coroutines.DisposableHandle? = null

        try {
            val http = (URL(release.apkUrl).openConnection() as? HttpsURLConnection)
                ?: throw UpdateException("更新包地址无效")
            connection = http
            http.connectTimeout = CONNECT_TIMEOUT_MS
            http.readTimeout = READ_TIMEOUT_MS
            http.instanceFollowRedirects = true
            http.setRequestProperty("Accept", "application/vnd.android.package-archive")
            http.setRequestProperty("Accept-Encoding", "identity")
            cancellationHandle = currentCoroutineContext()[Job]?.invokeOnCompletion { cause ->
                if (cause is CancellationException) runCatching { http.disconnect() }
            }
            http.connect()

            if (!http.url.protocol.equals("https", ignoreCase = true)) {
                throw UpdateException("更新包地址不是 HTTPS，已拒绝下载")
            }
            val status = http.responseCode
            if (status !in 200..299) {
                throw UpdateException("下载失败（HTTP $status）")
            }
            val total = http.contentLengthLong
            if (total > MAX_APK_BYTES) {
                throw UpdateException("安装包超过大小上限，已拒绝下载")
            }
            if (release.sizeBytes > 0L && total > 0L && total != release.sizeBytes) {
                throw UpdateException("安装包大小与服务器记录不一致")
            }

            val digest = MessageDigest.getInstance("SHA-256")
            var written = 0L
            var lastReport = 0L
            FileOutputStream(part).use { output ->
                http.inputStream.use { input ->
                    val buffer = ByteArray(BUFFER_SIZE)
                    while (true) {
                        currentCoroutineContext().ensureActive()
                        val read = input.read(buffer)
                        if (read < 0) break
                        output.write(buffer, 0, read)
                        digest.update(buffer, 0, read)
                        written += read
                        if (written > MAX_APK_BYTES) {
                            throw UpdateException("安装包超过大小上限，已拒绝下载")
                        }
                        val now = SystemClock.elapsedRealtime()
                        if (now - lastReport >= PROGRESS_INTERVAL_MS) {
                            lastReport = now
                            onProgress(written, total)
                        }
                    }
                }
                output.fd.sync()
            }

            onProgress(written, total)
            if (release.sizeBytes > 0L && written != release.sizeBytes) {
                throw UpdateException("安装包大小与服务器记录不一致")
            }
            val actual = digest.digest().toHexString()
            if (!actual.equals(release.sha256, ignoreCase = true)) {
                throw UpdateException("安装包校验失败（SHA-256 不一致）")
            }
            if (target.exists()) target.delete()
            if (!part.renameTo(target)) {
                part.copyTo(target, overwrite = true)
                part.delete()
            }
            target
        } catch (cancelled: CancellationException) {
            throw cancelled
        } catch (expected: UpdateException) {
            throw expected
        } catch (error: IOException) {
            throw UpdateException("下载失败：${error.message ?: "网络错误"}")
        } finally {
            cancellationHandle?.dispose()
            runCatching { connection?.disconnect() }
            if (part.exists()) part.delete()
            if (target.exists() && target.length() == 0L) target.delete()
        }
    }
}
