# 11 · 官方参考资料与核实记录

资料核实日期：2026-09-15。引用用于约束设计，不代表本项目已通过对应平台认证。依赖版本和发布规则可能变化，M0 与发布时需复核。

| 主题 | 官方来源 | 对本项目的影响 |
| --- | --- | --- |
| Android 10 剪贴板访问 | [AOSP Android 10 release notes](https://source.android.com/docs/whatsnew/android-10-release) | 普通后台读取/监听受限，默认 IME 与有焦点应用的条件不同 |
| Android 复制粘贴/敏感预览 | [Copy and paste](https://developer.android.com/develop/ui/compose/touch-input/copy-and-paste) | 系统剪贴板标记和复制反馈；敏感标记不是加密 |
| Android 安全处理 | [Secure clipboard handling](https://developer.android.com/privacy-and-security/risks/secure-clipboard-handling) | 系统可能自动清理剪贴板，不能承诺复制内容无限保留 |
| 后台省电 | [Doze and App Standby](https://developer.android.com/training/monitoring-device-state/doze-standby) | 网络和调度可能延迟，WorkManager 不作实时承诺 |
| 前台服务时长 | [Android 15 behavior changes](https://developer.android.com/about/versions/15/behavior-changes-15) | dataSync FGS 不能设计为无期限常驻 |
| 开机启动服务 | [Foreground service types changes](https://developer.android.com/about/versions/15/changes/foreground-service-types) | 针对 Android 15+ 的 dataSync 开机启动限制 |
| 输入法 | [Create an input method](https://developer.android.com/develop/ui/views/touch-and-input/creating-input-method) | IME 是独立产品能力，需用户主动选择和真机验证 |
| Android 构建版本 | [AGP 8.13 官方记录](https://developer.android.com/build/releases/agp-8-13-0-release-notes) | AGP 8.13.2、Gradle 8.13、JDK 17 基线；支持 SDK 36；Kotlin 2.2.21 为本项目固定选择 |
| Windows 剪贴板监听 | [AddClipboardFormatListener](https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-addclipboardformatlistener) | 用窗口消息驱动采集，正确注册/移除监听 |
| .NET 支持 | [.NET support policy](https://dotnet.microsoft.com/en-us/platform/support/policy) | 选择 .NET 10 LTS；安装 SDK 与运行时是不同前提 |
| Go 基线 | [Go 1.26 release notes](https://go.dev/doc/go1.26) | 选用可用的 1.26 基线，持续修补 |
| 密钥派生 | [RFC 5869 HKDF](https://datatracker.ietf.org/doc/html/rfc5869) | 明确 IKM/salt/info，使用标准实现与固定向量 |
| .NET HKDF | [HKDF API](https://learn.microsoft.com/en-us/dotnet/api/system.security.cryptography.hkdf?view=net-10.0) | Windows 可用平台提供的派生实现 |
| PostgreSQL 行锁 | [Explicit locking](https://www.postgresql.org/docs/17/explicit-locking.html) | 同账号事务序号与固定锁顺序；不能将序列分配顺序当提交顺序 |
| 自托管 HTTPS | [Caddy automatic HTTPS](https://caddyserver.com/docs/automatic-https) | 域名证书自动化、私有 CA 和客户端信任分开处理 |

普通 Android 模式的后台自动写入可靠性、特定厂商进程保活和默认 IME 直接粘贴体验是**本项目需要实测的推断/实现问题**，官方文档不能替代兼容性报告。
