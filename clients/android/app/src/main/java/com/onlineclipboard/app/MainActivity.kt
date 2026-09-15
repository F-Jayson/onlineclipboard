package com.onlineclipboard.app

import android.app.Activity
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.graphics.Color
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.text.InputType
import android.view.View
import android.view.ViewGroup
import android.widget.Button
import android.widget.CheckBox
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.TextView
import android.widget.Toast
import com.onlineclipboard.app.core.ApiClient
import com.onlineclipboard.app.core.ApiError
import com.onlineclipboard.app.core.AppSession
import com.onlineclipboard.app.core.CryptoV1
import com.onlineclipboard.app.core.HistoryItem
import com.onlineclipboard.app.core.LocalStore
import com.onlineclipboard.app.core.Origins
import com.onlineclipboard.app.core.SyncEngine
import org.json.JSONObject

class MainActivity : Activity() {
    private lateinit var store: LocalStore
    private lateinit var session: AppSession
    private var api: ApiClient? = null
    private var sync: SyncEngine? = null
    private val main = Handler(Looper.getMainLooper())
    private var capture = false
    private var autoWrite = true
    private var lastClipHmac: String? = null
    private var suppressUntil = 0L
    private var page = 0
    private var selected: HistoryItem? = null
    private var trashSelected: HistoryItem? = null
    private var devices: List<JSONObject> = emptyList()
    private var deviceIndex = -1

