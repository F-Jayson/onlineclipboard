package com.onlineclipboard.app.core

import android.content.Context
import android.database.sqlite.SQLiteDatabase
import android.database.sqlite.SQLiteOpenHelper
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

class LocalStore(context: Context) : SQLiteOpenHelper(context, "local.db", null, 1) {
    override fun onCreate(db: SQLiteDatabase) {
        db.execSQL("CREATE TABLE meta(k TEXT PRIMARY KEY, v BLOB)")
        db.execSQL(
            """CREATE TABLE clips(
                id TEXT PRIMARY KEY, status TEXT, version INTEGER, created_seq TEXT,
                created_at TEXT, expires_at TEXT, source_device_id TEXT,
                vault_id TEXT, nonce TEXT, ciphertext TEXT, plaintext TEXT)"""
        )
        db.execSQL("CREATE TABLE outbox(id TEXT PRIMARY KEY, envelope_json TEXT, created_at TEXT)")
    }

    override fun onUpgrade(db: SQLiteDatabase, oldVersion: Int, newVersion: Int) {}

    fun getString(key: String): String? {
        readableDatabase.rawQuery("SELECT v FROM meta WHERE k=?", arrayOf(key)).use { c ->
            if (!c.moveToFirst()) return null
            return c.getBlob(0)?.toString(Charsets.UTF_8)
        }
    }

    fun getBytes(key: String): ByteArray? {
        readableDatabase.rawQuery("SELECT v FROM meta WHERE k=?", arrayOf(key)).use { c ->
            if (!c.moveToFirst()) return null
            return c.getBlob(0)
        }
    }

    fun set(key: String, value: String) = set(key, value.toByteArray(Charsets.UTF_8))

    fun set(key: String, value: ByteArray) {
        writableDatabase.execSQL(
            "INSERT INTO meta(k,v) VALUES(?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v",
            arrayOf(key, value)
        )
    }

    fun upsertClip(item: HistoryItem, vaultId: String, nonce: String, ciphertext: String) {
        writableDatabase.execSQL(
            """INSERT INTO clips(id,status,version,created_seq,created_at,expires_at,source_device_id,vault_id,nonce,ciphertext,plaintext)
               VALUES(?,?,?,?,?,?,?,?,?,?,?)
               ON CONFLICT(id) DO UPDATE SET status=excluded.status, version=excluded.version,
                 created_seq=excluded.created_seq, expires_at=excluded.expires_at,
                 plaintext=excluded.plaintext, nonce=excluded.nonce, ciphertext=excluded.ciphertext""",
            arrayOf(
                item.id, item.status, item.version, item.createdSeq, item.createdAt, item.expiresAt ?: "",
                item.sourceDeviceId, vaultId, nonce, ciphertext, LocalProtect.wrap(item.text.toByteArray(Charsets.UTF_8))
            )
        )
    }

    fun deleteClip(id: String) {
        writableDatabase.execSQL("DELETE FROM clips WHERE id=?", arrayOf(id))
    }

    fun loadClips(status: String): List<HistoryItem> {
        val list = mutableListOf<HistoryItem>()
        readableDatabase.rawQuery(
            "SELECT id,status,version,created_seq,created_at,expires_at,source_device_id,plaintext FROM clips WHERE status=? ORDER BY created_seq DESC",
            arrayOf(status)
        ).use { c ->
            while (c.moveToNext()) {
                val text = readPlain(c, 7)
                list.add(
                    HistoryItem(
                        id = c.getString(0),
                        status = c.getString(1),
                        version = c.getInt(2),
                        createdSeq = c.getString(3),
                        createdAt = c.getString(4),
                        expiresAt = c.getString(5).ifEmpty { null },
                        sourceDeviceId = c.getString(6),
                        text = text,
                        preview = previewOf(text)
                    )
                )
            }
        }
        return list
    }

    fun addOutbox(id: String, json: String) {
        writableDatabase.execSQL(
            "INSERT OR REPLACE INTO outbox(id,envelope_json,created_at) VALUES(?,?,?)",
            arrayOf(id, json, System.currentTimeMillis().toString())
        )
    }

    fun outbox(): List<Pair<String, String>> {
        val list = mutableListOf<Pair<String, String>>()
        readableDatabase.rawQuery("SELECT id, envelope_json FROM outbox ORDER BY created_at", null).use { c ->
            while (c.moveToNext()) list.add(c.getString(0) to c.getString(1))
        }
        return list
    }

    fun removeOutbox(id: String) {
        writableDatabase.execSQL("DELETE FROM outbox WHERE id=?", arrayOf(id))
    }

    fun outboxCount(): Int {
        readableDatabase.rawQuery("SELECT COUNT(*) FROM outbox", null).use { c ->
            return if (c.moveToFirst()) c.getInt(0) else 0
        }
    }

    private fun readPlain(c: android.database.Cursor, i: Int): String {
        if (c.isNull(i)) return ""
        return try {
            val blob = c.getBlob(i)
            String(LocalProtect.unwrap(blob), Charsets.UTF_8)
        } catch (_: Exception) {
            c.getString(i) ?: ""
        }
    }

    companion object {
        fun previewOf(text: String): String {
            val one = text.replace("\r", " ").replace("\n", " ")
            return if (one.length <= 80) one else one.take(80) + "…"
        }
    }
}

data class HistoryItem(
    val id: String,
    var status: String,
    var version: Int,
    var createdSeq: String,
    val createdAt: String,
    var expiresAt: String?,
    val sourceDeviceId: String,
    var text: String,
    var preview: String
) {
    override fun toString(): String = preview
}

object LocalProtect {
    private const val ALIAS = "onlineclipboard-local"

    private fun key(): SecretKey {
        val ks = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        (ks.getKey(ALIAS, null) as? SecretKey)?.let { return it }
        val gen = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore")
        gen.init(
            KeyGenParameterSpec.Builder(
                ALIAS,
                KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT
            )
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setKeySize(256)
                .build()
        )
        return gen.generateKey()
    }

    fun wrap(data: ByteArray): ByteArray {
        val c = Cipher.getInstance("AES/GCM/NoPadding")
        c.init(Cipher.ENCRYPT_MODE, key())
        return c.iv + c.doFinal(data)
    }

    fun unwrap(data: ByteArray): ByteArray {
        require(data.size > 12) { "wrapped local secret too short" }
        val iv = data.copyOfRange(0, 12)
        val ct = data.copyOfRange(12, data.size)
        val c = Cipher.getInstance("AES/GCM/NoPadding")
        c.init(Cipher.DECRYPT_MODE, key(), GCMParameterSpec(128, iv))
        return c.doFinal(ct)
    }
}
