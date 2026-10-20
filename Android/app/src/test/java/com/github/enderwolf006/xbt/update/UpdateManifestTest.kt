package com.github.enderwolf006.xbt.update

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class UpdateManifestTest {
    private val sha = "a".repeat(64)

    private fun manifest(
        versionCode: Long = 4,
        apkUrl: String = "https://example.test/api/app/download/xbt-2.0.2.apk",
        sha256: String = sha,
        mandatory: Boolean = false,
        minSupportedVersionCode: Long = 3,
    ): String = """
        {"latest":{"versionCode":$versionCode,"versionName":"2.0.2",
          "apkUrl":"$apkUrl","sha256":"$sha256","sizeBytes":12345678,
          "changelog":"修复若干问题","publishedAt":"2026-10-21T00:00:00Z"},
         "minSupportedVersionCode":$minSupportedVersionCode,"mandatory":$mandatory}
    """.trimIndent()

    @Test
    fun `newer release is available and not mandatory`() {
        val outcome = parseVersionManifest(manifest(), installedVersionCode = 3)
        val available = outcome as UpdateCheckOutcome.Available
        assertEquals(4L, available.release.versionCode)
        assertEquals("2.0.2", available.release.versionName)
        assertEquals(sha, available.release.sha256)
        assertEquals(12345678L, available.release.sizeBytes)
        assertFalse(available.mandatory)
    }

    @Test
    fun `equal or older release is up to date`() {
        assertEquals(UpdateCheckOutcome.UpToDate, parseVersionManifest(manifest(), 4))
        assertEquals(UpdateCheckOutcome.UpToDate, parseVersionManifest(manifest(), 9))
    }

    @Test
    fun `server mandatory flag is preserved`() {
        val available = parseVersionManifest(manifest(mandatory = true), 3) as UpdateCheckOutcome.Available
        assertTrue(available.mandatory)
    }

    @Test
    fun `unsupported installed version forces mandatory`() {
        val available = parseVersionManifest(manifest(versionCode = 5, minSupportedVersionCode = 5), 4) as UpdateCheckOutcome.Available
        assertTrue(available.mandatory)
    }

    @Test
    fun `non https apk url is rejected`() {
        val outcome = parseVersionManifest(manifest(apkUrl = "http://example.test/xbt.apk"), 3)
        assertEquals("更新包地址不是 HTTPS，已拒绝本次更新", (outcome as UpdateCheckOutcome.Rejected).message)
    }

    @Test
    fun `invalid sha is rejected`() {
        assertTrue(parseVersionManifest(manifest(sha256 = "abc"), 3) is UpdateCheckOutcome.Rejected)
        assertTrue(parseVersionManifest(manifest(sha256 = "z".repeat(64)), 3) is UpdateCheckOutcome.Rejected)
    }

    @Test
    fun `sha is normalized to lower case`() {
        val available = parseVersionManifest(manifest(sha256 = "AB".repeat(32)), 3) as UpdateCheckOutcome.Available
        assertEquals("ab".repeat(32), available.release.sha256)
    }

    @Test
    fun `malformed payload is unavailable`() {
        assertEquals(UpdateCheckOutcome.Unavailable, parseVersionManifest("not json", 3))
        assertEquals(UpdateCheckOutcome.Unavailable, parseVersionManifest("""{"mandatory":false}""", 3))
        assertEquals(UpdateCheckOutcome.Unavailable, parseVersionManifest("""{"latest":{"versionCode":0}}""", 3))
    }

    @Test
    fun `endpoint keeps authority and encodes query`() {
        assertEquals(
            "https://example.test:8443/api/app/version?package=com.github.enderwolf006.xbt&versionCode=3&abi=arm64-v8a",
            buildVersionEndpoint(
                "https://example.test:8443/",
                "com.github.enderwolf006.xbt",
                3,
                "arm64-v8a",
            ),
        )
        assertEquals(
            "https://example.test/api/app/version?package=p&versionCode=7&abi=arm64-v8a",
            buildVersionEndpoint("https://example.test", "p", 7, "arm64-v8a"),
        )
    }

    @Test
    fun `endpoint rejects cleartext base`() {
        assertNull(buildVersionEndpoint("http://example.test/", "p", 3, "arm64-v8a"))
        assertNull(buildVersionEndpoint("not a url", "p", 3, "arm64-v8a"))
    }

    @Test
    fun `prompt throttle allows once per version per day`() {
        val now = 1_000_000_000_000L
        val hour = 60L * 60L * 1000L
        assertTrue(shouldPromptForVersion(0, 0, 4, now))
        assertTrue(shouldPromptForVersion(3, now - hour, 4, now))
        assertFalse(shouldPromptForVersion(4, now - hour, 4, now))
        assertFalse(shouldPromptForVersion(4, now - 23 * hour, 4, now))
        assertTrue(shouldPromptForVersion(4, now - 24 * hour, 4, now))
        // Clock rollback must not suppress the prompt forever.
        assertTrue(shouldPromptForVersion(4, now + hour, 4, now))
    }
}
