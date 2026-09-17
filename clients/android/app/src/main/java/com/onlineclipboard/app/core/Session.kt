package com.onlineclipboard.app.core

import org.json.JSONObject
import java.nio.charset.StandardCharsets

class AppSession {
    var origin: String = ""
    var userId: String = ""
    var deviceId: String = CryptoV1.uuid()
    var deviceName: String = "Android"
    var vaultId: String = ""
    var accessToken: String = ""
    var refreshToken: String = ""
    var cmk: ByteArray? = null
    var syncEpoch: String = ""
    var cursor: String = "0"
    var capture: Boolean = false
    var autoWrite: Boolean = true

    fun encrypt(text: String, intent: String): JSONObject {
        val key = cmk ?: throw IllegalStateException("locked")
        val id = CryptoV1.uuid()
        val nonce = CryptoV1.random(12)
        val plain = text.toByteArray(StandardCharsets.UTF_8)
        require(plain.isNotEmpty() && plain.size <= 65536)
        val ct = CryptoV1.encryptItem(key, vaultId, userId, id, deviceId, 1, nonce, plain)
        return JSONObject()
            .put("id", id)
            .put("vault_id", vaultId)
            .put("source_device_id", deviceId)
            .put("format_version", 1)
            .put("key_epoch", 1)
            .put("content_type", "text/plain")
            .put("nonce", CryptoV1.encode(nonce))
            .put("ciphertext", CryptoV1.encode(ct))
            .put("delivery_intent", intent)
    }

    fun decrypt(clip: JSONObject): String {
        val key = cmk ?: throw IllegalStateException("locked")
        val plain = CryptoV1.decryptItem(
            key,
            clip.getString("vault_id"),
            userId,
            clip.getString("id"),
            clip.getString("source_device_id"),
            clip.optInt("key_epoch", 1),
            CryptoV1.decode(clip.getString("nonce")),
            CryptoV1.decode(clip.getString("ciphertext"))
        )
        return String(plain, StandardCharsets.UTF_8)
    }

    fun persist(store: LocalStore) {
        store.set("origin", origin)
        store.set("user_id", userId)
        store.set("device_id", deviceId)
        store.set("device_name", deviceName)
        store.set("vault_id", vaultId)
        store.set("sync_epoch", syncEpoch)
        store.set("cursor", cursor)
        store.set("capture", if (capture) "1" else "0")
        store.set("auto_write", if (autoWrite) "1" else "0")
        if (accessToken.isNotEmpty()) store.set("access_token", LocalProtect.wrap(accessToken.toByteArray(Charsets.UTF_8)))
        if (refreshToken.isNotEmpty()) store.set("refresh_token", LocalProtect.wrap(refreshToken.toByteArray(Charsets.UTF_8)))
        val key = cmk
        if (key != null) store.set("cmk", LocalProtect.wrap(key)) else store.delete("cmk")
    }

    companion object {
        fun restore(store: LocalStore): AppSession? {
            val origin = store.getString("origin") ?: return null
            val s = AppSession()
            s.origin = origin
            s.userId = store.getString("user_id") ?: ""
            s.deviceId = store.getString("device_id") ?: CryptoV1.uuid()
            s.deviceName = store.getString("device_name") ?: "Android"
            s.vaultId = store.getString("vault_id") ?: ""
            s.syncEpoch = store.getString("sync_epoch") ?: ""
            s.cursor = store.getString("cursor") ?: "0"
            s.capture = store.getString("capture") == "1"
            s.autoWrite = store.getString("auto_write") != "0"
            try {
                store.getBytes("access_token")?.let { s.accessToken = String(LocalProtect.unwrap(it), Charsets.UTF_8) }
                store.getBytes("refresh_token")?.let { s.refreshToken = String(LocalProtect.unwrap(it), Charsets.UTF_8) }
                store.getBytes("cmk")?.let { s.cmk = LocalProtect.unwrap(it) }
            } catch (_: Exception) {
                s.accessToken = ""
                s.refreshToken = ""
                s.cmk = null
            }
            return s
        }
    }
}