    private lateinit var status: TextView
    private lateinit var originBox: EditText
    private lateinit var userBox: EditText
    private lateinit var passBox: EditText
    private lateinit var inviteBox: EditText
    private lateinit var recoveryBox: EditText
    private lateinit var originStatus: TextView
    private lateinit var keyStatus: TextView
    private lateinit var searchBox: EditText
    private lateinit var historyList: LinearLayout
    private lateinit var detail: TextView
    private lateinit var trashList: LinearLayout
    private lateinit var deviceList: LinearLayout
    private lateinit var capBox: CheckBox
    private lateinit var writeBox: CheckBox
    private lateinit var connectPage: View
    private lateinit var historyPage: View
    private lateinit var trashPage: View
    private lateinit var devicePage: View
    private lateinit var probeResult: TextView

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        store = LocalStore(this)
        session = AppSession.restore(store) ?: AppSession().also {
            it.deviceName = android.os.Build.MODEL.take(64).ifEmpty { "Android" }
        }
        capture = session.capture
        autoWrite = session.autoWrite
        if (session.origin.isNotEmpty()) {
            api = ApiClient(session.origin)
            api!!.accessToken = session.accessToken
            sync = SyncEngine(api!!, store, session)
        }
        setContentView(buildUi())
        refreshHistory()
        handleShare(intent)
        if (session.cmk != null && api != null) bg { runCatching { sync!!.runOnce(::writeRemote) }; main.post { setStatus(sync?.status ?: "已解锁"); refreshHistory() } }
        main.postDelayed(object : Runnable {
            override fun run() {
                if (session.cmk != null && api != null && hasWindowFocus()) {
                    bg {
                        runCatching { sync!!.runOnce(::writeRemote) }
                        main.post { refreshHistory() }
                    }
                }
                main.postDelayed(this, 60_000)
            }
        }, 60_000)
    }

    override fun onNewIntent(intent: Intent?) {
        super.onNewIntent(intent)
        handleShare(intent)
    }

    override fun onWindowFocusChanged(hasFocus: Boolean) {
        super.onWindowFocusChanged(hasFocus)
        if (hasFocus && capture && session.cmk != null) readClipboardIfNew()
    }

    override fun onResume() {
        super.onResume()
        clipboard().addPrimaryClipChangedListener(clipListener)
        if (hasWindowFocus() && capture && session.cmk != null) readClipboardIfNew()
        if (session.cmk != null && api != null) bg { runCatching { sync!!.runOnce(::writeRemote) }; main.post { refreshHistory() } }
    }

    override fun onPause() {
        super.onPause()
        clipboard().removePrimaryClipChangedListener(clipListener)
    }

    private val clipListener = ClipboardManager.OnPrimaryClipChangedListener {
        if (hasWindowFocus() && capture && session.cmk != null) readClipboardIfNew()
    }

    private fun clipboard(): ClipboardManager = getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager

    private fun readClipboardIfNew() {
        if (!hasWindowFocus()) return
        val text = currentClip() ?: return
        val hmac = sha(text)
        if (System.currentTimeMillis() < suppressUntil && hmac == lastClipHmac) return
        lastClipHmac = hmac
        sync?.generation = (sync?.generation ?: 0) + 1
        bg {
            try {
                sync?.enqueue(text, true)
                main.post { setStatus("已采集，正在上传"); refreshHistory() }
            } catch (ex: Exception) {
                main.post { setStatus(ex.message ?: "上传失败") }
            }
        }
    }

    private fun currentClip(): String? {
        val clip = clipboard().primaryClip ?: return null
        if (clip.itemCount == 0) return null
        val text = clip.getItemAt(0).coerceToText(this)?.toString() ?: return null
        val bytes = text.toByteArray(Charsets.UTF_8)
        if (bytes.isEmpty() || bytes.size > 65536) return null
        return text
    }

    private fun writeRemote(text: String, id: String) {
        main.post {
            if (!autoWrite) return@post
            lastClipHmac = sha(text)
            suppressUntil = System.currentTimeMillis() + 2000
            clipboard().setPrimaryClip(ClipData.newPlainText("onlineclipboard", text))
        }
    }

    private fun copyLocal(text: String) {
        lastClipHmac = sha(text)
        suppressUntil = System.currentTimeMillis() + 2000
        clipboard().setPrimaryClip(ClipData.newPlainText("onlineclipboard", text))
        Toast.makeText(this, "已复制到本机（不会再次上传）", Toast.LENGTH_SHORT).show()
    }

    private fun handleShare(intent: Intent?) {
        if (intent?.action != Intent.ACTION_SEND) return
        val text = intent.getStringExtra(Intent.EXTRA_TEXT) ?: return
        if (session.cmk == null || sync == null) {
            store.set("pending_share", text)
            setStatus("已收到分享文字，请先登录并解锁后再同步")
            return
        }
        bg {
            runCatching { sync!!.enqueue(text, true) }
            main.post { setStatus("已从分享上传"); refreshHistory() }
        }
    }

    private fun buildUi(): View {
        val pad = dp(16)
        val root = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setBackgroundColor(Color.parseColor("#F4F6FA"))
            setPadding(pad, pad * 2, pad, pad)
        }
        status = TextView(this).apply {
            textSize = 16f
            text = if (session.cmk == null) "请连接服务器并解锁保险库" else "已解锁，可同步"
        }
        capBox = CheckBox(this).apply {
            text = "前台采集"
            isChecked = capture
            setOnCheckedChangeListener { _, v ->
                capture = v
                session.capture = v
                session.persist(store)
            }
        }
        writeBox = CheckBox(this).apply {
            text = "自动写入"
            isChecked = autoWrite
            setOnCheckedChangeListener { _, v ->
                autoWrite = v
                session.autoWrite = v
                session.persist(store)
            }
        }
        val header = LinearLayout(this).apply { orientation = LinearLayout.HORIZONTAL }
        header.addView(status, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
        header.addView(capBox)
        header.addView(writeBox)
        root.addView(header)

        val tabs = LinearLayout(this).apply { orientation = LinearLayout.HORIZONTAL }
        fun tab(label: String, idx: Int) = Button(this).apply {
            text = label
            setOnClickListener { showPage(idx) }
        }
        tabs.addView(tab("连接", 0), LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
        tabs.addView(tab("历史", 1), LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
        tabs.addView(tab("回收站", 2), LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
        tabs.addView(tab("设备", 3), LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
        root.addView(tabs)

        connectPage = buildConnect()
        historyPage = buildHistory()
        trashPage = buildTrash()
        devicePage = buildDevices()
        root.addView(connectPage, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 1f))
        root.addView(historyPage, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 1f))
        root.addView(trashPage, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 1f))
        root.addView(devicePage, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 1f))
        showPage(0)
        return root
    }

    private fun buildConnect(): View {
        val col = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL; setPadding(0, dp(8), 0, 0) }
        originBox = field(session.origin.ifEmpty { "http://10.0.2.2:8080" })
        userBox = field(store.getString("username") ?: "")
        passBox = field("").apply { inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_PASSWORD }
        inviteBox = field("")
        recoveryBox = field("").apply { minLines = 2 }
        originStatus = TextView(this)
        keyStatus = TextView(this)
        probeResult = TextView(this).apply { textSize = 13f }
        col.addView(label("服务器地址"))
        col.addView(originBox)
        col.addView(row(btn("测试连接") { testOrigin() }, btn("能力检查") { probe() }))
        col.addView(originStatus)
        col.addView(label("用户名"))
        col.addView(userBox)
        col.addView(label("密码（12 位以上）"))
        col.addView(passBox)
        col.addView(label("邀请码（注册）"))
        col.addView(inviteBox)
        col.addView(row(btn("登录") { auth(false) }, btn("注册") { auth(true) }))
        col.addView(label("恢复密钥"))
        col.addView(recoveryBox)
        col.addView(row(btn("解锁保险库") { unlock() }, btn("初始化新保险库") { initVault() }))
        col.addView(keyStatus)
        col.addView(label("普通模式说明"))
        col.addView(TextView(this).apply {
            text = "Android 10+ 仅在本窗口有焦点时读取剪贴板。后台实时同步未宣称支持。可通过系统分享或点“复制”同步。输入法增强尚未实现。"
            textSize = 13f
        })
        col.addView(probeResult)
        return ScrollView(this).apply { addView(col) }
    }

    private fun buildHistory(): View {
        val col = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
        searchBox = field("").apply { hint = "搜索已下载正文" }
        searchBox.addTextChangedListener(SimpleWatcher { refreshHistory() })
        col.addView(row(searchBox, btn("同步") { syncNow() }))
        historyList = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
        col.addView(ScrollView(this).apply {
            addView(historyList)
            layoutParams = LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 1f)
        })
        detail = TextView(this).apply { textSize = 15f }
        col.addView(detail)
        col.addView(row(btn("复制到本机") {
            selected?.let { copyLocal(it.text) }
        }, btn("删除到回收站") { trashSelectedItem() }))
        return col
    }

    private fun buildTrash(): View {
        val col = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
        trashList = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
        col.addView(ScrollView(this).apply {
            addView(trashList)
            layoutParams = LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 1f)
        })
        col.addView(row(btn("恢复") { restoreItem() }, btn("永久删除") { purgeItem() }))
        return col
    }

    private fun buildDevices(): View {
        val col = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
        deviceList = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
        col.addView(ScrollView(this).apply {
            addView(deviceList)
            layoutParams = LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 1f)
        })
        col.addView(btn("撤销选中设备") { revokeSelected() })
        return col
    }

    private fun showPage(idx: Int) {
        page = idx
        connectPage.visibility = if (idx == 0) View.VISIBLE else View.GONE
        historyPage.visibility = if (idx == 1) View.VISIBLE else View.GONE
        trashPage.visibility = if (idx == 2) View.VISIBLE else View.GONE
        devicePage.visibility = if (idx == 3) View.VISIBLE else View.GONE
        if (idx == 3) loadDevices()
    }

    private fun testOrigin() {
        bg {
            try {
                val origin = Origins.normalize(originBox.text.toString())
                val info = ApiClient(origin).serverInfo()
                main.post {
                    originStatus.text = if (info.optBoolean("sync_available"))
                        "已连接 ${info.optString("name")} ${info.optString("version")}，注册模式 ${info.optString("registration_mode")}"
                    else "服务器尚未就绪（stage=${info.optString("stage")}）"
                }
            } catch (ex: Exception) {
                main.post { originStatus.text = ex.message }
            }
        }
    }

    private fun probe() {
        val lines = mutableListOf<String>()
        val focused = hasWindowFocus()
        lines += "窗口焦点: $focused"
        try {
            val text = currentClip()
            lines += if (text == null) "剪贴板读取: 空或不可读（仅前台有焦点时可读其他应用）" else "剪贴板读取: 成功（${text.length} 字符）"
        } catch (ex: Exception) {
            lines += "剪贴板读取: 失败 ${ex.message}"
        }
        try {
            val marker = "oc-probe-${System.currentTimeMillis()}"
            lastClipHmac = sha(marker)
            suppressUntil = System.currentTimeMillis() + 2000
            clipboard().setPrimaryClip(ClipData.newPlainText("probe", marker))
            val back = currentClip()
            lines += if (back == marker) "剪贴板写入: 成功" else "剪贴板写入: 已调用 setPrimaryClip，回读=${back != null}"
        } catch (ex: Exception) {
            lines += "剪贴板写入: 失败 ${ex.message}"
        }
        bg {
            try {
                val origin = Origins.normalize(originBox.text.toString())
                ApiClient(origin).serverInfo()
                lines += "网络: 已访问 server-info"
            } catch (ex: Exception) {
                lines += "网络: 失败 ${ex.message}"
            }
            lines += "系统粘贴: 请到其他应用长按粘贴，验证刚写入的探测文字。本应用不能代替目标应用完成粘贴。"
            lines += "后台实时同步: 未验证，普通模式不宣称支持。"
            main.post { probeResult.text = lines.joinToString("\n") }
        }
    }

    private fun auth(register: Boolean) {
        bg {
            try {
                val origin = Origins.normalize(originBox.text.toString())
                val client = ApiClient(origin)
                val result = if (register) client.register(
                    userBox.text.toString().trim(), passBox.text.toString(),
                    inviteBox.text.toString().trim(), session.deviceId, session.deviceName
                ) else client.login(
                    userBox.text.toString().trim(), passBox.text.toString(),
                    session.deviceId, session.deviceName
                )
                session.origin = origin
                session.userId = result.getString("user_id")
                session.deviceId = result.getString("device_id")
                session.accessToken = result.getString("access_token")
                session.refreshToken = result.getString("refresh_token")
                client.accessToken = session.accessToken
                api = client
                sync = SyncEngine(client, store, session)
                store.set("username", userBox.text.toString().trim())
                session.persist(store)
                main.post {
                    setStatus(if (result.optBoolean("vault_initialized")) "已登录，请输入恢复密钥解锁" else "已登录，请初始化保险库")
                }
            } catch (ex: Exception) {
                main.post { setStatus(ex.message ?: "认证失败") }
            }
        }
    }

    private fun initVault() {
        val client = api ?: return setStatus("请先登录")
        bg {
            try {
                val cmk = CryptoV1.random(32)
                val rk = CryptoV1.random(32)
                val salt = CryptoV1.random(32)
                val nonce = CryptoV1.random(12)
                val vaultId = CryptoV1.uuid()
                val wrapped = CryptoV1.wrapCmk(rk, salt, nonce, cmk, session.userId, vaultId)
                val env = JSONObject()
                    .put("vault_id", vaultId).put("format_version", 1).put("key_epoch", 1)
                    .put("wrap_salt", CryptoV1.encode(salt)).put("wrap_nonce", CryptoV1.encode(nonce))
                    .put("wrapped_key", CryptoV1.encode(wrapped))
                client.putVault(env)
                session.vaultId = vaultId
                session.cmk = cmk
                session.persist(store)
                val code = CryptoV1.recoveryCode(rk)
                main.post {
                    recoveryBox.setText(code)
                    keyStatus.text = "请离线保存恢复密钥。登录密码不能解密历史。"
                    setStatus("保险库已初始化")
                    Toast.makeText(this, "请立即抄写恢复密钥", Toast.LENGTH_LONG).show()
                    flushPendingShare()
                }
            } catch (ex: Exception) {
                main.post { setStatus(ex.message ?: "初始化失败") }
            }
        }
    }

    private fun unlock() {
        val client = api ?: return setStatus("请先登录")
        bg {
            try {
                val vault = client.getVault()
                val rk = CryptoV1.parseRecovery(recoveryBox.text.toString())
                val cmk = CryptoV1.unwrapCmk(
                    rk,
                    CryptoV1.decode(vault.getString("wrap_salt")),
                    CryptoV1.decode(vault.getString("wrap_nonce")),
                    CryptoV1.decode(vault.getString("wrapped_key")),
                    session.userId,
                    vault.getString("vault_id")
                )
                session.vaultId = vault.getString("vault_id")
                session.cmk = cmk
                session.persist(store)
                sync?.session = session
                sync?.runOnce(::writeRemote)
                main.post {
                    keyStatus.text = "已解锁"
                    setStatus("已解锁，开始同步")
                    refreshHistory()
                    flushPendingShare()
                }
            } catch (ex: Exception) {
                main.post {
                    keyStatus.text = "恢复密钥不正确，或信封无法解密。"
                    setStatus(ex.message ?: "解锁失败")
                }
            }
        }
    }

    private fun flushPendingShare() {
        val pending = store.getString("pending_share") ?: return
        if (pending.isEmpty() || sync == null || session.cmk == null) return
        bg {
            runCatching { sync!!.enqueue(pending, true) }
            store.set("pending_share", "")
            main.post { refreshHistory() }
        }
    }

    private fun syncNow() {
        if (session.cmk == null || sync == null) return setStatus("请先解锁")
        bg {
            try {
                sync!!.runOnce(::writeRemote)
                main.post { setStatus("已同步"); refreshHistory(); loadDevices() }
            } catch (ex: Exception) {
                main.post { setStatus(ex.message ?: "同步失败") }
            }
        }
    }

    private fun trashSelectedItem() {
        val item = selected ?: return
        val client = api ?: return
        bg {
            try {
                client.trash(item.id, item.version, CryptoV1.uuid())
                sync?.runOnce(::writeRemote)
                main.post { refreshHistory() }
            } catch (ex: Exception) {
                main.post { setStatus(ex.message ?: "删除失败") }
            }
        }
    }

    private fun restoreItem() {
        val item = trashSelected ?: return
        val client = api ?: return
        bg {
            try {
                client.restore(item.id, item.version, CryptoV1.uuid())
                sync?.runOnce(::writeRemote)
                main.post { refreshHistory() }
            } catch (ex: Exception) {
                main.post { setStatus(ex.message ?: "恢复失败") }
            }
        }
    }

    private fun purgeItem() {
        val item = trashSelected ?: return
        val client = api ?: return
        bg {
            try {
                client.purge(item.id, item.version, CryptoV1.uuid())
                store.deleteClip(item.id)
                sync?.runOnce(::writeRemote)
                main.post { refreshHistory() }
            } catch (ex: Exception) {
                main.post { setStatus(ex.message ?: "永久删除失败") }
            }
        }
    }

    private fun loadDevices() {
        val client = api ?: return
        bg {
            try {
                val page = client.devices()
                val arr = page.optJSONArray("items") ?: return@bg
                val list = mutableListOf<JSONObject>()
                for (i in 0 until arr.length()) list.add(arr.getJSONObject(i))
                devices = list
                main.post {
                    deviceList.removeAllViews()
                    list.forEachIndexed { idx, d ->
                        val row = TextView(this).apply {
                            text = "${d.optString("name")} (${d.optString("platform")})" +
                                if (d.has("revoked_at") && d.get("revoked_at") != JSONObject.NULL) " 已撤销" else ""
                            textSize = 16f
                            setPadding(dp(8), dp(8), dp(8), dp(8))
                            setOnClickListener { deviceIndex = idx }
                        }
                        deviceList.addView(row)
                    }
                }
            } catch (_: Exception) {
            }
        }
    }

    private fun revokeSelected() {
        val client = api ?: return
        if (deviceIndex !in devices.indices) return
        val id = devices[deviceIndex].optString("id")
        bg {
            try {
                client.revokeDevice(id)
                main.post { loadDevices() }
            } catch (ex: Exception) {
                main.post { setStatus(ex.message ?: "撤销失败") }
            }
        }
    }

    private fun refreshHistory() {
        val q = if (::searchBox.isInitialized) searchBox.text.toString().trim() else ""
        var active = store.loadClips("active")
        if (q.isNotEmpty() && session.cmk != null) active = active.filter { it.text.contains(q, true) }
        if (::historyList.isInitialized) {
            historyList.removeAllViews()
            active.forEach { item ->
                historyList.addView(TextView(this).apply {
                    text = item.preview
                    textSize = 16f
                    setPadding(dp(8), dp(10), dp(8), dp(10))
                    setOnClickListener {
                        selected = item
                        detail.text = item.text
                    }
                })
            }
        }
        if (::trashList.isInitialized) {
            trashList.removeAllViews()
            store.loadClips("trash").forEach { item ->
                trashList.addView(TextView(this).apply {
                    text = item.preview + if (item.expiresAt != null) "  到期 ${item.expiresAt}" else ""
                    textSize = 16f
                    setPadding(dp(8), dp(10), dp(8), dp(10))
                    setOnClickListener { trashSelected = item }
                })
            }
        }
        if (q.isNotEmpty()) setStatus("搜索已下载的 ${active.size} 条")
    }

    private fun setStatus(text: String) {
        if (::status.isInitialized) status.text = text
    }

    private fun label(text: String) = TextView(this).apply { this.text = text; setPadding(0, dp(8), 0, dp(4)) }
    private fun field(value: String) = EditText(this).apply { setText(value); setSingleLine() }
    private fun btn(text: String, click: () -> Unit) = Button(this).apply { this.text = text; setOnClickListener { click() } }
    private fun row(a: View, b: View) = LinearLayout(this).apply {
        orientation = LinearLayout.HORIZONTAL
        addView(a, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
        addView(b, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
    }
    private fun dp(v: Int) = (v * resources.displayMetrics.density).toInt()
    private fun bg(block: () -> Unit) {
        Thread(block).start()
    }
    private fun sha(text: String) = java.security.MessageDigest.getInstance("SHA-256")
        .digest(text.toByteArray()).joinToString("") { "%02x".format(it) }
}

private class SimpleWatcher(val onChange: () -> Unit) : android.text.TextWatcher {
    override fun beforeTextChanged(s: CharSequence?, start: Int, count: Int, after: Int) {}
    override fun onTextChanged(s: CharSequence?, start: Int, before: Int, count: Int) {}
    override fun afterTextChanged(s: android.text.Editable?) { onChange() }
}
