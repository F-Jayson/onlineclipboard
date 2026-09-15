# Android 客户端骨架

环境基线：Android Studio、Android SDK 36、JDK 17 或 21、Gradle 8.13。AGP 8.13.2 / Kotlin 2.2.21 固定于构建脚本，后续增加 Compose 和测试依赖时需同步校验兼容关系。

当前仓库**未包含 Gradle Wrapper 二进制与脚本**。先安装 Gradle 8.13，在本目录生成 Wrapper（需要网络下载）：

```powershell
gradle wrapper --gradle-version 8.13 --distribution-type bin
./gradlew.bat :app:assembleDebug
```

Linux/macOS 的第二步使用 `./gradlew :app:assembleDebug`。Android SDK 路径通过 Android Studio 的 `local.properties` 或 ANDROID_HOME 设置；local.properties 不提交仓库。生成 Wrapper 后，校验官方 distributionSha256Sum 并将 wrapper 脚本/JAR/配置一起纳入后续开发提交。

也可由 Android Studio 打开本目录并配置本地 Gradle 8.13，再执行构建。当前只有静态说明 Activity 和接口，没有读取/写入系统剪贴板、请求网络、启动后台服务或注册 IME。

普通模式与输入法增强的权限、交互及真机验收见 [客户端设计](../../docs/07-clients.md)。后续正式 UI 阶段适配 Android 15+ edge-to-edge 与窗口 insets，并加入 Compose；骨架静态入口不作为正式界面验收。
