package com.github.enderwolf006.xbt.update

import android.content.Context

/** Persists the last non-mandatory prompt so the same version is not nagged. */
internal class UpdatePreferences(context: Context) {
    private val preferences = context.getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE)

    fun shouldPrompt(versionCode: Long, now: Long = System.currentTimeMillis()): Boolean =
        shouldPromptForVersion(
            lastPromptedVersion = preferences.getLong(KEY_PROMPTED_VERSION, 0L),
            lastPromptedAt = preferences.getLong(KEY_PROMPTED_AT, 0L),
            versionCode = versionCode,
            now = now,
        )

    fun recordPrompt(versionCode: Long, now: Long = System.currentTimeMillis()) {
        preferences.edit()
            .putLong(KEY_PROMPTED_VERSION, versionCode)
            .putLong(KEY_PROMPTED_AT, now)
            .apply()
    }

    private companion object {
        const val PREFS_NAME = "xbt_update"
        const val KEY_PROMPTED_VERSION = "prompted_version_code"
        const val KEY_PROMPTED_AT = "prompted_at"
    }
}
