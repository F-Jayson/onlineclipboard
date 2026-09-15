# 当前交付验证记录

日期：2026-09-15；环境：Windows，工作目录 `C:\Users\Jayson\Desktop\onlineclipboard`。详细功能清单见 [实现状态](implementation-status.md)。

## 已执行

| 检查 | 结果与范围 |
| --- | --- |
| Go test / 真实 PostgreSQL 16 `127.0.0.1:55432` | 通过：健康、注册、保险库 CAS、创建幂等、回收站到期 410、清理墓碑、刷新重用、游标过期、账号隔离、设备撤销 |
| 本机 clipd `127.0.0.1:18080` | 通过：register/vault/create/replay/trash/snapshot/changes；跨账号 GET 404；`admin invite` 可用 |
| 加密参考向量 | Go `clipcrypto` 与 Windows `CryptoTests` 通过同一 `crypto-v1-vectors.json` |
| Windows Release 构建与测试 | `dotnet test` 2 通过；`dotnet build -c Release` 成功 |
| Android Debug APK | Gradle 9.4.1 + AGP 8.13.2 + SDK 36，`assembleDebug` 成功 |
| OpenAPI x-implementation | 业务路径标为 implemented |

## 未执行及必须标为未验证

- Android 真机/模拟器安装后的剪贴板读、写、系统粘贴、分享上传。
- Windows ↔ Android 同一账号明文往返与防循环手工验收。
- Compose 构建 `postgres:17-alpine` 与 Caddy HTTPS；备份/恢复演练未在干净机器重做。
- 签名安装包、IME、全厂商后台同步。

不得把未验证项计为通过。
