package com.onlineclipboard.app

import android.app.Activity
import android.os.Bundle
import android.widget.LinearLayout
import android.widget.TextView

class MainActivity : Activity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val padding = (24 * resources.displayMetrics.density).toInt()
        val layout = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(padding, padding * 3, padding, padding)
        }
        layout.addView(TextView(this).apply {
            text = "云剪贴板"
            textSize = 28f
        })
        layout.addView(TextView(this).apply {
            text = "\nAndroid 项目骨架\n\n账号、加密、服务器连接、历史和同步尚未实现。\n\nAndroid 后台剪贴板读取存在系统限制，普通模式将提供前台同步和主动分享入口。"
            textSize = 16f
        })
        setContentView(layout)
    }
}
