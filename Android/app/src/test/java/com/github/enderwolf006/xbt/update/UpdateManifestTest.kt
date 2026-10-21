package com.github.enderwolf006.xbt.update

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class UpdateManifestTest {
    private val sha = "a".repeat(64)

    private val versionDownloadUrl =
        "https://github.com/12123ads/XBT/releases/download/v2.0.3/version.json"
    private val apkDownloadUrl =
        "https://github.com/12123ads/XBT/releases/download/v2.0.3/xbt-latest.apk"

    private fun release(
        versionUrl: String = versionDownloadUrl,
        apkUrl: String = apkDownloadUrl,
        includeVersion: Boolean = true,
        includeApk: Boolean = true,
    ): String {
        val assets = buildList {
            if (includeVersion) {
                add("""{"name":"version.json","browser_download_url":"$versionUrl"}""")
            }
            if (includeApk) {
                add("""{"name":"xbt-latest.apk","browser_download_url":"$apkUrl"}""")
            }
        }
        return """
            {"tag_name":"v2.0.3","draft":false,"prerelease":false,
             "assets":[${assets.joinToString(",")}]}
        """.trimIndent()
    }

    private fun version(
        versionCode: Long = 5,
        versionName: String = "2.0.3",
        sha256: String = sha,
        sizeBytes: Long = 9390437,
        changelog: String = "修复若干问题",
        minSupportedVersionCode: Long = 3,
    ): String = """
        {"versionCode":$versionCode,"versionName":"$versionName","sha256":"$sha256",
         "sizeBytes":$sizeBytes,"changelog":"$changelog",
         "minSupportedVersionCode":$minSupportedVersionCode}
    """.trimIndent()

    @Test
    fun `newer release is available and not mandatory`() {
        val outcome = parseGitHubRelease(release(), version(), installedVersionCode = 4)
        val available = outcome as UpdateCheckOutcome.Available
        assertEquals(5L, available.release.versionCode)
        assertEquals("2.0.3", available.release.versionName)
        assertEquals(sha, available.release.sha256)
        assertEquals(9390437L, available.release.sizeBytes)
        assertEquals("修复若干问题", available.release.changelog)
        assertFalse(available.mandatory)
        assertEquals(
            "https://ghfast.top/https://github.com/12123ads/XBT/releases/download/v2.0.3/xbt-latest.apk",
            available.release.apkUrl,
        )
    }

    @Test
    fun `equal or older release is up to date`() {
        assertEquals(UpdateCheckOutcome.UpToDate, parseGitHubRelease(release(), version(), 5))
        assertEquals(UpdateCheckOutcome.UpToDate, parseGitHubRelease(release(), version(), 9))
    }

    @Test
    fun `release without version json asset is unavailable`() {
        assertEquals(
            UpdateCheckOutcome.Unavailable,
            parseGitHubRelease(release(includeVersion = false), version(), 4),
        )
    }

    @Test
    fun `release without apk asset is unavailable`() {
        assertEquals(
            UpdateCheckOutcome.Unavailable,
            parseGitHubRelease(release(includeApk = false), version(), 4),
        )
    }

    @Test
    fun `non https apk url is rejected`() {
        val outcome = parseGitHubRelease(
            release(apkUrl = "http://example.test/xbt-latest.apk"),
            version(),
            installedVersionCode = 4,
        )
        assertEquals(
            "更新包地址不是 HTTPS，已拒绝本次更新",
            (outcome as UpdateCheckOutcome.Rejected).message,
        )
    }

    @Test
    fun `non https version json url is unavailable`() {
        assertEquals(
            UpdateCheckOutcome.Unavailable,
            parseGitHubRelease(
                release(versionUrl = "http://example.test/version.json"),
                version(),
                installedVersionCode = 4,
            ),
        )
    }

    @Test
    fun `invalid sha is rejected`() {
        assertTrue(parseGitHubRelease(release(), version(sha256 = "abc"), 4) is UpdateCheckOutcome.Rejected)
        assertTrue(
            parseGitHubRelease(release(), version(sha256 = "z".repeat(64)), 4)
                is UpdateCheckOutcome.Rejected,
        )
    }

    @Test
    fun `negative size is rejected`() {
        assertTrue(
            parseGitHubRelease(release(), version(sizeBytes = -1), 4)
                is UpdateCheckOutcome.Rejected,
        )
    }

    @Test
    fun `sha is normalized to lower case`() {
        val available = parseGitHubRelease(
            release(),
            version(sha256 = "AB".repeat(32)),
            installedVersionCode = 4,
        ) as UpdateCheckOutcome.Available
        assertEquals("ab".repeat(32), available.release.sha256)
    }

    @Test
    fun `unsupported installed version forces mandatory`() {
        val available = parseGitHubRelease(
            release(),
            version(versionCode = 6, minSupportedVersionCode = 6),
            installedVersionCode = 4,
        ) as UpdateCheckOutcome.Available
        assertTrue(available.mandatory)
    }

    @Test
    fun `malformed payloads are unavailable`() {
        assertEquals(UpdateCheckOutcome.Unavailable, parseGitHubRelease("not json", version(), 4))
        assertEquals(
            UpdateCheckOutcome.Unavailable,
            parseGitHubRelease("""{"tag_name":"v2.0.3"}""", version(), 4),
        )
        assertEquals(UpdateCheckOutcome.Unavailable, parseGitHubRelease(release(), "not json", 4))
        assertEquals(
            UpdateCheckOutcome.Unavailable,
            parseGitHubRelease(release(), version(versionCode = 0), 4),
        )
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
