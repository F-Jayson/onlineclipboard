# 实现状态

日期：2026-09-17。对照 [开发计划](09-development-plan.md)。

## 已完成

- 服务端 Go + PostgreSQL：认证、保险库 CAS、密文条目、增量/快照、回收站 168 小时、到期清理作业、设备撤销、刷新令牌轮换与重用检测、WebSocket 变化提示。
- 登录密码包装信封：客户端 Argon2id + HKDF 包装 CMK，服务端只存密文；`PUT /vault/password-wrap` 可补写。恢复密钥仍作备用。
- 注册模式：`invite` / `open` / `email`（SMTP 或 log 验证码）/ `external`（HTTP 校验外部站点账号并 JIT 开通剪贴板用户）。
- 启动自动迁移；`clipd migrate`、`clipd admin invite|reset-password|rotate-sync-epoch`。
- Windows WPF：服务器地址、按 server-info 切换注册 UI、登录后密码自动解锁、剪贴板监听与防循环写入、历史搜索、删除/恢复/永久删除、设备撤销、离线 outbox。
- Android 普通模式：前台焦点读取、写入、分享 `ACTION_SEND`、历史/回收站/设备、能力检查入口、密码包装解锁。未实现输入法增强。
- 加密 v1：Go / .NET 对照 `contracts/crypto-v1-vectors.json`（含 password_wrap）；Android 使用同一 HKDF-SHA256 + AES-256-GCM 与 Argon2id 布局。
- Compose 模板：postgres + api 默认启动；`https` profile 启动 Caddy；支持 external/SMTP 环境变量。

## 实际验证结果

| 项目 | 结果 |
| --- | --- |
| `go test ./...`（真实 PostgreSQL 16，`127.0.0.1:5432`） | 通过：含 email 注册、external 登录/本地回退、password wrap 初始化与补写 |
| 本机 `clipd` `127.0.0.1:18080` HTTP 冒烟 | 通过：register/vault/create/replay/trash/snapshot/changes；另一账号 404 |
| Windows `dotnet test` + `dotnet build -c Release` | 通过（向量 3 项，含 password wrap） |
| Android `:app:assembleDebug` | 通过，APK 位于 `clients/android/app/build/outputs/apk/debug/app-debug.apk` |
| 跨端加密向量 | Go 与 Windows 已跑同一组向量；Android 未在 JVM/真机跑向量（实现与向量一致，**未作为仪器测试通过**） |
| 双端真实剪贴板互相同步 | **未验证**：无已连接的 Android 真机/模拟器会话完成复制往返 |
| 防循环（远端写入不回传） | Windows 代码含 HMAC/序号抑制；**未做双机手工验收** |
| Compose 镜像构建与备份演练 | **未验证**：本机已有 `oc-postgres`（16），未成功拉取 `postgres:17-alpine` 发布镜像 |
| IME / 全后台实时同步 | **未实现 / 未宣称** |

## 尚未完成

- Android 输入法增强（P1）与厂商后台矩阵。
- 签名安装包、自动更新、托盘常驻。
- 私有 CA 导入 UI（Android `ApiClient` 已有 PEM 信任代码路径，连接页未提供导入）。
- 正式发布镜像 digest 锁定与 CI。
- Windows 锁屏停止写入、睡眠恢复 CatchingUp 的完整桌面生命周期。

## 环境限制和阻塞

- Docker Hub 拉取 `postgres:17-alpine` 曾失败；开发验证使用已有 `postgres:16-alpine` 容器 `oc-postgres` 映射 `127.0.0.1:55432`。
- 无已连接 Android 设备，无法记录厂商/焦点/Doze 实测。
- Gradle 8.13 Wrapper 已写入；本机用已缓存 Gradle 9.4.1 完成 APK（AGP 8.13 与 Gradle ≥9.6 不兼容）。

## 下一步任务

1. 在模拟器或真机安装 debug APK，用「能力检查」记录读/写/网络，再与 Windows 做同一账号文字往返。
2. 按 [自托管](08-self-hosting.md) 在干净机器用 Compose 构建 api，演练 `pg_dump`/`pg_restore`。
3. 为 Android 增加仪器测试：向量解密、分享上传、焦点外读取应失败。
4. 需要时再评估 IME；不得把未验证后台读取写成已支持。
