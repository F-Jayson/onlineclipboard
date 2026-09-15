package com.onlineclipboard.app.core

import org.json.JSONObject
import java.io.BufferedReader
import java.io.InputStreamReader
import java.io.OutputStreamWriter
import java.net.HttpURLConnection
import java.net.URL
import java.security.KeyStore
import java.security.cert.CertificateFactory
import javax.net.ssl.HttpsURLConnection
import javax.net.ssl.SSLContext
import javax.net.ssl.TrustManagerFactory

class ApiError(val status: Int, val code: String, message: String) : Exception(message)

class ApiClient(var origin: String, private var customCaPem: String? = null) {
    var accessToken: String? = null

    fun serverInfo(): JSONObject = get("/api/v1/server-info")

    fun register(username: String, password: String, invite: String, deviceId: String, name: String): JSONObject {
        val body = JSONObject()
            .put("username", username).put("password", password).put("invite_token", invite)
            .put("device", JSONObject().put("id", deviceId).put("name", name).put("platform", "android"))
        return send("POST", "/api/v1/auth/register", body, 201)
    }

    fun login(username: String, password: String, deviceId: String, name: String): JSONObject {
        val body = JSONObject()
            .put("username", username).put("password", password)
            .put("device", JSONObject().put("id", deviceId).put("name", name).put("platform", "android"))
        return send("POST", "/api/v1/auth/login", body, 200)
    }

    fun refresh(refreshToken: String): JSONObject =
        send("POST", "/api/v1/auth/refresh", JSONObject().put("refresh_token", refreshToken), 200)

    fun putVault(env: JSONObject): JSONObject =
        send("PUT", "/api/v1/vault", env, 201, mapOf("If-None-Match" to "*"))

    fun getVault(): JSONObject = get("/api/v1/vault")

    fun createClip(env: JSONObject): JSONObject = send("POST", "/api/v1/clips", env, 0)

    fun getClip(id: String): JSONObject = get("/api/v1/clips/$id")

    fun trash(id: String, version: Int, op: String) =
        send("DELETE", "/api/v1/clips/$id", null, 200, mapOf("If-Match" to "\"$version\"", "Idempotency-Key" to op))

    fun restore(id: String, version: Int, op: String) =
        send("POST", "/api/v1/clips/$id/restore", null, 200, mapOf("If-Match" to "\"$version\"", "Idempotency-Key" to op))

    fun purge(id: String, version: Int, op: String) =
        send("DELETE", "/api/v1/clips/$id/permanent", null, 200, mapOf("If-Match" to "\"$version\"", "Idempotency-Key" to op))

    fun changes(epoch: String, after: String): JSONObject = get("/api/v1/sync/changes?epoch=$epoch&after=$after&limit=100")

    fun beginSnapshot(): JSONObject = send("POST", "/api/v1/sync/snapshots", null, 201)

    fun snapshotPage(token: String, page: String?): JSONObject {
        var path = "/api/v1/sync/snapshots/$token?limit=100"
        if (!page.isNullOrEmpty()) path += "&page_token=$page"
        return get(path)
    }

    fun devices(): JSONObject = get("/api/v1/devices")

    fun revokeDevice(id: String) {
        send("DELETE", "/api/v1/devices/$id", null, 204)
    }

    private fun get(path: String) = send("GET", path, null, 200)

    private fun send(method: String, path: String, body: JSONObject?, expected: Int, headers: Map<String, String> = emptyMap()): JSONObject {
        val url = URL(origin.trimEnd('/') + path)
        val conn = url.openConnection() as HttpURLConnection
        applyTrust(conn)
        conn.requestMethod = method
        conn.connectTimeout = 15000
        conn.readTimeout = 20000
        conn.setRequestProperty("Accept", "application/json")
        accessToken?.let { conn.setRequestProperty("Authorization", "Bearer $it") }
        headers.forEach { (k, v) -> conn.setRequestProperty(k, v) }
        if (body != null) {
            conn.doOutput = true
            conn.setRequestProperty("Content-Type", "application/json; charset=utf-8")
            OutputStreamWriter(conn.outputStream, Charsets.UTF_8).use { it.write(body.toString()) }
        }
        val code = conn.responseCode
        val stream = try {
            if (code in 200..299) conn.inputStream else conn.errorStream
        } catch (_: Exception) {
            null
        }
        val text = stream?.let { BufferedReader(InputStreamReader(it, Charsets.UTF_8)).readText() } ?: ""
        conn.disconnect()
        if (expected != 0 && code != expected) throw parseError(code, text)
        if (expected == 0 && code !in listOf(200, 201)) throw parseError(code, text)
        if (text.isEmpty()) return JSONObject()
        return JSONObject(text)
    }

    private fun parseError(code: Int, text: String): ApiError {
        return try {
            val err = JSONObject(text).getJSONObject("error")
            ApiError(code, err.optString("code"), err.optString("message"))
        } catch (_: Exception) {
            ApiError(code, "HTTP_ERROR", text.ifEmpty { "HTTP $code" })
        }
    }

    private fun applyTrust(conn: HttpURLConnection) {
        val pem = customCaPem ?: return
        if (conn !is HttpsURLConnection) return
        val cf = CertificateFactory.getInstance("X.509")
        val cert = cf.generateCertificate(pem.byteInputStream())
        val ks = KeyStore.getInstance(KeyStore.getDefaultType())
        ks.load(null)
        ks.setCertificateEntry("user-ca", cert)
        val tmf = TrustManagerFactory.getInstance(TrustManagerFactory.getDefaultAlgorithm())
        tmf.init(ks)
        val ctx = SSLContext.getInstance("TLS")
        ctx.init(null, tmf.trustManagers, null)
        conn.sslSocketFactory = ctx.socketFactory
    }
}

object Origins {
    fun normalize(input: String): String {
        var value = input.trim()
        require(value.isNotEmpty()) { "请填写服务器地址" }
        require('@' !in value && '?' !in value && '#' !in value) { "地址不能包含用户名、查询或片段" }
        if (!value.contains("://")) value = "https://$value"
        val url = URL(value)
        require(url.protocol == "https" || url.protocol == "http") { "只支持 http 或 https" }
        val path = url.path ?: "/"
        require(path == "" || path == "/") { "首版不支持子路径" }
        val loopback = url.host in setOf("127.0.0.1", "localhost", "::1", "10.0.2.2")
        require(url.protocol != "http" || loopback) { "非本机地址必须使用 HTTPS" }
        val port = if (url.port == -1) "" else ":${url.port}"
        return "${url.protocol}://${url.host}$port"
    }
}