class SyncEngine(
    var api: ApiClient,
    val store: LocalStore,
    var session: AppSession
) {
    var catchingUp = true
    var status = "未连接"
    var generation = 0L

    fun runOnce(writeClipboard: (String, String) -> Unit) {
        api.accessToken = session.accessToken
        if (session.accessToken.isEmpty()) throw ApiError(401, "UNAUTHENTICATED", "需要登录。")
        try {
            flushOutbox()
            if (session.syncEpoch.isEmpty() || session.cursor.isEmpty()) rebuild(writeClipboard)
            catchingUp = false
            pull(writeClipboard)
            status = "已同步"
        } catch (ex: ApiError) {
            if (ex.code == "UNAUTHENTICATED" || ex.code == "TOKEN_EXPIRED") {
                refresh()
            }
            throw ex
        }
    }

    fun refresh() {
        if (session.refreshToken.isEmpty()) throw ApiError(401, "UNAUTHENTICATED", "需要重新登录。")
        val pair = api.refresh(session.refreshToken)
        session.accessToken = pair.getString("access_token")
        session.refreshToken = pair.getString("refresh_token")
        api.accessToken = session.accessToken
        session.persist(store)
    }

    fun enqueue(text: String, live: Boolean) {
        val env = session.encrypt(text, if (live) "live" else "history_only")
        store.addOutbox(env.getString("id"), env.toString())
        store.upsertClip(
            HistoryItem(
                env.getString("id"), "active", 1, "0",
                java.time.Instant.now().toString(), null, session.deviceId, text, LocalStore.previewOf(text)
            ),
            session.vaultId, env.getString("nonce"), env.getString("ciphertext")
        )
        try {
            flushOutbox()
        } catch (_: Exception) {
            status = "待上传 ${store.outboxCount()}"
        }
    }

    private fun flushOutbox() {
        for ((id, json) in store.outbox()) {
            val env = JSONObject(json)
            val receipt = api.createClip(env)
            store.removeOutbox(id)
            val items = store.loadClips("active") + store.loadClips("trash")
            items.find { it.id == id }?.let {
                it.version = receipt.optInt("version", 1)
                it.createdSeq = receipt.optString("seq", it.createdSeq)
                store.upsertClip(it, env.getString("vault_id"), env.getString("nonce"), env.getString("ciphertext"))
            }
        }
    }

    private fun rebuild(writeClipboard: (String, String) -> Unit) {
        status = "正在校准历史…"
        catchingUp = true
        val snap = api.beginSnapshot()
        var page: String? = null
        do {
            val batch = api.snapshotPage(snap.getString("token"), page)
            val items = batch.optJSONArray("items")
            if (items != null) {
                for (i in 0 until items.length()) applyRemote(items.getJSONObject(i), false, writeClipboard)
            }
            page = batch.optString("next_page_token").ifEmpty { null }
        } while (!page.isNullOrEmpty())
        session.syncEpoch = snap.getString("sync_epoch")
        session.cursor = snap.getString("base_seq")
        session.persist(store)
        catchingUp = false
        pull(writeClipboard)
    }

    private fun pull(writeClipboard: (String, String) -> Unit) {
        if (session.syncEpoch.isEmpty()) return
        while (true) {
            val page = try {
                api.changes(session.syncEpoch, session.cursor)
            } catch (ex: ApiError) {
                if (ex.code == "CURSOR_EXPIRED" || ex.code == "SYNC_RESET_REQUIRED") {
                    session.syncEpoch = ""
                    session.cursor = "0"
                    rebuild(writeClipboard)
                    return
                }
                throw ex
            }
            val events = page.optJSONArray("events")
            val serverTime = page.optString("server_time")
            if (events != null) {
                for (i in 0 until events.length()) {
                    val ev = events.getJSONObject(i)
                    try {
                        val clip = api.getClip(ev.getString("clip_id"))
                        val createdAt = clip.optString("created_at")
                        val live = !catchingUp &&
                            ev.optString("kind") == "clip.created" &&
                            clip.optString("delivery_intent") == "live" &&
                            clip.optString("source_device_id") != session.deviceId &&
                            session.autoWrite &&
                            withinSeconds(serverTime, createdAt, 30)
                        val gen = generation
                        applyRemote(clip, live && generation == gen, writeClipboard)
                    } catch (ex: ApiError) {
                        if (ex.code == "CLIP_PURGED" || ex.code == "TRASH_EXPIRED") {
                            store.deleteClip(ev.getString("clip_id"))
                        } else throw ex
                    }
                }
            }
            session.cursor = page.optString("next_seq", session.cursor)
            session.syncEpoch = page.optString("sync_epoch", session.syncEpoch)
            session.persist(store)
            if (!page.optBoolean("has_more")) break
        }
    }

    private fun applyRemote(clip: JSONObject, write: Boolean, writeClipboard: (String, String) -> Unit) {
        if (session.cmk == null) return
        val text = try {
            session.decrypt(clip)
        } catch (_: Exception) {
            return
        }
        val item = HistoryItem(
            id = clip.getString("id"),
            status = clip.optString("status", "active"),
            version = clip.optInt("version", 1),
            createdSeq = clip.optString("created_seq", clip.optString("last_seq", "0")),
            createdAt = clip.optString("created_at"),
            expiresAt = clip.optString("expires_at").ifEmpty { null },
            sourceDeviceId = clip.optString("source_device_id"),
            text = text,
            preview = LocalStore.previewOf(text)
        )
        store.upsertClip(item, clip.getString("vault_id"), clip.getString("nonce"), clip.getString("ciphertext"))
        if (write) writeClipboard(text, item.id)
    }

    private fun withinSeconds(server: String, created: String, seconds: Long): Boolean {
        return try {
            val s = java.time.Instant.parse(server)
            val c = java.time.Instant.parse(created)
            java.time.Duration.between(c, s).seconds in 0..seconds
        } catch (_: Exception) {
            false
        }
    }
}
