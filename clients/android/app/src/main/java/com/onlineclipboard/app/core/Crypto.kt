package com.onlineclipboard.app.core

import android.util.Base64
import java.nio.charset.StandardCharsets
import javax.crypto.Cipher
import javax.crypto.Mac
import javax.crypto.spec.GCMParameterSpec
import javax.crypto.spec.SecretKeySpec

object CryptoV1 {
    fun encode(raw: ByteArray): String =
        Base64.encodeToString(raw, Base64.URL_SAFE or Base64.NO_WRAP or Base64.NO_PADDING)

    fun decode(value: String): ByteArray {
        val raw = Base64.decode(value, Base64.URL_SAFE or Base64.NO_WRAP or Base64.NO_PADDING)
        if (encode(raw) != value) throw IllegalArgumentException("non-canonical base64url")
        return raw
    }

    fun random(n: Int): ByteArray {
        val buf = ByteArray(n)
        java.security.SecureRandom().nextBytes(buf)
        return buf
    }

    fun hkdf(ikm: ByteArray, salt: ByteArray, info: ByteArray, length: Int = 32): ByteArray {
        val mac = Mac.getInstance("HmacSHA256")
        val saltKey = if (salt.isEmpty()) ByteArray(32) else salt
        mac.init(SecretKeySpec(saltKey, "HmacSHA256"))
        val prk = mac.doFinal(ikm)
        mac.init(SecretKeySpec(prk, "HmacSHA256"))
        mac.update(info)
        mac.update(0x01)
        return mac.doFinal().copyOf(length)
    }

    fun aesGcm(key: ByteArray, nonce: ByteArray, plaintext: ByteArray, aad: ByteArray): ByteArray {
        val c = Cipher.getInstance("AES/GCM/NoPadding")
        c.init(Cipher.ENCRYPT_MODE, SecretKeySpec(key, "AES"), GCMParameterSpec(128, nonce))
        c.updateAAD(aad)
        return c.doFinal(plaintext)
    }

    fun aesGcmOpen(key: ByteArray, nonce: ByteArray, ciphertext: ByteArray, aad: ByteArray): ByteArray {
        val c = Cipher.getInstance("AES/GCM/NoPadding")
        c.init(Cipher.DECRYPT_MODE, SecretKeySpec(key, "AES"), GCMParameterSpec(128, nonce))
        c.updateAAD(aad)
        return c.doFinal(ciphertext)
    }

    fun recoveryCode(rk: ByteArray) = "oc1_" + encode(rk)

    fun parseRecovery(code: String): ByteArray {
        val compact = code.replace(" ", "")
        require(compact.startsWith("oc1_")) { "recovery code must start with oc1_" }
        val raw = decode(compact.removePrefix("oc1_"))
        require(raw.size == 32) { "recovery key must be 32 bytes" }
        return raw
    }

    fun wrapCmk(rk: ByteArray, salt: ByteArray, nonce: ByteArray, cmk: ByteArray, userId: String, vaultId: String): ByteArray {
        val kek = hkdf(rk, salt, "onlineclipboard/v1/wrap".toByteArray(StandardCharsets.UTF_8))
        val aad = "oc-v1|vault|$userId|$vaultId|1".toByteArray(StandardCharsets.UTF_8)
        return aesGcm(kek, nonce, cmk, aad)
    }

    fun unwrapCmk(rk: ByteArray, salt: ByteArray, nonce: ByteArray, wrapped: ByteArray, userId: String, vaultId: String): ByteArray {
        val kek = hkdf(rk, salt, "onlineclipboard/v1/wrap".toByteArray(StandardCharsets.UTF_8))
        val aad = "oc-v1|vault|$userId|$vaultId|1".toByteArray(StandardCharsets.UTF_8)
        return aesGcmOpen(kek, nonce, wrapped, aad)
    }

    fun encryptItem(
        cmk: ByteArray, vaultId: String, userId: String, clipId: String, sourceDeviceId: String,
        epoch: Int, nonce: ByteArray, plaintext: ByteArray
    ): ByteArray {
        val info = "onlineclipboard/v1/item|$clipId|$epoch".toByteArray(StandardCharsets.UTF_8)
        val key = hkdf(cmk, vaultId.toByteArray(StandardCharsets.UTF_8), info)
        val aad = "oc-v1|clip|$userId|$vaultId|$clipId|$sourceDeviceId|$epoch|text/plain".toByteArray(StandardCharsets.UTF_8)
        return aesGcm(key, nonce, plaintext, aad)
    }

    fun decryptItem(
        cmk: ByteArray, vaultId: String, userId: String, clipId: String, sourceDeviceId: String,
        epoch: Int, nonce: ByteArray, ciphertext: ByteArray
    ): ByteArray {
        val info = "onlineclipboard/v1/item|$clipId|$epoch".toByteArray(StandardCharsets.UTF_8)
        val key = hkdf(cmk, vaultId.toByteArray(StandardCharsets.UTF_8), info)
        val aad = "oc-v1|clip|$userId|$vaultId|$clipId|$sourceDeviceId|$epoch|text/plain".toByteArray(StandardCharsets.UTF_8)
        return aesGcmOpen(key, nonce, ciphertext, aad)
    }

    fun uuid(): String = java.util.UUID.randomUUID().toString()
}
