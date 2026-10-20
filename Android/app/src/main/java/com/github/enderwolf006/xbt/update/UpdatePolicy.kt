package com.github.enderwolf006.xbt.update

/** Non-mandatory updates for the same version prompt at most once per 24h. */
internal const val UPDATE_PROMPT_INTERVAL_MS = 24L * 60L * 60L * 1000L

internal fun shouldPromptForVersion(
    lastPromptedVersion: Long,
    lastPromptedAt: Long,
    versionCode: Long,
    now: Long,
): Boolean {
    if (lastPromptedVersion != versionCode) return true
    // Missing or future timestamps (clock rollback) must not suppress forever.
    if (lastPromptedAt <= 0L || lastPromptedAt > now) return true
    return now - lastPromptedAt >= UPDATE_PROMPT_INTERVAL_MS
}
