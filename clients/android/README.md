# Android 客户端

环境：JDK 17/21、Android SDK 36、Gradle 9.7.1（本仓库 Wrapper）、AGP 9.4.0。

```powershell
./gradlew.bat :app:assembleDebug
```

产物：`app/build/outputs/apk/debug/app-debug.apk`（debug 签名，仅用于开发安装）。

`local.properties` 不要提交；设置 `ANDROID_HOME` 或由 Android Studio 生成 sdk.dir。

## 普通模式能力

- 仅在本 Activity **窗口有焦点** 且开启「前台采集」时读取剪贴板。
- 支持系统分享 `text/plain` 上传。
- 历史中点击「复制到本机」调用 `setPrimaryClip`。
- 回前台时补拉增量；默认不以后台前台服务维持剪贴板读取。
- 连接页「能力检查」分别探测读取、写入和 server-info 网络；系统粘贴需到其他应用手工确认。

**不宣称**所有手机在后台实时同步。输入法增强未实现。非 loopback 地址必须 HTTPS，且不关闭证书校验。模拟器访问开发机使用 `http://10.0.2.2:端口`。
