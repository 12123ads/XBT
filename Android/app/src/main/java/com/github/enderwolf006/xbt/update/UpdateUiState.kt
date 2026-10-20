package com.github.enderwolf006.xbt.update

/** UI state for the in-app update flow, rendered by [com.github.enderwolf006.xbt.ui.UpdateDialogs]. */
internal sealed interface UpdateUiState {
    data object Idle : UpdateUiState

    data class Available(val release: AppRelease, val mandatory: Boolean) : UpdateUiState

    data class Downloading(
        val release: AppRelease,
        val mandatory: Boolean,
        val bytes: Long,
        val total: Long,
    ) : UpdateUiState

    data class Verifying(val release: AppRelease, val mandatory: Boolean) : UpdateUiState

    data class NeedsInstallPermission(val release: AppRelease, val mandatory: Boolean) : UpdateUiState

    data class Failed(
        val release: AppRelease?,
        val mandatory: Boolean,
        val message: String,
    ) : UpdateUiState
}
