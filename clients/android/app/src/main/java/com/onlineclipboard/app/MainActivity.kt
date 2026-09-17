package com.onlineclipboard.app

import android.app.Activity
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.view.LayoutInflater
import android.view.View
import android.widget.EditText
import android.widget.ImageView
import android.widget.LinearLayout
import android.widget.Switch
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
    private lateinit var capBox: Switch
    private lateinit var writeBox: Switch
    private lateinit var connectPage: View
    private lateinit var historyPage: View
    private lateinit var trashPage: View
    private lateinit var devicePage: View
    private lateinit var probeResult: TextView
    private lateinit var historyEmpty: View
    private lateinit var trashEmpty: View
    private lateinit var deviceEmpty: View
    private lateinit var tabConnect: LinearLayout
    private lateinit var tabHistory: LinearLayout
    private lateinit var tabTrash: LinearLayout
    private lateinit var tabDevices: LinearLayout

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
        setContentView(R.layout.activity_main)
        bindViews()
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

    private fun bindViews() {
        status = findViewById(R.id.status)
        originBox = findViewById(R.id.originBox)
        userBox = findViewById(R.id.userBox)
        passBox = findViewById(R.id.passBox)
        inviteBox = findViewById(R.id.inviteBox)
        recoveryBox = findViewById(R.id.recoveryBox)
        originStatus = findViewById(R.id.originStatus)
        keyStatus = findViewById(R.id.keyStatus)
        searchBox = findViewById(R.id.searchBox)
        historyList = findViewById(R.id.historyList)
        detail = findViewById(R.id.detail)
        trashList = findViewById(R.id.trashList)
        deviceList = findViewById(R.id.deviceList)
        capBox = findViewById(R.id.capBox)
        writeBox = findViewById(R.id.writeBox)
        connectPage = findViewById(R.id.pageConnect)
        historyPage = findViewById(R.id.pageHistory)
        trashPage = findViewById(R.id.pageTrash)
        devicePage = findViewById(R.id.pageDevices)
        probeResult = findViewById(R.id.probeResult)
        historyEmpty = findViewById(R.id.historyEmpty)
        trashEmpty = findViewById(R.id.trashEmpty)
        deviceEmpty = findViewById(R.id.deviceEmpty)
        tabConnect = findViewById(R.id.tabConnect)
        tabHistory = findViewById(R.id.tabHistory)
        tabTrash = findViewById(R.id.tabTrash)
        tabDevices = findViewById(R.id.tabDevices)

        originBox.setText(session.origin.ifEmpty { "https://clp.fjayson.com" })
        userBox.setText(store.getString("username") ?: "")
        capBox.isChecked = capture
        writeBox.isChecked = autoWrite
        status.text = if (session.cmk == null) "请先解锁" else "已解锁"

        capBox.setOnCheckedChangeListener { _, v ->
            capture = v
            session.capture = v
            session.persist(store)
        }
        writeBox.setOnCheckedChangeListener { _, v ->
            autoWrite = v
            session.autoWrite = v
            session.persist(store)
        }
        searchBox.addTextChangedListener(SimpleWatcher { refreshHistory() })
        findViewById<View>(R.id.btnTest).setOnClickListener { testOrigin() }
        findViewById<View>(R.id.btnProbe).setOnClickListener { probe() }
        findViewById<View>(R.id.btnLogin).setOnClickListener { auth(false) }
        findViewById<View>(R.id.btnRegister).setOnClickListener { auth(true) }
        findViewById<View>(R.id.btnUnlock).setOnClickListener { unlock() }
        findViewById<View>(R.id.btnInitVault).setOnClickListener { initVault() }
        findViewById<View>(R.id.btnSync).setOnClickListener { syncNow() }
        findViewById<View>(R.id.btnCopy).setOnClickListener { selected?.let { copyLocal(it.text) } }
        findViewById<View>(R.id.btnTrash).setOnClickListener { trashSelectedItem() }
        findViewById<View>(R.id.btnRestore).setOnClickListener { restoreItem() }
        findViewById<View>(R.id.btnPurge).setOnClickListener { purgeItem() }
        findViewById<View>(R.id.btnRevoke).setOnClickListener { revokeSelected() }
        tabConnect.setOnClickListener { showPage(0) }
        tabHistory.setOnClickListener { showPage(1) }
        tabTrash.setOnClickListener { showPage(2) }
        tabDevices.setOnClickListener { showPage(3) }
        showPage(0)
    }

    private fun showPage(idx: Int) {
        page = idx
        connectPage.visibility = if (idx == 0) View.VISIBLE else View.GONE
        historyPage.visibility = if (idx == 1) View.VISIBLE else View.GONE
        trashPage.visibility = if (idx == 2) View.VISIBLE else View.GONE
        devicePage.visibility = if (idx == 3) View.VISIBLE else View.GONE
        styleTab(tabConnect, idx == 0)
        styleTab(tabHistory, idx == 1)
        styleTab(tabTrash, idx == 2)
        styleTab(tabDevices, idx == 3)
        if (idx == 3) loadDevices()
    }

    private fun styleTab(tab: LinearLayout, on: Boolean) {
        val color = getColor(if (on) R.color.accent else R.color.muted)
        (tab.getChildAt(0) as ImageView).setColorFilter(color)
        val label = tab.getChildAt(1) as TextView
        label.setTextColor(color)
        label.paint.isFakeBoldText = on
        tab.setBackgroundResource(if (on) R.drawable.bg_tab_active else 0)
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
                main.post { renderDeviceList() }
            } catch (_: Exception) {
            }
        }
    }

    private fun renderDeviceList() {
        if (!::deviceList.isInitialized) return
        deviceList.removeAllViews()
        deviceEmpty.visibility = if (devices.isEmpty()) View.VISIBLE else View.GONE
        devices.forEachIndexed { idx, d ->
            val row = LayoutInflater.from(this).inflate(R.layout.item_device, deviceList, false)
            val revoked = d.has("revoked_at") && d.get("revoked_at") != JSONObject.NULL
            row.findViewById<TextView>(R.id.deviceName).text = d.optString("name")
            row.findViewById<TextView>(R.id.deviceMeta).text = d.optString("platform")
            row.findViewById<TextView>(R.id.deviceStatus).text = if (revoked) "已撤销" else "可用"
            row.setBackgroundResource(if (deviceIndex == idx) R.drawable.bg_card_selected else R.drawable.bg_card)
            row.setOnClickListener {
                deviceIndex = idx
                renderDeviceList()
            }
            deviceList.addView(row)
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
            historyEmpty.visibility = if (active.isEmpty()) View.VISIBLE else View.GONE
            active.forEach { item ->
                val row = LayoutInflater.from(this).inflate(R.layout.item_clip, historyList, false)
                row.findViewById<TextView>(R.id.preview).text = item.preview
                row.findViewById<TextView>(R.id.meta).text = formatTime(item.createdAt)
                row.setBackgroundResource(if (selected?.id == item.id) R.drawable.bg_card_selected else R.drawable.bg_card)
                row.setOnClickListener {
                    selected = item
                    detail.text = item.text
                    refreshHistory()
                }
                historyList.addView(row)
            }
        }
        if (::trashList.isInitialized) {
            val trash = store.loadClips("trash")
            trashList.removeAllViews()
            trashEmpty.visibility = if (trash.isEmpty()) View.VISIBLE else View.GONE
            trash.forEach { item ->
                val row = LayoutInflater.from(this).inflate(R.layout.item_clip, trashList, false)
                row.findViewById<TextView>(R.id.preview).text = item.preview
                row.findViewById<TextView>(R.id.meta).text =
                    if (item.expiresAt != null) "将于 ${formatTime(item.expiresAt)} 永久删除" else "回收站"
                row.findViewById<TextView>(R.id.meta).setTextColor(getColor(R.color.warning))
                row.setBackgroundResource(if (trashSelected?.id == item.id) R.drawable.bg_card_selected else R.drawable.bg_card)
                row.setOnClickListener {
                    trashSelected = item
                    refreshHistory()
                }
                trashList.addView(row)
            }
        }
        if (q.isNotEmpty()) setStatus("搜索已下载的 ${active.size} 条")
    }

    private fun setStatus(text: String) {
        if (!::status.isInitialized) return
        status.text = text
        status.setTextColor(getColor(if (session.cmk != null) R.color.on_accent else R.color.sidebar_muted))
    }

    private fun formatTime(raw: String?): String {
        if (raw.isNullOrBlank()) return ""
        return try {
            val instant = java.time.Instant.parse(raw)
            java.time.format.DateTimeFormatter.ofPattern("yyyy-MM-dd HH:mm")
                .withZone(java.time.ZoneId.systemDefault())
                .format(instant)
        } catch (_: Exception) {
            raw.take(16).replace('T', ' ')
        }
    }

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
