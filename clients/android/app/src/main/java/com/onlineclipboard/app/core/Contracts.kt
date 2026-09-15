package com.onlineclipboard.app.core

// Implementations must check window focus / IME state before clipboard access.
// A foreground service alone does not grant background clipboard read access.
interface ClipboardAdapter {
    val canReadNow: Boolean
    val canWriteNow: Boolean
    suspend fun readText(): String?
    suspend fun writeText(text: String, sourceClipId: String)
}

interface SyncCoordinator {
    suspend fun catchUp()
    suspend fun pause()
}

interface LocalVault {
    val isUnlocked: Boolean
    suspend fun unlock(recoveryCode: String)
    fun lock()
}
