package com.github.enderwolf006.xbt.update

import java.io.File
import java.io.IOException
import java.security.MessageDigest

internal class UpdateException(message: String) : Exception(message)

internal fun ByteArray.toHexString(): String {
    val out = StringBuilder(size * 2)
    for (byte in this) {
        val value = byte.toInt() and 0xFF
        out.append(HEX_CHARS[value ushr 4]).append(HEX_CHARS[value and 0x0F])
    }
    return out.toString()
}

private const val HEX_CHARS = "0123456789abcdef"

internal fun sha256Hex(file: File): String? = try {
    val digest = MessageDigest.getInstance("SHA-256")
    file.inputStream().use { input ->
        val buffer = ByteArray(64 * 1024)
        while (true) {
            val read = input.read(buffer)
            if (read < 0) break
            digest.update(buffer, 0, read)
        }
    }
    digest.digest().toHexString()
} catch (_: IOException) {
    null
}
